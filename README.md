# udp-quality-exporter

### Usage

```bash
env GOOS=linux GOARCH=amd64 go build -o udp-quality-exporter

# Capture UDP packets on eth0 port 9000, 30s sliding window, Prometheus metrics exposed on port 2112
sudo ./udp-quality-exporter --iface eth0 --window 30s --metrics :2112 --filter_port 9000

# Default values
sudo ./udp-quality-exporter --iface eth0 --window 30s --metrics :2112 --max_clients 100 --window_buffer_cap 1

```

##### Sliding Window Recommendations

For audio/video call scenarios, the recommended sliding window (--window) duration is:

- Common values: 30 seconds to 2 minutes are reasonable choices.
- Industry practices:
    - 30 seconds: Quickly reflects network state changes, suitable for real-time monitoring and alerting.
    - 60 seconds: Smoother, suitable for statistics and trend analysis.
    - 2 minutes or more: Good for historical statistics, but not recommended for scenarios requiring high real-time sensitivity.

Suggestions:

- Real-time monitoring/alerting: 30 seconds to 1 minute
- Statistics/trend analysis: 1 to 2 minutes
- For sparse traffic (e.g., silence periods), use 1 minute or longer.

You can adjust the window length via the parameter and choose the best value for your business scenario.


### System Dependencies

```bash
# Debian-based systems
sudo apt-get install libpcap-dev

# RHEL-based systems
sudo dnf install libpcap-devel

```

### Testing 

You can use Ncat for testing:

```bash
# Server：
nc -l -u -p 9000

# Client：
nc -u <server-ip> 9000

```
