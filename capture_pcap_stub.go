//go:build !pcap
// +build !pcap

package main

import (
	"fmt"

	"github.com/google/gopacket"
)

// captureWithPcap 的 stub 版本：未编译 pcap 支持时被使用
// 你可以返回错误，让调用方在启动时选择 afpacket 或退出。
func captureWithPcap(iface, filter string, handlePacket func(gopacket.Packet)) error {
	return fmt.Errorf("pcap support not compiled in; build with `-tags pcap` to enable pcap. (current build omitted pcap)")
}
