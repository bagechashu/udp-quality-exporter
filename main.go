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

// 每个客户端的滑动窗口
type slidingWindow struct {
	mu      sync.Mutex
	samples []sample
	window  time.Duration
}

func (w *slidingWindow) add(s sample) {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := s.ts
	w.samples = append(w.samples, s)

	// 清理窗口外的数据
	cutoff := now.Add(-w.window)
	i := 0
	for _, sm := range w.samples {
		if sm.ts.After(cutoff) {
			break
		}
		i++
	}
	if i > 0 {
		w.samples = w.samples[i:]
	}
}

func (w *slidingWindow) metrics() (lossRate float64, jitter float64, avgInterval float64) {
	w.mu.Lock()
	defer w.mu.Unlock()

	n := len(w.samples)
	if n < 2 {
		return 0, 0, 0
	}

	// 到达间隔
	var intervals []float64
	for i := 1; i < n; i++ {
		iv := w.samples[i].ts.Sub(w.samples[i-1].ts).Seconds() * 1000 // ms
		intervals = append(intervals, iv)
	}

	// 平均间隔
	var sum float64
	for _, iv := range intervals {
		sum += iv
	}
	avgInterval = sum / float64(len(intervals))

	// 抖动 (平均相邻间隔差)
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

	// 丢包率估算 (基于 interval 假设): 如果平均间隔明显偏大，可推测丢包
	// 简化：假设理想间隔为最小间隔
	minInterval := intervals[0]
	for _, iv := range intervals {
		if iv < minInterval {
			minInterval = iv
		}
	}
	expected := (w.samples[n-1].ts.Sub(w.samples[0].ts)).Seconds() * 1000 / minInterval
	lossRate = 1 - float64(len(intervals))/expected
	if lossRate < 0 {
		lossRate = 0
	}

	return
}

// ---------------------- Prometheus 指标 ----------------------

var (
	lossGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "udp_loss_rate_window",
		Help: "Estimated packet loss rate per client (0~1).",
	}, []string{"client"})

	jitterGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "udp_avg_jitter_ms_window",
		Help: "Average jitter per client in sliding window (ms).",
	}, []string{"client"})

	intervalGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "udp_avg_interval_ms_window",
		Help: "Average inter-arrival time per client in sliding window (ms).",
	}, []string{"client"})
)

// ---------------------- 主程序 ----------------------

func main() {
	// 参数
	iface := flag.String("iface", "eth0", "Network interface to capture")
	window := flag.Duration("window", 30*time.Second, "Sliding window duration (e.g. 30s, 1m)")
	metricsAddr := flag.String("metrics", ":2112", "Prometheus metrics HTTP listen address")
	filter := flag.String("filter", "udp", "BPF filter (e.g. 'udp and port 9000')")
	flag.Parse()

	prometheus.MustRegister(lossGauge, jitterGauge, intervalGauge)

	// 客户端状态管理
	clientWindows := make(map[string]*slidingWindow)
	var mu sync.Mutex

	// 定时更新 Prometheus
	go func() {
		for range time.Tick(1 * time.Second) {
			mu.Lock()
			for client, win := range clientWindows {
				loss, jitter, interval := win.metrics()
				lossGauge.WithLabelValues(client).Set(loss)
				jitterGauge.WithLabelValues(client).Set(jitter)
				intervalGauge.WithLabelValues(client).Set(interval)
			}
			mu.Unlock()
		}
	}()

	// 抓包
	handle, err := pcap.OpenLive(*iface, 1600, true, pcap.BlockForever)
	if err != nil {
		log.Fatal(err)
	}
	defer handle.Close()

	if err := handle.SetBPFFilter(*filter); err != nil {
		log.Fatal(err)
	}

	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
	for packet := range packetSource.Packets() {
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

		srcIP := network.NetworkFlow().Src().String()
		srcPort := udp.SrcPort.String()
		client := fmt.Sprintf("%s:%s", srcIP, srcPort)

		mu.Lock()
		win, ok := clientWindows[client]
		if !ok {
			win = &slidingWindow{window: *window}
			clientWindows[client] = win
		}
		win.add(sample{ts: packet.Metadata().Timestamp})
		mu.Unlock()
	}

	// Prometheus HTTP
	http.Handle("/metrics", promhttp.Handler())
	log.Printf("Prometheus metrics available at %s/metrics\n", *metricsAddr)
	log.Fatal(http.ListenAndServe(*metricsAddr, nil))
}
