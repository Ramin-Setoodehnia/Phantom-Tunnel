package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hashicorp/yamux"
	"nhooyr.io/websocket"
)

const (
	logFilePath       = "/tmp/phantom-tunnel.log"
	pidFilePath       = "/tmp/phantom.pid"
	successSignalPath = "/tmp/phantom_success.signal"
)

var bufferPool = &sync.Pool{
	New: func() any {
		return make([]byte, 32*1024)
	},
}

type TunnelStats struct {
	sync.Mutex
	ActiveConnections int
	TotalBytesIn      int64
	TotalBytesOut     int64
	Uptime            time.Time
	Connected         bool
}
var stats = &TunnelStats{Uptime: time.Now()}

type rateLimitedConn struct {
	net.Conn
	rate int // bytes per second
}

func (rlc *rateLimitedConn) Read(p []byte) (int, error) {
	max := rlc.rate
	if max <= 0 {
		max = len(p)
	}
	if len(p) > max {
		p = p[:max]
	}
	n, err := rlc.Conn.Read(p)
	if n > 0 && rlc.rate > 0 {
		time.Sleep(time.Duration(n) * time.Second / time.Duration(rlc.rate))
	}
	return n, err
}

func (rlc *rateLimitedConn) Write(p []byte) (int, error) {
	max := rlc.rate
	if max <= 0 {
		max = len(p)
	}
	if len(p) > max {
		p = p[:max]
	}
	n, err := rlc.Conn.Write(p)
	if n > 0 && rlc.rate > 0 {
		time.Sleep(time.Duration(n) * time.Second / time.Duration(rlc.rate))
	}
	return n, err
}

func main() {
	mode := flag.String("mode", "", "internal: 'server' or 'client'")
	rateLimit := flag.Int("ratelimit", 0, "Max bytes per second per conn (default: unlimited)")
	dashboardPort := flag.String("dashboard", "", "Dashboard port (default: 8080 server, 8081 client)")
	flag.Parse()
	if *mode != "" {
		configureLogging()
		args := flag.Args()
		dbPort := *dashboardPort
		if dbPort == "" {
			if *mode == "server" {
				dbPort = "8080"
			} else {
				dbPort = "8081"
			}
		}
		go startWebDashboard(":" + dbPort)
		if *mode == "server" {
			if len(args) < 5 {
				log.Fatal("Internal error: Not enough arguments for server mode.")
			}
			runServer(args[0], args[1], args[2], args[3], args[4], *rateLimit)
		} else if *mode == "client" {
			if len(args) < 2 {
				log.Fatal("Internal error: Not enough arguments for client mode.")
			}
			runClient(args[0], args[1], *rateLimit)
		}
		return
	}
	showInteractiveMenu()
}

func showInteractiveMenu() {
	fmt.Println("=======================================")
	fmt.Println("   👻 Phantom Tunnel v2.2 (Live Monitor) ")
	fmt.Println("   Make your traffic disappear.     ")
	fmt.Println("=======================================")
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Println("\nSelect an option:")
		fmt.Println("  1. Start Server Mode")
		fmt.Println("  2. Start Client Mode")
		fmt.Println("  3. Monitor Logs")
		fmt.Println("  4. Stop & Clean Tunnel")
		fmt.Println("  ------------------------")
		fmt.Println("  5. Uninstall")
		fmt.Println("  6. Exit")
		fmt.Print("Enter your choice [1-6]: ")
		choice, _ := reader.ReadString('\n')
		choice = strings.TrimSpace(choice)
		switch choice {
		case "1":
			setupServer(reader)
		case "2":
			setupClient(reader)
		case "3":
			monitorLogs()
		case "4":
			stopAndCleanTunnel(reader)
		case "5":
			uninstallSelf(reader)
		case "6":
			fmt.Println("Exiting.")
			os.Exit(0)
		default:
			fmt.Println("Invalid choice. Please try again.")
		}
	}
}

func setupServer(reader *bufio.Reader) {
	if isTunnelRunning() {
		fmt.Println("A tunnel is already running. Stop it first with option '4'.")
		return
	}
	fmt.Println("\n--- 👻 Server Setup ---")
	listenAddr := promptForInput(reader, "Enter Tunnel Port", "443")
	publicAddr := promptForInput(reader, "Enter Public Port", "8000")
	path := promptForInput(reader, "Enter Secret URL Path", "/"+generateRandomPath())
	rateLimitStr := promptForInput(reader, "Enter Rate-Limit (KB/s, 0 for unlimited)", "0")
	rateLimit, _ := strconv.Atoi(rateLimitStr)
	rateLimit = rateLimit * 1024
	dashboardPort := promptForInput(reader, "Enter Dashboard Port", "8080")
	if !strings.HasPrefix(listenAddr, ":") {
		listenAddr = ":" + listenAddr
	}
	if !strings.HasPrefix(publicAddr, ":") {
		publicAddr = ":" + publicAddr
	}
	if _, err := os.Stat("server.crt"); os.IsNotExist(err) {
		fmt.Println("SSL certificate not found. Generating a new one...")
		if err := generateSelfSignedCert(); err != nil {
			log.Fatalf("Failed to generate SSL: %v", err)
		}
		fmt.Println("✅ SSL certificate 'server.crt' and 'server.key' generated.")
	} else {
		fmt.Println("✅ Existing SSL certificate found.")
	}

	// ==========================================================
	// FIX: Flags are now placed BEFORE positional arguments.
	// ==========================================================
	cmd := exec.Command(os.Args[0],
		"--mode", "server",
		"--ratelimit", strconv.Itoa(rateLimit),
		"--dashboard", dashboardPort,
		// Positional arguments now come after all flags
		listenAddr, publicAddr, path, "server.crt", "server.key")

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		fmt.Printf("Error starting server process: %v\n", err)
		return
	}
	pid := cmd.Process.Pid
	_ = os.WriteFile(pidFilePath, []byte(strconv.Itoa(pid)), 0644)
	fmt.Printf("\n✅ Server process started in the background (PID: %d).\n", pid)
	fmt.Printf("Dashboard: http://localhost:%s/\n", dashboardPort)
}

// در این فایل، این تابع را پیدا کرده و با کد زیر جایگزین کنید

func setupClient(reader *bufio.Reader) {
	if isTunnelRunning() {
		fmt.Println("A tunnel is already running. Stop it first with option '4'.")
		return
	}
	fmt.Println("\n--- 👻 Client Setup ---")
	serverIP := promptForInput(reader, "Enter Server IP or Hostname", "")
	if serverIP == "" {
		fmt.Println("Error: Server IP cannot be empty.")
		return
	}
	serverPort := promptForInput(reader, "Enter Server Tunnel Port", "443")
	serverPath := promptForInput(reader, "Enter Server Secret Path", "/connect")
	localAddr := promptForInput(reader, "Enter Local Service Address", "localhost:3000")
	rateLimitStr := promptForInput(reader, "Enter Rate-Limit (KB/s, 0 for unlimited)", "0")
	rateLimit, _ := strconv.Atoi(rateLimitStr)
	rateLimit = rateLimit * 1024
	dashboardPort := promptForInput(reader, "Enter Dashboard Port", "8081")
	serverURL := fmt.Sprintf("wss://%s:%s%s", serverIP, serverPort, serverPath)

	// ==========================================================
	// FIX: Flags are now placed BEFORE positional arguments.
	// ==========================================================
	cmd := exec.Command(os.Args[0],
		"--mode", "client",
		"--ratelimit", strconv.Itoa(rateLimit),
		"--dashboard", dashboardPort,
		// Positional arguments now come after all flags
		serverURL, localAddr)

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		fmt.Printf("Error starting client process: %v\n", err)
		return
	}
	pid := cmd.Process.Pid
	_ = os.WriteFile(pidFilePath, []byte(strconv.Itoa(pid)), 0644)
	fmt.Printf("\nClient process started (PID: %d). Waiting for connection confirmation...\n", pid)
	timeout := time.After(20 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-timeout:
			fmt.Println("❌ Could not confirm initial connection within 20 seconds.")
			return
		case <-ticker.C:
			if _, err := os.Stat(successSignalPath); err == nil {
				os.Remove(successSignalPath)
				fmt.Println("✅ Tunnel connection established successfully! Running in the background.")
				fmt.Printf("Dashboard: http://localhost:%s/\n", dashboardPort)
				return
			}
		}
	}
}

func pipeCount(dst io.Writer, src io.Reader, counter *int64, limit int) {
	buf := bufferPool.Get().([]byte)
	defer bufferPool.Put(buf)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if counter != nil {
				stats.Lock()
				*counter += int64(n)
				stats.Unlock()
			}
			written := 0
			for written < n {
				x := n - written
				if limit > 0 && x > limit {
					x = limit
				}
				m, writeErr := dst.Write(buf[written : written+x])
				written += m
				if limit > 0 && m > 0 {
					time.Sleep(time.Second * time.Duration(m) / time.Duration(limit))
				}
				if writeErr != nil {
					return
				}
			}
		}
		if err != nil {
			break
		}
	}
}

func runServer(listenAddr, publicAddr, path, certFile, keyFile string, ratelimit int) {
	log.Println("[Server Mode] 🚀 Starting process...")
	stats.Lock()
	stats.Connected = false
	stats.Unlock()
	var session *yamux.Session
	httpServer := &http.Server{
		Addr: listenAddr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != path {
				http.NotFound(w, r)
				return
			}
			wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"tunnel"}})
			if err != nil {
				log.Printf("[Server] Websocket accept failed: %v", err)
				return
			}
			conn := websocket.NetConn(context.Background(), wsConn, websocket.MessageBinary)
			log.Println("[Server] 🤝 WebSocket tunnel established!")
			yamuxConfig := yamux.DefaultConfig()
			yamuxConfig.KeepAliveInterval = 10 * time.Second
			yamuxConfig.ConnectionWriteTimeout = 10 * time.Second
			yamuxConfig.MaxStreamWindowSize = 256 * 1024
			session, _ = yamux.Server(conn, yamuxConfig)
			stats.Lock()
			stats.Connected = true
			stats.Unlock()
			go func() {
				<-session.CloseChan()
				stats.Lock()
				stats.Connected = false
				stats.Unlock()
			}()
		}),
	}
	go func() {
		log.Printf("[Server] ✅ Listening for tunnel on wss://%s", listenAddr)
		if err := httpServer.ListenAndServeTLS(certFile, keyFile); err != nil {
			log.Fatalf("[Server] HTTPS server failed: %v", err)
		}
	}()
	publicListener, err := net.Listen("tcp", publicAddr)
	if err != nil {
		log.Fatalf("[Server] Public listener failed on %s: %v", publicAddr, err)
	}
	log.Printf("[Server] ✅ Listening for public traffic on %s", publicAddr)
	for {
		publicConn, err := publicListener.Accept()
		if err != nil {
			continue
		}
		stats.Lock()
		stats.ActiveConnections++
		stats.Unlock()
		go func() {
			defer func() {
				publicConn.Close()
				stats.Lock()
				stats.ActiveConnections--
				stats.Unlock()
			}()
			c := publicConn
			if ratelimit > 0 {
				c = &rateLimitedConn{Conn: publicConn, rate: ratelimit}
			}
			if session == nil || session.IsClosed() {
				return
			}
			stream, err := session.OpenStream()
			if err != nil {
				return
			}
			defer stream.Close()
			go pipeCount(stream, c, &stats.TotalBytesIn, ratelimit)
			pipeCount(c, stream, &stats.TotalBytesOut, ratelimit)
		}()
	}
}

func runClient(serverURL, localAddr string, ratelimit int) {
	for {
		log.Printf("[Client] ... Attempting connection to %s", serverURL)
		stats.Lock()
		stats.Connected = false
		stats.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		wsConn, _, err := websocket.Dial(ctx, serverURL, &websocket.DialOptions{
			Subprotocols: []string{"tunnel"},
			HTTPClient:   &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}},
		})
		cancel()
		if err != nil {
			log.Printf("[Client] ❌ Connection failed: %v. Retrying...", err)
			time.Sleep(5 * time.Second)
			continue
		}
		log.Println("[Client] ✅ Tunnel connection established!")
		stats.Lock()
		stats.Connected = true
		stats.Unlock()
		if f, err := os.Create(successSignalPath); err == nil {
			f.Close()
		}
		yamuxConfig := yamux.DefaultConfig()
		yamuxConfig.KeepAliveInterval = 10 * time.Second
		yamuxConfig.ConnectionWriteTimeout = 10 * time.Second
		yamuxConfig.MaxStreamWindowSize = 256 * 1024
		session, err := yamux.Client(websocket.NetConn(context.Background(), wsConn, websocket.MessageBinary), yamuxConfig)
		if err != nil {
			log.Printf("[Client] ❌ Multiplexing failed: %v", err)
			stats.Lock()
			stats.Connected = false
			stats.Unlock()
			continue
		}
		go func() {
			<-session.CloseChan()
			stats.Lock()
			stats.Connected = false
			stats.Unlock()
		}()
		for {
			stream, err := session.AcceptStream()
			if err != nil {
				log.Printf("[Client] ... Session terminated: %v. Reconnecting...", err)
				break
			}
			stats.Lock()
			stats.ActiveConnections++
			stats.Unlock()
			go func() {
				defer func() {
					stream.Close()
					stats.Lock()
					stats.ActiveConnections--
					stats.Unlock()
				}()
				localConn, err := net.Dial("tcp", localAddr)
				if err != nil {
					return
				}
				c := localConn
				if ratelimit > 0 {
					c = &rateLimitedConn{Conn: localConn, rate: ratelimit}
				}
				defer localConn.Close()
				go pipeCount(c, stream, &stats.TotalBytesIn, ratelimit)
				pipeCount(stream, c, &stats.TotalBytesOut, ratelimit)
			}()
		}
	}
}

func startWebDashboard(port string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		stats.Lock()
		defer stats.Unlock()
		info := struct {
			ActiveConnections int    `json:"active_connections"`
			TotalBytesIn      int64  `json:"total_bytes_in"`
			TotalBytesOut     int64  `json:"total_bytes_out"`
			Uptime            string `json:"uptime"`
			Connected         bool   `json:"connected"`
		}{
			ActiveConnections: stats.ActiveConnections,
			TotalBytesIn:      stats.TotalBytesIn,
			TotalBytesOut:     stats.TotalBytesOut,
			Uptime:            time.Since(stats.Uptime).String(),
			Connected:         stats.Connected,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(info)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Phantom Tunnel Dashboard</title>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <style>
    body {
      background: #f4f6fb;
      color: #222;
      font-family: 'Vazirmatn', 'Segoe UI', Arial, sans-serif;
      margin: 0; padding: 0;
    }
    .container {
      max-width: 520px;
      margin: 32px auto;
      padding: 24px 20px 10px 20px;
      background: #fff;
      border-radius: 22px;
      box-shadow: 0 4px 24px #0001;
      display: flex;
      flex-direction: column;
      gap: 24px;
    }
    .title {
      text-align: center;
      font-size: 1.5rem;
      font-weight: 700;
      letter-spacing: 1px;
      margin-bottom: 8px;
    }
    .status-row {
      display: flex;
      justify-content: center;
      align-items: center;
      gap: 10px;
      margin-bottom: 6px;
    }
    .dot {
      width: 18px;
      height: 18px;
      border-radius: 50%;
      margin-right: 8px;
      box-shadow: 0 1px 8px #b1c6f41a;
      border: 2px solid #fff;
      display: inline-block;
      vertical-align: middle;
      transition: background 0.2s;
    }
    .status-label {
      font-weight: 600;
      font-size: 1.13rem;
      letter-spacing: 1px;
      color: #666;
      vertical-align: middle;
    }
    .row {
      display: flex;
      flex-direction: row;
      justify-content: space-between;
      gap: 14px;
    }
    .card {
      background: #f4f8ff;
      border-radius: 16px;
      flex: 1 1 0;
      text-align: center;
      padding: 18px 0 10px 0;
      min-width: 0;
      box-shadow: 0 1px 8px #b1c6f41a;
      display: flex;
      flex-direction: column;
      align-items: center;
      font-weight: 600;
      font-size: 1.07rem;
      transition: box-shadow 0.2s;
    }
    .card span.value {
      margin-top: 7px;
      font-size: 1.32rem;
      font-weight: 800;
      color: #387df6;
      background: #e7f1ff;
      border-radius: 10px;
      padding: 3px 12px;
      min-width: 50px;
      display: inline-block;
    }
    .uptime-card {
      background: #f6faf6;
      border-radius: 16px;
      text-align: center;
      font-size: 1.05rem;
      font-weight: 500;
      padding: 15px 0 8px 0;
      color: #2b6b2e;
      box-shadow: 0 1px 8px #b1f4c61a;
      margin-bottom: 4px;
    }
    .chart-container {
      background: #f6f8fa;
      border-radius: 16px;
      padding: 15px 10px 18px 10px;
      min-height: 180px;
      box-shadow: 0 1px 6px #b1c6f41a;
      display: flex;
      flex-direction: column;
      align-items: center;
    }
    .footer {
      text-align: center;
      color: #9daabb;
      font-size: 0.95rem;
      margin: 16px 0 0 0;
      padding-bottom: 10px;
    }
    @media (max-width: 650px) {
      .container { padding: 8px 3vw; }
      .row { flex-direction: column; gap: 8px;}
      .card { padding: 10px 0 7px 0; }
      .chart-container { min-height: 120px; }
    }
  </style>
</head>
<body>
  <div class="container">
    <div class="title">👻 Phantom Tunnel Dashboard</div>
    <div class="status-row">
      <span id="status-dot" class="dot" style="background:#f6c7c7"></span>
      <span id="status-label" class="status-label">Connecting...</span>
    </div>
    <div class="row">
      <div class="card">
        Active
        <span class="value" id="active">0</span>
      </div>
      <div class="card">
        Total In
        <span class="value" id="in">0 B</span>
      </div>
      <div class="card">
        Total Out
        <span class="value" id="out">0 B</span>
      </div>
    </div>
    <div class="uptime-card">
      <span>Uptime: <b id="uptime">0s</b></span>
    </div>
    <div class="chart-container">
      <canvas id="trafficChart" height="90"></canvas>
    </div>
    <div class="footer">
      &copy; 2025 Phantom Tunnel &mdash; webwizards-team
    </div>
  </div>
  <script src="https://cdn.jsdelivr.net/npm/chart.js@4.4.1/dist/chart.umd.min.js"></script>
  <script>
    let lastIn = 0, lastOut = 0;
    let trafficData = [];
    let labels = [];
    const MAX_POINTS = 60;

    const ctx = document.getElementById('trafficChart').getContext('2d');
    const chart = new Chart(ctx, {
      type: 'line',
      data: {
        labels: labels,
        datasets: [
          {
            label: "In (KB/s)",
            data: [],
            borderColor: "#387df6",
            backgroundColor: "rgba(56,125,246,0.09)",
            borderWidth: 2,
            cubicInterpolationMode: 'monotone',
            tension: 0.4,
            pointRadius: 0,
            fill: true,
          },
          {
            label: "Out (KB/s)",
            data: [],
            borderColor: "#2bc48a",
            backgroundColor: "rgba(43,196,138,0.09)",
            borderWidth: 2,
            cubicInterpolationMode: 'monotone',
            tension: 0.4,
            pointRadius: 0,
            fill: true,
          }
        ]
      },
      options: {
        responsive: true,
        scales: {
          x: { display: false },
          y: {
            beginAtZero: true,
            ticks: { color: "#7d93b2" },
            grid: { color: "#e3e8ef" }
          }
        },
        plugins: {
          legend: { labels: { color: "#49597a", font: { size: 13 } } }
        }
      }
    });

    function formatBytes(bytes) {
      if (bytes < 1024) return bytes + " B";
      let k = 1024, sizes = ["KB", "MB", "GB", "TB"], i = -1;
      do { bytes = bytes / k; i++; } while (bytes >= k && i < sizes.length - 1);
      return bytes.toFixed(2) + " " + sizes[i];
    }

    function updateStats() {
      fetch('/stats').then(res => res.json()).then(stat => {
        document.getElementById('active').innerText = stat.active_connections;
        document.getElementById('in').innerText = formatBytes(stat.total_bytes_in);
        document.getElementById('out').innerText = formatBytes(stat.total_bytes_out);
        document.getElementById('uptime').innerText = stat.uptime;

        // Connection status
        let dot = document.getElementById('status-dot');
        let label = document.getElementById('status-label');
        if (stat.connected) {
          dot.style.background = "#40dd7a";
          label.innerText = "Connected";
          label.style.color = "#269d5b";
        } else {
          dot.style.background = "#f24c4c";
          label.innerText = "Disconnected";
          label.style.color = "#b52121";
        }

        // Traffic Chart
        let nowIn = stat.total_bytes_in;
        let nowOut = stat.total_bytes_out;
        let inDiff = Math.max(0, (nowIn - lastIn) / 1024);
        let outDiff = Math.max(0, (nowOut - lastOut) / 1024);
        lastIn = nowIn; lastOut = nowOut;
        if (trafficData.length >= MAX_POINTS) {
          trafficData.shift();
          labels.shift();
        }
        trafficData.push({in: inDiff, out: outDiff});
        labels.push('');
        chart.data.labels = labels;
        chart.data.datasets[0].data = trafficData.map(val => val.in);
        chart.data.datasets[1].data = trafficData.map(val => val.out);
        chart.update();
      }).catch(()=>{
        // آفلاین یا unreachable
        let dot = document.getElementById('status-dot');
        let label = document.getElementById('status-label');
        dot.style.background = "#aaaaaa";
        label.innerText = "Connecting...";
        label.style.color = "#888";
      });
    }
    setInterval(updateStats, 1000); updateStats();
  </script>
</body>
</html>
		`))
	})
	log.Printf("[Dashboard] Running at http://localhost%s/", port)
	http.ListenAndServe(port, mux)
}

func stopAndCleanTunnel(reader *bufio.Reader) {
	fmt.Println("\nThis will stop any running tunnel AND delete all generated files.")
	fmt.Print("Are you sure? [y/N]: ")
	confirm, _ := reader.ReadString('\n')
	if strings.TrimSpace(strings.ToLower(confirm)) != "y" {
		fmt.Println("Operation cancelled.")
		return
	}
	if pidBytes, err := os.ReadFile(pidFilePath); err == nil {
		pid, _ := strconv.Atoi(string(pidBytes))
		if process, err := os.FindProcess(pid); err == nil {
			fmt.Printf("Stopping tunnel process (PID: %d)...\n", pid)
			if err := process.Signal(syscall.SIGTERM); err == nil {
				fmt.Println("  - Process stopped successfully.")
			}
		}
	} else {
		fmt.Println("No running process found, proceeding with file cleanup.")
	}
	fmt.Println("Cleaning up generated files...")
	deleteFile("server.crt")
	deleteFile("server.key")
	deleteFile(logFilePath)
	deleteFile(pidFilePath)
	deleteFile(successSignalPath)
	fmt.Println("✅ Cleanup complete.")
}
func uninstallSelf(reader *bufio.Reader) {
	if isTunnelRunning() {
		fmt.Println("A tunnel is running. Stop and clean it first.")
		return
	}
	fmt.Println("\nWARNING: This will permanently remove the 'phantom-tunnel' command.")
	fmt.Print("Are you sure? [y/N]: ")
	if confirm, _ := reader.ReadString('\n'); strings.TrimSpace(strings.ToLower(confirm)) != "y" {
		fmt.Println("Uninstall cancelled.")
		return
	}
	executablePath, err := os.Executable()
	if err != nil {
		fmt.Println("Error: Could not determine executable path:", err)
		return
	}
	deleteFile(pidFilePath)
	deleteFile(logFilePath)
	deleteFile(successSignalPath)
	fmt.Printf("Removing executable: %s\n", executablePath)
	if err = os.Remove(executablePath); err != nil {
		fmt.Printf("Error: Failed to remove executable: %v\n", err)
		return
	}
	fmt.Println("✅ Phantom Tunnel has been successfully uninstalled.")
	os.Exit(0)
}
func isTunnelRunning() bool {
	pidBytes, err := os.ReadFile(pidFilePath)
	if err != nil {
		return false
	}
	pid, _ := strconv.Atoi(string(pidBytes))
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}
func monitorLogs() {
	if !isTunnelRunning() && func() bool {
		_, err := os.Stat(logFilePath)
		return os.IsNotExist(err)
	}() {
		fmt.Println("No tunnel process is running and no log file found.")
		return
	}
	if !isTunnelRunning() {
		fmt.Println("No tunnel process is running. Displaying logs from the last run...")
	}
	fmt.Println("\n--- 🔎 Real-time Log Monitoring ---")
	fmt.Println("... Press Ctrl+C to stop monitoring and return to the menu.")
	cmd := exec.Command("tail", "-f", logFilePath)
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-Command", "Get-Content", "-Path", logFilePath, "-Wait")
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
	fmt.Println("\n... Stopped monitoring.")
}
func configureLogging() {
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil {
		log.Fatalf("Failed to open log file: %v", err)
	}
	log.SetOutput(logFile)
}
func promptForInput(reader *bufio.Reader, promptText, defaultValue string) string {
	fmt.Printf("%s [%s]: ", promptText, defaultValue)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return defaultValue
	}
	return input
}
func deleteFile(filePath string) {
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		fmt.Printf("  - Error deleting %s: %v\n", filePath, err)
	} else if err == nil {
		fmt.Printf("  - Deleted: %s\n", filePath)
	}
}
func generateSelfSignedCert() error {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{Organization: []string{"Phantom Tunnel"}},
		NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour * 24 * 3650),
		KeyUsage: x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return err
	}
	certOut, err := os.Create("server.crt")
	if err != nil {
		return err
	}
	defer certOut.Close()
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyOut, err := os.Create("server.key")
	if err != nil {
		return err
	}
	defer keyOut.Close()
	pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	return nil
}
func generateRandomPath() string {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return "secret-path"
	}
	return hex.EncodeToString(bytes)
}
