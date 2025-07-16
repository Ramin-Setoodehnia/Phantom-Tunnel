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

type activeSession struct {
	sync.RWMutex
	session *yamux.Session
}

func (as *activeSession) Get() *yamux.Session {
	as.RLock()
	defer as.RUnlock()
	return as.session
}

func (as *activeSession) Set(session *yamux.Session) {
	as.Lock()
	defer as.Unlock()
	if as.session != nil && !as.session.IsClosed() {
		as.session.Close()
	}
	as.session = session
}

type rateLimitedConn struct {
	net.Conn
	rate int
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
	tunnelType := flag.String("tunnel-type", "wss", "Tunnel protocol: 'wss' or 'tcpmux'")
	authToken := flag.String("token", "", "Authentication token for the tunnel")
	fragSize := flag.Int("frag-size", 0, "Fragmentation size in bytes")
	fragDelay := flag.Int("frag-delay", 0, "Fragmentation delay in milliseconds")
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
			runServer(args[0], args[1], args[2], args[3], args[4], *rateLimit, *tunnelType, *authToken, *fragSize, *fragDelay)
		} else if *mode == "client" {
			if len(args) < 2 {
				log.Fatal("Internal error: Not enough arguments for client mode.")
			}
			runClient(args[0], args[1], *rateLimit, *tunnelType, *authToken, *fragSize, *fragDelay)
		}
		return
	}
	showInteractiveMenu()
}

func showInteractiveMenu() {
	fmt.Println("=======================================")
	fmt.Println(" 👻 Phantom Tunnel v2.3 (TcpMux+Fragment-Port)   ")
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
	
	fmt.Println("Select Tunnel Type:")
	fmt.Println("  1. WSS (Encrypted, Resembles HTTPS)")
	fmt.Println("  2. TCP Mux (Raw TCP, Low Latency) - Recommended")
	tunnelChoice := promptForInput(reader, "Enter your choice [1-2]", "2")
	tunnelType := "wss"
	if tunnelChoice == "2" {
		tunnelType = "tcpmux"
	}

	listenAddr := promptForInput(reader, "Enter Tunnel Port", "443")
	
	var publicPorts []string
	for i := 0; ; i++ {
		prompt := fmt.Sprintf("Enter Public Port %d (e.g., 8000) or leave blank to finish", i+1)
		port := promptForInput(reader, prompt, "")
		if port == "" {
			if len(publicPorts) == 0 {
				fmt.Println("Error: At least one public port is required.")
				return
			}
			break
		}
		publicPorts = append(publicPorts, port)
	}
	publicAddrs := strings.Join(publicPorts, ",")

	authToken := promptForInput(reader, "Enter a Secret Token (like a password)", generateRandomPath())

	fragInput := promptForInput(reader, "Fragmentation? 'size:delay_ms' (empty to disable)", "")
	fragSize, fragDelay := parseFragmentation(fragInput)

	path := "/"
	if tunnelType == "wss" {
		path = promptForInput(reader, "Enter Secret URL Path", "/"+generateRandomPath())
		if _, err := os.Stat("server.crt"); os.IsNotExist(err) {
			fmt.Println("SSL certificate not found. Generating a new one...")
			if err := generateSelfSignedCert(); err != nil {
				log.Fatalf("Failed to generate SSL: %v", err)
			}
			fmt.Println("✅ SSL certificate 'server.crt' and 'server.key' generated.")
		} else {
			fmt.Println("✅ Existing SSL certificate found.")
		}
	}

	rateLimitStr := promptForInput(reader, "Enter Rate-Limit (KB/s, 0 for unlimited)", "0")
	rateLimit, _ := strconv.Atoi(rateLimitStr)
	rateLimit = rateLimit * 1024
	dashboardPort := promptForInput(reader, "Enter Dashboard Port", "8080")
	if !strings.HasPrefix(listenAddr, ":") {
		listenAddr = ":" + listenAddr
	}

	cmd := exec.Command(os.Args[0],
		"--mode", "server",
		"--ratelimit", strconv.Itoa(rateLimit),
		"--dashboard", dashboardPort,
		"--tunnel-type", tunnelType,
		"--token", authToken,
		"--frag-size", strconv.Itoa(fragSize),
		"--frag-delay", strconv.Itoa(fragDelay),
		listenAddr, publicAddrs, path, "server.crt", "server.key")

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

func setupClient(reader *bufio.Reader) {
	if isTunnelRunning() {
		fmt.Println("A tunnel is already running. Stop it first with option '4'.")
		return
	}
	fmt.Println("\n--- 👻 Client Setup ---")

	fmt.Println("Select Tunnel Type:")
	fmt.Println("  1. WSS (Encrypted)")
	fmt.Println("  2. TCP Mux (Raw TCP, Low Latency)")
	tunnelChoice := promptForInput(reader, "Enter your choice [1-2]", "2")
	tunnelType := "wss"
	if tunnelChoice == "2" {
		tunnelType = "tcpmux"
	}

	serverIP := promptForInput(reader, "Enter Server IP or Hostname", "")
	if serverIP == "" {
		fmt.Println("Error: Server IP cannot be empty.")
		return
	}
	serverPort := promptForInput(reader, "Enter Server Tunnel Port", "443")
	authToken := promptForInput(reader, "Enter the Server's Secret Token", "")

	var localAddrsList []string
	for i := 0; ; i++ {
		prompt := fmt.Sprintf("Enter Local Service Address %d (e.g., localhost:3000) or leave blank to finish", i+1)
		addr := promptForInput(reader, prompt, "")
		if addr == "" {
			if len(localAddrsList) == 0 {
				fmt.Println("Error: At least one local service address is required.")
				return
			}
			break
		}
		localAddrsList = append(localAddrsList, addr)
	}
	localAddrs := strings.Join(localAddrsList, ",")

	fragInput := promptForInput(reader, "Fragmentation? 'size:delay_ms' (empty to disable)", "")
	fragSize, fragDelay := parseFragmentation(fragInput)

	var serverURL string
	if tunnelType == "wss" {
		serverPath := promptForInput(reader, "Enter Server Secret Path", "/connect")
		serverURL = fmt.Sprintf("wss://%s:%s%s", serverIP, serverPort, serverPath)
	} else {
		serverURL = fmt.Sprintf("%s:%s", serverIP, serverPort)
	}

	rateLimitStr := promptForInput(reader, "Enter Rate-Limit (KB/s, 0 for unlimited)", "0")
	rateLimit, _ := strconv.Atoi(rateLimitStr)
	rateLimit = rateLimit * 1024
	dashboardPort := promptForInput(reader, "Enter Dashboard Port", "8081")

	cmd := exec.Command(os.Args[0],
		"--mode", "client",
		"--ratelimit", strconv.Itoa(rateLimit),
		"--dashboard", dashboardPort,
		"--tunnel-type", tunnelType,
		"--token", authToken,
		"--frag-size", strconv.Itoa(fragSize),
		"--frag-delay", strconv.Itoa(fragDelay),
		serverURL, localAddrs)

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
			fmt.Println("❌ Could not confirm initial connection. Check token and logs.")
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

func parseFragmentation(input string) (size int, delay int) {
	if input == "" {
		return 0, 0
	}
	parts := strings.Split(input, ":")
	if len(parts) != 2 {
		return 0, 0
	}
	size, _ = strconv.Atoi(parts[0])
	delay, _ = strconv.Atoi(parts[1])
	if size <= 0 {
		return 0, 0
	}
	return size, delay
}

func pipeCount(dst io.Writer, src io.Reader, counter *int64, fragSize int, fragDelay int) {
	buf := bufferPool.Get().([]byte)
	defer bufferPool.Put(buf)

	for {
		readN, readErr := src.Read(buf)
		if readN > 0 {
			if counter != nil {
				stats.Lock()
				*counter += int64(readN)
				stats.Unlock()
			}

			if fragSize > 0 {
				offset := 0
				for offset < readN {
					end := offset + fragSize
					if end > readN {
						end = readN
					}
					fragment := buf[offset:end]
					
					_, writeErr := dst.Write(fragment)
					if writeErr != nil {
						return
					}
					
					if fragDelay > 0 {
						time.Sleep(time.Duration(fragDelay) * time.Millisecond)
					}
					
					offset = end
				}
			} else {
				_, writeErr := dst.Write(buf[:readN])
				if writeErr != nil {
					return
				}
			}
		}

		if readErr != nil {
			break
		}
	}
}


// =========================================================================
//                             SERVER LOGIC
// =========================================================================

func runServer(listenAddr, publicAddrs, path, certFile, keyFile string, ratelimit int, tunnelType, authToken string, fragSize, fragDelay int) {
	log.Printf("[Server Mode] 🚀 Starting process in %s mode...", tunnelType)
	currentSession := &activeSession{}

	ports := strings.Split(publicAddrs, ",")
	for i, port := range ports {
		if port == "" {
			continue
		}
		if !strings.HasPrefix(port, ":") {
			port = ":" + port
		}
		go startPublicListener(port, i, currentSession, ratelimit, fragSize, fragDelay)
	}

	yamuxConfig := yamux.DefaultConfig()
	yamuxConfig.KeepAliveInterval = 30 * time.Second
	yamuxConfig.ConnectionWriteTimeout = 30 * time.Second
	yamuxConfig.MaxStreamWindowSize = 2 * 1024 * 1024

	switch tunnelType {
	case "wss":
		listenWSS(listenAddr, path, certFile, keyFile, currentSession, yamuxConfig, authToken)
	case "tcpmux":
		listenTCPMux(listenAddr, currentSession, yamuxConfig, authToken)
	default:
		log.Fatalf("Unknown tunnel type: %s", tunnelType)
	}
}

func startPublicListener(publicAddr string, portIndex int, as *activeSession, ratelimit int, fragSize, fragDelay int) {
	publicListener, err := net.Listen("tcp", publicAddr)
	if err != nil {
		log.Printf("[Server] FATAL: Could not listen on public port %s: %v", publicAddr, err)
		return
	}
	defer publicListener.Close()
	log.Printf("[Server] ✅ Listening for public traffic on %s (Index: %d)", publicAddr, portIndex)

	for {
		publicConn, err := publicListener.Accept()
		if err != nil {
			continue
		}

		go func(publicConn net.Conn) {
			defer publicConn.Close()
			sess := as.Get()
			if sess == nil || sess.IsClosed() {
				return
			}

			stream, err := sess.OpenStream()
			if err != nil {
				return
			}
			defer stream.Close()

			stats.Lock()
			stats.ActiveConnections++
			stats.Unlock()
			defer func() {
				stats.Lock()
				stats.ActiveConnections--
				stats.Unlock()
			}()

			stream.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_, err = stream.Write([]byte{byte(portIndex)})
			stream.SetWriteDeadline(time.Time{})
			if err != nil {
				log.Printf("[Server] Failed to send port index to client: %v", err)
				return
			}

			c := publicConn
			if ratelimit > 0 {
				c = &rateLimitedConn{Conn: publicConn, rate: ratelimit}
			}

			go pipeCount(stream, c, &stats.TotalBytesIn, fragSize, fragDelay)
			pipeCount(c, stream, &stats.TotalBytesOut, 0, 0)
		}(publicConn)
	}
}

// MODIFIED: This function now creates a robust, optimized http.Server for WSS.
func listenWSS(listenAddr, path, certFile, keyFile string, as *activeSession, config *yamux.Config, authToken string) {
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if authToken != "" && r.Header.Get("X-Auth-Token") != authToken {
			log.Printf("[Server] WSS Auth failed for %s. Invalid token.", r.RemoteAddr)
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"tunnel"}})
		if err != nil {
			log.Printf("[Server] Websocket accept failed: %v", err)
			return
		}
		conn := websocket.NetConn(context.Background(), wsConn, websocket.MessageBinary)
		go handleNewClient(conn, as, config)
	})

	// Create a robust server with timeouts to prevent resource exhaustion from scanners.
	server := &http.Server{
		Addr:         listenAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  90 * time.Second, // Crucial for cleaning up stalled connections.
	}

	log.Printf("[Server] ✅ Listening for WSS tunnel on %s", listenAddr)
	if err := server.ListenAndServeTLS(certFile, keyFile); err != nil {
		log.Fatalf("[Server] HTTPS server failed: %v", err)
	}
}

func listenTCPMux(listenAddr string, as *activeSession, config *yamux.Config, authToken string) {
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("[Server] Raw TCP listener failed on %s: %v", listenAddr, err)
	}
	defer listener.Close()
	log.Printf("[Server] ✅ Listening for raw TCP Mux tunnel on %s", listenAddr)
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("[Server] Raw TCP accept error: %v", err)
			continue
		}
		go func(c net.Conn) {
			if authToken != "" {
				c.SetReadDeadline(time.Now().Add(10 * time.Second))
				token, err := bufio.NewReader(c).ReadString('\n')
				c.SetReadDeadline(time.Time{})
				if err != nil {
					log.Printf("[Server] Failed to read token from %s: %v", c.RemoteAddr(), err)
					c.Close()
					return
				}
				if strings.TrimSpace(token) != authToken {
					log.Printf("[Server] Auth failed for %s. Invalid token.", c.RemoteAddr())
					c.Close()
					return
				}
			}
			handleNewClient(c, as, config)
		}(conn)
	}
}

func handleNewClient(conn net.Conn, as *activeSession, config *yamux.Config) {
	log.Printf("[Server] 🤝 Authenticated client connected from %s", conn.RemoteAddr())
	session, err := yamux.Server(conn, config)
	if err != nil {
		log.Printf("[Server] Yamux server creation failed for %s: %v", conn.RemoteAddr(), err)
		return
	}
	as.Set(session)
	log.Println("[Server] ✅ Client session is now active.")
	stats.Lock()
	stats.Connected = true
	stats.Unlock()
	<-session.CloseChan()
	log.Printf("[Server] 🔌 Client session from %s has closed.", conn.RemoteAddr())
	stats.Lock()
	stats.Connected = false
	stats.Unlock()
}

// =========================================================================
//                             CLIENT LOGIC
// =========================================================================

func runClient(serverURL string, localAddrs string, ratelimit int, tunnelType, authToken string, fragSize, fragDelay int) {
	localAddrList := strings.Split(localAddrs, ",")
	if len(localAddrList) == 0 || localAddrList[0] == "" {
		log.Fatal("[Client] No local addresses provided to forward to. Exiting.")
	}
	log.Printf("[Client] Forwarding to %d local addresses: %v", len(localAddrList), localAddrList)

	yamuxConfig := yamux.DefaultConfig()
	yamuxConfig.KeepAliveInterval = 30 * time.Second
	yamuxConfig.ConnectionWriteTimeout = 30 * time.Second
	yamuxConfig.MaxStreamWindowSize = 2 * 1024 * 1024

	for {
		log.Printf("[Client] ... Attempting connection to %s using %s", serverURL, tunnelType)
		stats.Lock()
		stats.Connected = false
		stats.Unlock()

		var conn net.Conn
		var err error

		switch tunnelType {
		case "wss":
			header := http.Header{}
			if authToken != "" {
				header.Set("X-Auth-Token", authToken)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			wsConn, _, dialErr := websocket.Dial(ctx, serverURL, &websocket.DialOptions{
				Subprotocols:   []string{"tunnel"},
				HTTPClient:     &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}},
				HTTPHeader:     header,
			})
			cancel()
			if dialErr != nil {
				err = dialErr
			} else {
				conn = websocket.NetConn(context.Background(), wsConn, websocket.MessageBinary)
			}
		case "tcpmux":
			conn, err = net.DialTimeout("tcp", serverURL, 20*time.Second)
			if err == nil && authToken != "" {
				_, writeErr := conn.Write([]byte(authToken + "\n"))
				if writeErr != nil {
					err = writeErr
					conn.Close()
				}
			}
		default:
			log.Fatalf("Unknown client tunnel type: %s", tunnelType)
		}

		if err != nil {
			log.Printf("[Client] ❌ Connection failed: %v. Retrying in 5s...", err)
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

		session, err := yamux.Client(conn, yamuxConfig)
		if err != nil {
			log.Printf("[Client] ❌ Multiplexing failed: %v", err)
			conn.Close()
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
			go func(s *yamux.Stream) {
				defer s.Close()
				s.SetReadDeadline(time.Now().Add(5 * time.Second))
				idxByte := make([]byte, 1)
				_, err := s.Read(idxByte)
				s.SetReadDeadline(time.Time{})
				if err != nil {
					log.Printf("[Client] Failed to read port index from stream: %v", err)
					return
				}
				portIndex := int(idxByte[0])

				if portIndex < 0 || portIndex >= len(localAddrList) {
					log.Printf("[Client] Received invalid port index %d. Max is %d.", portIndex, len(localAddrList)-1)
					return
				}

				targetAddr := localAddrList[portIndex]
				log.Printf("[Client] New stream for index %d -> %s", portIndex, targetAddr)
				
				localConn, err := net.Dial("tcp", targetAddr)
				if err != nil {
					log.Printf("[Client] Failed to dial local service '%s': %v", targetAddr, err)
					return
				}
				defer localConn.Close()
				
				stats.Lock()
				stats.ActiveConnections++
				stats.Unlock()
				defer func() {
					stats.Lock()
					stats.ActiveConnections--
					stats.Unlock()
				}()

				c := localConn
				if ratelimit > 0 {
					c = &rateLimitedConn{Conn: localConn, rate: ratelimit}
				}
				
				go pipeCount(c, s, &stats.TotalBytesOut, 0, 0)
				pipeCount(s, c, &stats.TotalBytesIn, fragSize, fragDelay)
			}(stream)
		}
	}
}

// ... (The rest of the file remains unchanged) ...
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
      © 2025 Phantom Tunnel — webwizards-team
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
