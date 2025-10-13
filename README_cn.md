# udp-quality-exporter

### Usage

```bash
# Debian-based systems
sudo apt-get install libpcap-dev
# RHEL-based systems
sudo dnf install libpcap-devel

# 编译
# with pcap
env GOOS=linux GOARCH=amd64 go build -tags pcap -o udp-quality-exporter
# without pcap
env GOOS=linux GOARCH=amd64 go build -o udp-quality-exporter

# 抓取 eth0 上 9000 端口 UDP，窗口 30s，Prometheus metrics 暴露在 2112 端口
sudo ./udp-quality-exporter --iface eth0 --window 30s --metrics :2112 --filter_ports 9000,9001

# 参数与默认值
sudo ./udp-quality-exporter --iface eth0 --window 30s --metrics :2112 --max_clients 100 --window_buffer_cap 1

```

##### 对于音视频通话业务，滑动窗口 window 长度建议如下：

- 常用推荐值：30秒 ~ 2分钟 都是合理的选择。
- 行业常见做法：
    - 30秒：能较快反映网络状态变化，适合实时监控和报警。
    - 60秒：更平滑，适合统计展示和趋势分析。
    - 2分钟及以上：适合做历史统计，但对实时性要求高的场景不建议太长。

具体建议
- 实时监控/报警：30秒~1分钟
- 统计展示/趋势分析：1~2分钟
- 如果通话流量很稀疏（比如静音时很少包），建议用 1 分钟或更长窗口。

你可以把窗口长度做成参数，实际业务中观察下效果，选择最适合你业务的窗口长度。


### Testing 

You can use Ncat for testing:

```bash
# Server：
nc -l -u -p 9000

# Client：
nc -u <server-ip> 9000

```

### Other

查看 MTU

```
ip link

```
