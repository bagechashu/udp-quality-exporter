package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ---------------------- Prometheus ----------------------

var (
	udpIntervalAvgGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "udp_interval_ms_avg",
		Help: "Average packet interval (ms) over the last window duration across all active UDP clients.",
	})

	udpIntervalPercentileGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "udp_interval_ms_percentile",
		Help: "Percentile packet interval (ms) over the last window duration across all active UDP clients.",
	}, []string{"percentile"})

	udpJitterAvgGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "udp_jitter_ms_avg",
		Help: "Average jitter (ms) over the last window duration across all active UDP clients.",
	})

	udpJitterPercentileGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "udp_jitter_ms_percentile",
		Help: "Percentile jitter (ms) over the last window duration across all active UDP clients.",
	}, []string{"percentile"})

	udpJitterStdDevGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "udp_jitter_ms_stddev",
		Help: "Standard deviation of jitter (ms) over the last window duration across all active UDP clients.",
	})

	udpJitterVarianceGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "udp_jitter_ms_variance",
		Help: "Variance of jitter (ms^2) over the last window duration across all active UDP clients.",
	})

	udpJitterCVGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "udp_jitter_ms_cv",
		Help: "Coefficient of variation (stddev/mean) of jitter (ms) across all active UDP clients.",
	})

	udpJitterMADGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "udp_jitter_ms_mad",
		Help: "Mean absolute deviation of jitter (ms) over the last window duration across all active UDP clients.",
	})
	activeClientsGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "udp_active_clients",
		Help: "Number of active UDP clients.",
	})

	mapSizeGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "udp_clients_map_size",
		Help: "Number of current client map size.",
	})

	expiredClientsCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "udp_inactive_clients_removed_total",
		Help: "Total number of UDP clients removed due to inactivity.",
	})

	droppedClientsCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "udp_dropped_clients_total",
		Help: "Total number of UDP clients dropped due to max_clients limit.",
	})

	pendingClientsSizeGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "udp_pending_clients_size",
		Help: "Number of pending_clients waiting for second packet.",
	})
)

// ---------------------- 主逻辑 ----------------------

func main() {
	// 参数定义
	mode := flag.String("mode", "pcap", "Capture mode: 'pcap' or 'afpacket'")
	iface := flag.String("iface", "eth0", "Network interface to capture")
	window := flag.Duration("window", 30*time.Second, "Sliding window duration (e.g. 30s, 1m)")
	inactiveTimeout := flag.Duration("inactive_timeout", 2*time.Minute, "Remove clients inactive for this duration")
	metricsAddr := flag.String("metrics", ":2112", "Prometheus metrics HTTP listen address")
	filterPorts := flag.String("filter_ports", "", "Comma-separated UDP ports to filter (e.g. '9000,9001')")
	maxClients := flag.Int("max_clients", 1000, "Maximum number of tracked clients")
	windowBufferCapTimes := flag.Int("window_buffer_cap", 1, "Multiplier for buffered samples in sliding window (base 1024)")
	percentileArg := flag.Float64("percentile", 90, "Percentile for jitter/interval metrics (e.g. 90, 95, 99)")
	debug := flag.Bool("debug", false, "Enable debug logging")
	pendingTTL := flag.Duration("pending_ttl", 2*time.Second, "TTL for pending clients waiting for second packet")
	pprofAddr := flag.String("pprof", ":6060", "pprof listen address, (only in debug mode)")
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
		udpIntervalAvgGauge,
		udpIntervalPercentileGauge,
		udpJitterAvgGauge,
		udpJitterPercentileGauge,
		udpJitterStdDevGauge,
		udpJitterVarianceGauge,
		udpJitterCVGauge,
		udpJitterMADGauge,
		activeClientsGauge,
		mapSizeGauge,
		expiredClientsCounter,
		droppedClientsCounter,
		pendingClientsSizeGauge,
	)

	clientWindows := sync.Map{}
	pendingClients := sync.Map{} // 防止短流创建

	// buffer capacity multiplier: base 1024 * times
	windowBufferCap := 1024 * (*windowBufferCapTimes)
	if windowBufferCap <= 0 {
		windowBufferCap = 1024
	}

	// ---------------------- pendingClients 清理 Goroutine ----------------------
	if *pendingTTL > 0 {
		go func() {
			t := time.NewTicker(5 * time.Second)
			defer t.Stop()
			for range t.C {
				now := time.Now()
				var pendingCount int
				pendingClients.Range(func(k, v any) bool {
					pendingCount++
					ts := v.(time.Time)
					if now.Sub(ts) > *pendingTTL {
						pendingClients.Delete(k)
					}
					return true
				})
				pendingClientsSizeGauge.Set(float64(pendingCount))
			}
		}()
	}

	// ---------------------- pprof server（可选） ----------------------
	if *debug {
		go func() {
			// pprof on separate port; note: net/http/pprof imported for side effects
			log.Printf("pprof listening on %s (for heap/cpu profiles)", *pprofAddr)
			if err := http.ListenAndServe(*pprofAddr, nil); err != nil {
				log.Printf("pprof server error: %v", err)
			}
		}()
	}

	// 统计循环
	go func() {
		t := time.NewTicker(1 * time.Second)
		defer t.Stop()
		for range t.C {
			now := time.Now()
			var jitterVals, intervalVals []float64
			active := 0
			size := 0
			clientWindows.Range(func(key, val any) bool {
				size++
				win := val.(*slidingWindow)
				last, ok := win.last()
				if !ok || now.Sub(last) > *inactiveTimeout {
					clientWindows.Delete(key)
					expiredClientsCounter.Inc()
					return true
				}
				jitter, interval := win.metricsFast()
				if jitter > 0 {
					jitterVals = append(jitterVals, jitter)
				}
				if interval > 0 {
					intervalVals = append(intervalVals, interval)
				}
				active++
				return true
			})
			activeClientsGauge.Set(float64(active))
			mapSizeGauge.Set(float64(size))

			p := *percentileArg
			pLabel := fmt.Sprintf("%.0f", p)

			// 更新统计 Gauge
			if len(jitterVals) > 0 {
				avg := mean(jitterVals)
				std := stddev(jitterVals)
				varianceVal := variance(jitterVals)
				cv := coefficientOfVariation(jitterVals)
				madVal := mad(jitterVals)

				udpJitterAvgGauge.Set(avg)
				udpJitterPercentileGauge.WithLabelValues(pLabel).Set(percentile(jitterVals, p))
				udpJitterStdDevGauge.Set(std)
				udpJitterVarianceGauge.Set(varianceVal)
				udpJitterCVGauge.Set(cv)
				udpJitterMADGauge.Set(madVal)
			} else {
				udpJitterAvgGauge.Set(0)
				udpJitterPercentileGauge.WithLabelValues(pLabel).Set(0)
				udpJitterStdDevGauge.Set(0)
				udpJitterVarianceGauge.Set(0)
				udpJitterCVGauge.Set(0)
				udpJitterMADGauge.Set(0)
			}

			if len(intervalVals) > 0 {
				udpIntervalAvgGauge.Set(mean(intervalVals))
				udpIntervalPercentileGauge.WithLabelValues(pLabel).Set(percentile(intervalVals, p))
			} else {
				udpIntervalAvgGauge.Set(0)
				udpIntervalPercentileGauge.WithLabelValues(pLabel).Set(0)
			}
		}
	}()

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
		dstPort := udp.DstPort.String()
		client := fmt.Sprintf("%s:%s", srcIP, dstPort)
		meta := packet.Metadata()
		if meta == nil {
			return
		}

		// 防止短流（改进：保留原逻辑，并依赖上面清理 goroutine 清理过期 pending）
		_, exists := clientWindows.Load(client)
		if !exists {
			first, seen := pendingClients.LoadOrStore(client, meta.Timestamp)
			if !seen {
				// 第一次见到，先记录时间，等待下一包
				return
			}
			// seen == true: second packet
			// 如果时间间隔过大，也清理并返回（保持你的原逻辑）
			if meta.Timestamp.Sub(first.(time.Time)) > 1*time.Second {
				pendingClients.Delete(client)
				return
			}
			pendingClients.Delete(client)
			// 防止超过最大 client 限制
			if countSyncMap(&clientWindows) >= *maxClients {
				droppedClientsCounter.Inc()
				return
			}
			clientWindows.Store(client, newSlidingWindow(*window, windowBufferCap))
		}

		val, ok := clientWindows.Load(client)
		if !ok {
			// 极端并发情况下可能已被删除/未创建，安全处理
			return
		}
		win := val.(*slidingWindow)
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
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))

	server := &http.Server{
		Addr:        *metricsAddr,
		Handler:     mux,
		ReadTimeout: 5 * time.Second,
		IdleTimeout: 5 * time.Second,
	}
	log.Printf("Prometheus metrics available at %s/metrics", *metricsAddr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("HTTP server error: %v", err)
	}
}
