package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

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
	n := r.size
	idx := 0
	for idx < n && r.data[(r.start+idx)%r.cap].ts.Before(cutoff) {
		idx++
	}
	r.start = (r.start + idx) % r.cap
	r.size -= idx
}

// 修改 slidingWindow 用 ringBuffer
type slidingWindow struct {
	mu     sync.Mutex
	buffer *ringBuffer
	window time.Duration
}

func newSlidingWindow(window time.Duration, cap int) *slidingWindow {
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

// ---------------------- Prometheus 指标 ----------------------

var (
	jitterGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "udp_avg_jitter_ms_window",
		Help: "Average jitter per client in sliding window (ms).",
	}, []string{"client"})

	intervalGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "udp_avg_interval_ms_window",
		Help: "Average inter-arrival time per client in sliding window (ms).",
	}, []string{"client"})

	activeClientsGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "udp_active_clients",
		Help: "Number of active UDP clients.",
	})

	droppedClientsCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "udp_dropped_clients_total",
		Help: "Total number of UDP clients dropped due to max_clients limit.",
	})
)

// ---------------------- 主程序 ----------------------

func main() {
	// 参数
	iface := flag.String("iface", "eth0", "Network interface to capture")
	window := flag.Duration("window", 30*time.Second, "Sliding window duration (e.g. 30s, 1m)")
	metricsAddr := flag.String("metrics", ":2112", "Prometheus metrics HTTP listen address")
	filter := flag.String("filter", "udp", "BPF filter (e.g. 'udp and port 9000')")
	maxClients := flag.Int("max_clients", 100, "Maximum number of tracked clients")
	windowBufferCapTimes := flag.Int("window_buffer_cap", 1, "Multiplier for buffered samples in sliding window")
	flag.Parse()

	registry := prometheus.NewRegistry()
	registry.MustRegister(jitterGauge, intervalGauge, activeClientsGauge, droppedClientsCounter)

	// 客户端状态管理
	clientWindows := make(map[string]*slidingWindow)
	var mu sync.RWMutex // 优化为读写锁

	// 计算环形缓冲区容量，windowBufferCapTimes 作为乘数参数
	windowBufferCap := 1024 * (*windowBufferCapTimes) // 环形缓冲区容量，可根据实际流量调整

	// 定时更新 Prometheus
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			mu.Lock()
			activeCount := 0
			for client, win := range clientWindows {
				if !win.isActive(now) {
					jitterGauge.DeleteLabelValues(client)
					intervalGauge.DeleteLabelValues(client)
					delete(clientWindows, client)
					continue
				}
				jitter, interval := win.metrics()
				win.mu.Lock()
				hasData := win.buffer.size > 1
				win.mu.Unlock()
				if hasData {
					jitterGauge.WithLabelValues(client).Set(jitter)
					intervalGauge.WithLabelValues(client).Set(interval)
				}
				activeCount++
			}
			activeClientsGauge.Set(float64(activeCount))
			mu.Unlock()
		}
	}()

	// 抓包
	handle, err := pcap.OpenLive(*iface, 1600, true, pcap.BlockForever)
	if err != nil {
		log.Printf("Failed to open interface %s: %v", *iface, err)
		return
	}
	defer handle.Close()

	if err := handle.SetBPFFilter(*filter); err != nil {
		log.Printf("Failed to set BPF filter '%s': %v", *filter, err)
		return
	}

	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
	go func() {
		for packet := range packetSource.Packets() {
			// ...处理 packet ...
			// 提取五元组
			network := packet.NetworkLayer()
			transport := packet.TransportLayer()
			if network == nil || transport == nil {
				continue
			}
			udp, ok := transport.(*layers.UDP)
			if !ok {
				continue
			}
			// 可选：如需更精确可用五元组
			srcIP := network.NetworkFlow().Src().String()
			srcPort := udp.SrcPort.String()
			// dstIP := network.NetworkFlow().Dst().String()
			// dstPort := udp.DstPort.String()
			// client := fmt.Sprintf("%s:%s-%s:%s", srcIP, srcPort, dstIP, dstPort)

			client := fmt.Sprintf("%s:%s", srcIP, srcPort)

			// 判空，防止 Metadata() 为 nil
			meta := packet.Metadata()
			if meta == nil {
				continue
			}

			mu.RLock()
			win, ok := clientWindows[client]
			mu.RUnlock()
			if !ok {
				mu.Lock()
				// 再次检查，防止并发下重复创建
				if win, ok = clientWindows[client]; !ok {
					if len(clientWindows) >= *maxClients {
						droppedClientsCounter.Inc()
						log.Printf("Max clients reached (%d), dropping new client: %s", *maxClients, client)
						mu.Unlock()
						continue
					}
					win = newSlidingWindow(*window, windowBufferCap)
					clientWindows[client] = win
				}
				mu.Unlock()
			}
			win.add(sample{ts: meta.Timestamp})

			// 已移除高频 map 清理逻辑
		}
	}()

	// Prometheus HTTP
	server := &http.Server{
		Addr:        *metricsAddr,
		Handler:     promhttp.HandlerFor(registry, promhttp.HandlerOpts{}),
		ReadTimeout: 5 * time.Second,
		IdleTimeout: 5 * time.Second,
	}
	log.Printf("Prometheus metrics available at %s/metrics\n", *metricsAddr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("HTTP server error: %v", err)
	}
}
