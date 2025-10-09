package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
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
	// 优化：用二分查找提升清理效率
	cutoff := now.Add(-w.window)
	left, right := 0, len(w.samples)
	for left < right {
		mid := (left + right) / 2
		if w.samples[mid].ts.Before(cutoff) {
			left = mid + 1
		} else {
			right = mid
		}
	}
	if left > 0 {
		w.samples = w.samples[left:]
	}
}

func (w *slidingWindow) isActive(now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.samples) == 0 {
		return false
	}
	// 只要窗口内有数据在 [now-window, now] 范围内就算活跃
	cutoff := now.Add(-w.window)
	return w.samples[len(w.samples)-1].ts.After(cutoff)
}

func (w *slidingWindow) metrics() (jitter float64, avgInterval float64) {
	w.mu.Lock()
	defer w.mu.Unlock()

	n := len(w.samples)
	if n < 2 {
		return 0, 0
	}

	// 到达间隔
	intervals := make([]float64, 0, n-1)
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
)

// ---------------------- 主程序 ----------------------

func main() {
	// 参数
	iface := flag.String("iface", "eth0", "Network interface to capture")
	window := flag.Duration("window", 30*time.Second, "Sliding window duration (e.g. 30s, 1m)")
	metricsAddr := flag.String("metrics", ":2112", "Prometheus metrics HTTP listen address")
	filter := flag.String("filter", "udp", "BPF filter (e.g. 'udp and port 9000')")
	maxClients := flag.Int("max_clients", 10000, "Maximum number of tracked clients")
	flag.Parse()

	registry := prometheus.NewRegistry()
	registry.MustRegister(jitterGauge, intervalGauge)

	// 客户端状态管理
	clientWindows := make(map[string]*slidingWindow)
	var mu sync.Mutex

	// 优雅退出
	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		close(stop)
	}()

	// 定时更新 Prometheus

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				now := time.Now()
				mu.Lock()
				for client, win := range clientWindows {
					if !win.isActive(now) {
						jitterGauge.DeleteLabelValues(client)
						intervalGauge.DeleteLabelValues(client)
						delete(clientWindows, client)
						continue
					}
					jitter, interval := win.metrics()
					win.mu.Lock()
					hasData := len(win.samples) > 1
					win.mu.Unlock()
					if hasData {
						jitterGauge.WithLabelValues(client).Set(jitter)
						intervalGauge.WithLabelValues(client).Set(interval)
					}
				}
				mu.Unlock()
			case <-stop:
				return
			}
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
	go func() {
		for {
			select {
			case packet, ok := <-packetSource.Packets():
				if !ok {
					return
				}
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
				srcIP := network.NetworkFlow().Src().String()
				srcPort := udp.SrcPort.String()
				client := fmt.Sprintf("%s:%s", srcIP, srcPort)
				mu.Lock()
				// 限制客户端数量，防止标签膨胀
				if _, ok := clientWindows[client]; !ok {
					if len(clientWindows) >= *maxClients {
						mu.Unlock()
						continue
					}
					clientWindows[client] = &slidingWindow{window: *window}
				}
				win := clientWindows[client]
				win.add(sample{ts: packet.Metadata().Timestamp})
				mu.Unlock()
			case <-stop:
				return
			}
		}
	}()

	// Prometheus HTTP
	server := &http.Server{
		Addr:    *metricsAddr,
		Handler: promhttp.HandlerFor(registry, promhttp.HandlerOpts{}),
	}
	go func() {
		log.Printf("Prometheus metrics available at %s/metrics\n", *metricsAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	<-stop // 等待信号

	// 优雅关闭 HTTP 服务
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("HTTP server Shutdown: %v", err)
	}
	log.Println("Exporter exited gracefully")
}
