package main

import (
	"fmt"
	"log"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/afpacket"
	"github.com/google/gopacket/layers"
)

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
