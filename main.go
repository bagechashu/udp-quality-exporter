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
	"github.com/google/gopacket/layers"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ---------------------- Prometheus ----------------------

var (
	udpJitterAvgGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "udp_jitter_ms_avg",
		Help: "Average jitter (ms) over the last window duration across all active UDP clients.",
	})

	udpJitterPercentileGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "udp_jitter_ms_percentile",
		Help: "Percentile jitter (ms) over the last window duration across all active UDP clients.",
	}, []string{"percentile"})

	udpIntervalAvgGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "udp_interval_ms_avg",
		Help: "Average packet interval (ms) over the last window duration across all active UDP clients.",
	})

	udpIntervalPercentileGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "udp_interval_ms_percentile",
		Help: "Percentile packet interval (ms) over the last window duration across all active UDP clients.",
	}, []string{"percentile"})

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
	// debug := flag.Bool("debug", false, "Enable debug logging")
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
		udpJitterAvgGauge,
		udpJitterPercentileGauge,
		udpIntervalAvgGauge,
		udpIntervalPercentileGauge,
		activeClientsGauge,
		mapSizeGauge,
		expiredClientsCounter,
		droppedClientsCounter,
	)

	clientWindows := sync.Map{}
	pendingClients := sync.Map{} // 防止短流创建

	// buffer capacity multiplier: base 1024 * times
	windowBufferCap := 1024 * (*windowBufferCapTimes)
	if windowBufferCap <= 0 {
		windowBufferCap = 1024
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
				udpJitterAvgGauge.Set(mean(jitterVals))
				udpJitterPercentileGauge.WithLabelValues(pLabel).Set(percentile(jitterVals, p))
			} else {
				udpJitterAvgGauge.Set(0)
				udpJitterPercentileGauge.WithLabelValues(pLabel).Set(0)
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

		// 防止短流
		_, exists := clientWindows.Load(client)
		if !exists {
			first, seen := pendingClients.LoadOrStore(client, meta.Timestamp)
			if !seen {
				return
			}
			if meta.Timestamp.Sub(first.(time.Time)) > 1*time.Second {
				pendingClients.Delete(client)
				return
			}
			pendingClients.Delete(client)
			if countSyncMap(&clientWindows) >= *maxClients {
				droppedClientsCounter.Inc()
				return
			}
			clientWindows.Store(client, newSlidingWindow(*window, windowBufferCap))
		}

		val, _ := clientWindows.Load(client)
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
