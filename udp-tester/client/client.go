package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"time"
)

func main() {
	server := flag.String("server", "127.0.0.1:9000", "server address")
	mtu := flag.Int("mtu", 1000, "maximum transmission unit")
	lossRate := flag.Float64("loss", 0.0, "packet loss rate (0.0~1.0)")
	reorderRate := flag.Float64("reorder", 0.0, "packet reorder rate (0.0~1.0)")
	flag.Parse()

	conn, err := net.Dial("udp", *server)
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	fmt.Printf("🎯 Sending packets to %s (loss=%.2f reorder=%.2f)\n", *server, *lossRate, *reorderRate)

	var seq uint32
	sendBuf := make([][]byte, 0, 10)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		seq++
		packet := make([]byte, *mtu)
		binary.BigEndian.PutUint32(packet[0:4], seq)
		binary.BigEndian.PutUint64(packet[4:12], uint64(time.Now().UnixNano()))
		copy(packet[12:], []byte("FAKE_AUDIO_FRAME"))

		// 模拟丢包
		if rand.Float64() < *lossRate {
			continue
		}

		// 模拟乱序
		if rand.Float64() < *reorderRate {
			sendBuf = append(sendBuf, packet)
			if len(sendBuf) >= 3 {
				rand.Shuffle(len(sendBuf), func(i, j int) {
					sendBuf[i], sendBuf[j] = sendBuf[j], sendBuf[i]
				})
				for _, p := range sendBuf {
					conn.Write(p)
				}
				sendBuf = sendBuf[:0]
			}
			continue
		}

		conn.Write(packet)
	}
}
