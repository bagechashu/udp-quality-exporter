package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type ClientStats struct {
	Addr       string
	LastSeq    uint32
	Received   uint32
	Lost       uint32
	TotalDelay time.Duration
	LastTime   time.Time
}

var (
	stats     sync.Map // key=addr string -> *ClientStats
	upgrader  = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	wsClients = make(map[*websocket.Conn]bool)
	wsLock    sync.Mutex
)

func main() {
	packageSize := flag.Int("package_size", 2000, "maximum transmission unit")
	udpserver := flag.String("udpserver", ":9000", "listen address")
	httpserver := flag.String("httpserver", ":8080", "http listen address")
	flag.Parse()

	go startUDP(*udpserver, *packageSize)
	startHTTP(*httpserver)
}

func startUDP(udpserver string, packageSize int) {
	addr, _ := net.ResolveUDPAddr("udp", udpserver)
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	fmt.Printf("✅ UDP listening on %s\n", udpserver)

	buf := make([]byte, packageSize)
	for {
		n, client, err := conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		fmt.Printf("Debug: Received UDP from %s, length=%d\n", client.String(), n)
		go handlePacket(client, buf[:n])
	}
}

func handlePacket(client *net.UDPAddr, data []byte) {
	if len(data) < 12 {
		return
	}

	seq := binary.BigEndian.Uint32(data[:4])
	ts := int64(binary.BigEndian.Uint64(data[4:12]))
	delay := time.Since(time.Unix(0, ts))

	val, _ := stats.LoadOrStore(client.String(), &ClientStats{Addr: client.String()})
	st := val.(*ClientStats)

	st.Received++
	if seq > st.LastSeq+1 {
		st.Lost += seq - st.LastSeq - 1
	}
	st.LastSeq = seq
	st.TotalDelay += delay
	st.LastTime = time.Now()

	if st.Received%50 == 0 {
		broadcastStats()
	}
}

func startHTTP(httpserver string) {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, indexHTML)
	})

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		wsLock.Lock()
		wsClients[conn] = true
		wsLock.Unlock()
	})

	fmt.Printf("🌐 HTTP/WebSocket running on %s\n", httpserver)
	http.ListenAndServe(httpserver, nil)
}

func broadcastStats() {
	all := []ClientStats{}
	stats.Range(func(_, v any) bool {
		st := v.(*ClientStats)
		all = append(all, *st)
		return true
	})

	msg, _ := json.Marshal(all)

	wsLock.Lock()
	defer wsLock.Unlock()
	for conn := range wsClients {
		err := conn.WriteMessage(websocket.TextMessage, msg)
		if err != nil {
			conn.Close()
			delete(wsClients, conn)
		}
	}
}

const indexHTML = `
<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>UDP Stream Monitor</title>
<style>
body { font-family: sans-serif; background:#111; color:#0f0; padding:1em; }
table { border-collapse:collapse; width:100%; }
td,th { border:1px solid #333; padding:4px; text-align:left; }
</style>
</head>
<body>
<h2>UDP Client Monitor</h2>
<table id="tbl">
<tr><th>Client</th><th>Recv</th><th>Lost</th><th>Loss %</th><th>Avg Delay</th><th>Last Seen</th></tr>
</table>
<script>
let ws = new WebSocket("ws://" + location.host + "/ws");
ws.onmessage = (e)=>{
  let data = JSON.parse(e.data);
  let t = document.getElementById("tbl");
  t.innerHTML='<tr><th>Client</th><th>Recv</th><th>Lost</th><th>Loss %</th><th>Avg Delay</th><th>Last Seen</th></tr>';
  data.forEach(c=>{
    let loss = (c.Lost/(c.Received+c.Lost)*100).toFixed(2);
    let delay = (c.TotalDelay/c.Received/1e6).toFixed(2)+'ms';
    t.innerHTML += '<tr><td>'+c.Addr+'</td><td>'+c.Received+'</td><td>'+c.Lost+'</td><td>'+loss+'</td><td>'+delay+'</td><td>'+c.LastTime+'</td></tr>';
  })
}
</script>
</body>
</html>`
