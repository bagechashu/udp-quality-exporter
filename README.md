# udp-quality-exporter

### Usage

```bash
# Debian-based systems
sudo apt-get install libpcap-dev
# RHEL-based systems
sudo dnf install libpcap-devel

# compilation
# with pcap
env GOOS=linux GOARCH=amd64 go build -tags pcap -o udp-quality-exporter
# without pcap
env GOOS=linux GOARCH=amd64 go build -o udp-quality-exporter

# Capture UDP packets on eth0 port 9000, 30s sliding window, Prometheus metrics exposed on port 2112
sudo ./udp-quality-exporter --iface eth0 --window 30s --metrics :2112 --filter_ports 9000,9001

# Default values
sudo ./udp-quality-exporter --iface eth0 --window 30s --metrics :2112 --max_clients 100 --window_buffer_cap 1 --percentile 90 --mode pcap --debug

```

### Testing 

- Use Ncat for testing:

```bash
# Server：
nc -l -u -p 9000

# Client：
nc -u <server-ip> 9000

# metrics
curl http://localhost:2112/metrics

```

### Pprof

```
go tool pprof http://127.0.0.1:6060/debug/pprof/heap

```
