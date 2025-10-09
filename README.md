# udp-quality-exporter

```
env GOOS=linux GOARCH=amd64 go build -o udp-quality-exporter

# 抓取 eth0 上 9000 端口 UDP，窗口 30s，Prometheus metrics 暴露在 2112 端口
sudo ./udp-quality-exporter --iface eth0 --window 30s --metrics :2112 --filter "udp and port 9000"

```

# 依赖

```
# Debian
sudo apt-get install libpcap-dev

# RHCE
sudo dnf install libpcap-devel

```

# 测试

```
服务端：nc -l -u -p 9000
客户端：nc -u 192.168.1.12 9000

```
