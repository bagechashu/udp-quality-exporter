<!-- @format -->

# UDP 网络质量测试模拟器

带有基础的实时可视化输出（HTTP / WebSocket）和流媒体行为模拟（音频流 + 丢包 + 乱序）。

功能:

- 多客户端 UDP 接入: 服务端可同时接收多个 UDP 客户端
- HTTP / WebSocket 输出: 在 `http://localhost:8080` 查看实时统计
- 丢包、乱序模拟: 客户端支持自定义丢包率和乱序概率
- 音频流行为模拟: 客户端每隔 20ms 发送 1 个 “音频帧” 包（模拟音频流)
- 实时统计: 服务端计算接收速率、丢包率、平均延迟、客户端数量

# 服务端

```bash
go run udp-tester/server/server.go

```

访问 [http://localhost:8080](http://localhost:8080) 可实时查看所有客户端的延迟与丢包。

# 客户端

参数:

- 指定服务端 (--server) (127.0.0.1:9000)
- MTU 设定 (--mtu) (default 1000)
- 丢包率（--loss）(0.0~1.0)
- 乱序概率（--reorder）(0.0~1.0)
- 模拟音频流（20ms 1 帧）

```bash
go run udp-tester/client/client.go --loss 0.1 --reorder 0.2

```
