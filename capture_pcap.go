//go:build pcap
// +build pcap

package main

import (
	"fmt"
	"log"

	"github.com/google/gopacket"
	"github.com/google/gopacket/pcap"
)

// captureWithPcap 使用 libpcap 实现，只有在带上 -tags pcap 时才会编译进来
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
