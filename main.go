package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/afpacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

//
// ---------------------- 数据结构 ----------------------
//

type sample struct {
	ts time.Time
}

// 环形缓冲区实现
type ringBuffer struct {
	data  []sample
	start int
	size  int
	cap   int
}

func newRingBuffer(capacity int) *ringBuffer {
	if capacity <= 0 {
		capacity = 16
	}
	return &ringBuffer{
		data: make([]sample, capacity),
		cap:  capacity,
	}
}

func (r *ringBuffer) append(s sample) {
	if r.size < r.cap {
		r.data[(r.start+r.size)%r.cap] = s
		r.size++
	} else {
		r.data[r.start] = s
		r.start = (r.start + 1) % r.cap
	}
}

func (r *ringBuffer) slice() []sample {
	out := make([]sample, r.size)
	for i := 0; i < r.size; i++ {
		out[i] = r.data[(r.start+i)%r.cap]
	}
	return out
}

func (r *ringBuffer) trimBefore(cutoff time.Time) {
	// 移动 start，减少 size
	n := r.size
	idx := 0
	for idx < n && r.data[(r.start+idx)%r.cap].ts.Before(cutoff) {
		idx++
	}
	if idx > 0 {
		r.start = (r.start + idx) % r.cap
		r.size -= idx
	}
}

// 滑动窗口结构
type slidingWindow struct {
	mu     sync.Mutex
	buffer *ringBuffer
	window time.Duration
}

func newSlidingWindow(window time.Duration, cap int) *slidingWindow {
	if cap <= 0 {
		cap = 16
	}
	return &slidingWindow{
		buffer: newRingBuffer(cap),
		window: window,
	}
}

func (w *slidingWindow) add(s sample) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buffer.append(s)
	cutoff := s.ts.Add(-w.window)
	w.buffer.trimBefore(cutoff)
}

func (w *slidingWindow) isActive(now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buffer.size == 0 {
		return false
	}
	cutoff := now.Add(-w.window)
	samples := w.buffer.slice()
	return samples[len(samples)-1].ts.After(cutoff)
}

// last 返回窗口中最后一个样本的时间戳，如果没有样本返回零时间并返回 false
func (w *slidingWindow) last() (time.Time, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buffer.size == 0 {
		return time.Time{}, false
	}
	samples := w.buffer.slice()
	return samples[len(samples)-1].ts, true
}

func (w *slidingWindow) metrics() (jitter float64, avgInterval float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	samples := w.buffer.slice()
	n := len(samples)
	if n < 2 {
		return 0, 0
	}
	intervals := make([]float64, 0, n-1)
	for i := 1; i < n; i++ {
		iv := samples[i].ts.Sub(samples[i-1].ts).Seconds() * 1000 // ms
		intervals = append(intervals, iv)
	}
	var sum float64
	for _, iv := range intervals {
		sum += iv
	}
	avgInterval = sum / float64(len(intervals))
	var jitterSum float64
	for i := 1; i < len(intervals); i++ {
		d := intervals[i] - intervals[i-1]
		if d < 0 {
			d = -d
		}
		jitterSum += d
	}
	if len(intervals) > 1 {
		jitter = jitterSum / float64(len(intervals)-1)
	}
	return
}

//
// ---------------------- Prometheus 指标 ----------------------
//

var (
	udpJitterSummary = prometheus.NewSummary(prometheus.SummaryOpts{
		Name:       "udp_jitter_ms_summary",
		Help:       "Jitter distribution across all active UDP clients (ms).",
		Objectives: map[float64]float64{0.5: 0.01, 0.9: 0.01, 0.99: 0.001},
	})

	udpIntervalSummary = prometheus.NewSummary(prometheus.SummaryOpts{
		Name:       "udp_interval_ms_summary",
		Help:       "Packet inter-arrival interval distribution across all clients (ms).",
		Objectives: map[float64]float64{0.5: 0.01, 0.9: 0.01, 0.99: 0.001},
	})

	activeClientsGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "udp_active_clients",
		Help: "Number of active UDP clients.",
	})

	activeStreamsGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "udp_active_streams",
		Help: "Number of active UDP streams.",
	})

	droppedClientsCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "udp_dropped_clients_total",
		Help: "Total number of UDP clients dropped due to max_clients limit.",
	})
)

//
// ---------------------- 抓包实现：pcap ----------------------
//

func captureWithPcap(iface, filter string, handlePacket func(gopacket.Packet)) error {
	handle, err := pcap.OpenLive(iface, 1600, true, pcap.BlockForever)
	if err != nil {
		return fmt.Errorf("pcap open failed: %w", err)
	}
	defer handle.Close()

	if filter != "" {
		if err := handle.SetBPFFilter(filter); err != nil {
			return fmt.Errorf("set BPF failed: %w", err)
		}
	}

	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
	log.Printf("[pcap] capturing on %s, filter=%s", iface, filter)
	for packet := range packetSource.Packets() {
		handlePacket(packet)
	}
	return nil
}

//
// ---------------------- 抓包实现：afpacket ----------------------
//

func captureWithAfpacket(iface string, handlePacket func(gopacket.Packet)) error {
	handle, err := afpacket.NewTPacket(
		afpacket.OptInterface(iface),
		afpacket.OptFrameSize(4096),
		afpacket.OptBlockSize(4096*512),
		afpacket.OptNumBlocks(128),
		afpacket.OptPollTimeout(50*time.Millisecond),
	)
	if err != nil {
		return fmt.Errorf("afpacket open failed: %w", err)
	}
	defer handle.Close()

	packetSource := gopacket.NewPacketSource(handle, layers.LinkTypeEthernet)
	log.Printf("[afpacket] capturing on %s", iface)

	for packet := range packetSource.Packets() {
		handlePacket(packet)
	}
	return nil
}

//
// ---------------------- 主程序入口 ----------------------
//

func main() {
	// 参数定义
	mode := flag.String("mode", "pcap", "Capture mode: 'pcap' or 'afpacket'")
	iface := flag.String("iface", "eth0", "Network interface to capture")
	window := flag.Duration("window", 30*time.Second, "Sliding window duration (e.g. 30s, 1m)")
	metricsAddr := flag.String("metrics", ":2112", "Prometheus metrics HTTP listen address")
	filterPorts := flag.String("filter_ports", "", "Comma-separated UDP ports to filter (e.g. '9000,9001')")
	maxClients := flag.Int("max_clients", 100, "Maximum number of tracked clients")
	windowBufferCapTimes := flag.Int("window_buffer_cap", 1, "Multiplier for buffered samples in sliding window (base 1024)")
	debug := flag.Bool("debug", false, "Enable debug logging")
	flag.Parse()

	// ---------------------- 端口过滤 ----------------------
	var filterPortsSet map[int]bool
	var filterStr string
	if *filterPorts != "" {
		filterPortsSet = make(map[int]bool)
		var exprs []string
		for _, p := range strings.Split(*filterPorts, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			num, err := strconv.Atoi(p)
			if err != nil || num < 0 || num > 65535 {
				log.Fatalf("Invalid port number: %q", p)
			}
			filterPortsSet[num] = true
			exprs = append(exprs, fmt.Sprintf("port %d", num))
		}
		// pcap filter
		filterStr = fmt.Sprintf("udp and (%s)", strings.Join(exprs, " or "))
	} else {
		filterStr = "udp"
	}

	// ---------------------- Prometheus 注册 ----------------------
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		udpJitterSummary,
		udpIntervalSummary,
		activeClientsGauge,
		activeStreamsGauge,
		droppedClientsCounter,
	)

	clientWindows := make(map[string]*slidingWindow)
	var mu sync.RWMutex

	// buffer capacity multiplier: base 1024 * times
	windowBufferCap := 1024 * (*windowBufferCapTimes)
	if windowBufferCap <= 0 {
		windowBufferCap = 1024
	}

	// ---------------------- 指标定时更新 ----------------------
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			mu.Lock()
			activeClients := 0
			activeStreams := 0
			// 遍历 clientWindows
			for client, win := range clientWindows {
				if !win.isActive(now) {
					// 超过窗口范围，删除
					delete(clientWindows, client)
					continue
				}
				jitter, interval := win.metrics()

				// 探测包过滤逻辑（简单版：窗口内样本数少于 3 认为是探测/异常）
				win.mu.Lock()
				isProbe := win.buffer.size < 3
				win.mu.Unlock()
				if isProbe {
					continue
				}

				// 汇总全局分布
				if jitter > 0 {
					udpJitterSummary.Observe(jitter)
				}
				if interval > 0 {
					udpIntervalSummary.Observe(interval)
				}

				activeClients++

				// 如果最近一个包在 1 秒内，则认为是活跃流
				if last, ok := win.last(); ok {
					if now.Sub(last) < 1*time.Second {
						activeStreams++
					}
				}
			}
			activeClientsGauge.Set(float64(activeClients))
			activeStreamsGauge.Set(float64(activeStreams))
			mu.Unlock()
		}
	}()

	// ---------------------- 通用包处理函数 ----------------------
	handlePacket := func(packet gopacket.Packet) {
		network := packet.NetworkLayer()
		transport := packet.TransportLayer()
		if network == nil || transport == nil {
			return
		}
		udp, ok := transport.(*layers.UDP)
		if !ok {
			return
		}

		// 端口过滤逻辑（pcap 模式已经做 bpf，但 afpacket 不会）
		if len(filterPortsSet) > 0 {
			port := int(udp.DstPort)
			if !filterPortsSet[port] {
				return
			}
		}

		srcIP := network.NetworkFlow().Src().String()
		srcPort := udp.SrcPort.String()
		client := fmt.Sprintf("%s:%s", srcIP, srcPort)

		meta := packet.Metadata()
		if meta == nil {
			return
		}

		// 时间戳过滤, 过滤掉乱序或系统异常时间戳（时间差过大）
		if time.Since(meta.Timestamp) > 10*time.Second || meta.Timestamp.After(time.Now().Add(2*time.Second)) {
			return
		}

		// 查找或创建 window
		mu.RLock()
		win, ok := clientWindows[client]
		mu.RUnlock()
		if !ok {
			mu.Lock()
			// double-check
			if win, ok = clientWindows[client]; !ok {
				if len(clientWindows) >= *maxClients {
					droppedClientsCounter.Inc()
					if *debug {
						log.Printf("Max clients reached (%d), dropping: %s", *maxClients, client)
					}
					mu.Unlock()
					return
				}
				win = newSlidingWindow(*window, windowBufferCap)
				clientWindows[client] = win
			}
			mu.Unlock()
		}
		win.add(sample{ts: meta.Timestamp})
	}

	// ---------------------- 启动抓包 ----------------------
	go func() {
		switch *mode {
		case "pcap":
			if err := captureWithPcap(*iface, filterStr, handlePacket); err != nil {
				log.Fatalf("PCAP error: %v", err)
			}
		case "afpacket":
			if runtime.GOOS != "linux" {
				log.Fatalf("AF_PACKET mode only supported on Linux")
			}
			if err := captureWithAfpacket(*iface, handlePacket); err != nil {
				log.Fatalf("AF_PACKET error: %v", err)
			}
		default:
			log.Fatalf("Unknown mode: %s (must be 'pcap' or 'afpacket')", *mode)
		}
	}()

	// ---------------------- 启动 Prometheus HTTP ----------------------
	server := &http.Server{
		Addr:        *metricsAddr,
		Handler:     promhttp.HandlerFor(registry, promhttp.HandlerOpts{}),
		ReadTimeout: 5 * time.Second,
		IdleTimeout: 5 * time.Second,
	}
	log.Printf("Prometheus metrics available at %s/metrics", *metricsAddr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("HTTP server error: %v", err)
	}
}
