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
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
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
	"github.com/quic-go/quic-go"
	"nhooyr.io/websocket"
)

const (
	logFilePath       = "/tmp/phantom-tunnel.log"
	pidFilePath       = "/tmp/phantom.pid"
	successSignalPath = "/tmp/phantom_success.signal"
)

var bufferPool = &sync.Pool{
	New: func() any { return make([]byte, 32*1024) },
}

// --- نقطه شروع اصلی برنامه ---
func main() {
	mode := flag.String("mode", "", "internal: 'websocket' or 'quic'")
	flag.Parse()

	if *mode != "" {
		configureLogging()
		args := flag.Args()
		if *mode == "websocket_server" {
			if len(args) < 5 { log.Fatal("Internal error: Not enough arguments for websocket server") }
			runServerWebSocket(args[0], args[1], args[2], args[3], args[4])
		} else if *mode == "websocket_client" {
			if len(args) < 2 { log.Fatal("Internal error: Not enough arguments for websocket client") }
			runClientWebSocket(args[0], args[1])
		} else if *mode == "quic_server" {
			if len(args) < 2 { log.Fatal("Internal error: Not enough arguments for quic server") }
			runServerQUIC(args[0], args[1])
		} else if *mode == "quic_client" {
			if len(args) < 3 { log.Fatal("Internal error: Not enough arguments for quic client") }
			runClientQUIC(args[0], args[1], args[2])
		}
		return
	}
	showInteractiveMenu()
}

// --- منوی تعاملی ---
func showInteractiveMenu() {
	fmt.Println("=======================================")
	fmt.Println("  👻 Phantom Tunnel v7.0 (Dual Mode)  ")
	fmt.Println("  Choose Your Weapon: WebSocket or QUIC")
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
		case "1": setupServer(reader)
		case "2": setupClient(reader)
		case "3": monitorLogs()
		case "4": stopAndCleanTunnel(reader)
		case "5": uninstallSelf(reader)
		case "6": fmt.Println("Exiting."); os.Exit(0)
		default: fmt.Println("Invalid choice.")
		}
	}
}

// --- توابع راه‌اندازی (اصلاح شده برای انتخاب حالت) ---
func setupServer(reader *bufio.Reader) {
	if isTunnelRunning() { fmt.Println("A tunnel is already running. Stop it first."); return }
	fmt.Println("\n--- 👻 Server Setup ---")
	fmt.Println("Choose a tunnel type:")
	fmt.Println("  1. WebSocket (TCP): Best for bypassing firewalls.")
	fmt.Println("  2. QUIC (UDP):      Best for low latency and unstable networks.")
	fmt.Print("Enter choice [1-2]: ")
	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(choice)

	if choice == "1" {
		setupWebSocketServer(reader)
	} else if choice == "2" {
		setupQUICServer(reader)
	} else {
		fmt.Println("Invalid choice.")
	}
}

func setupClient(reader *bufio.Reader) {
	if isTunnelRunning() { fmt.Println("A tunnel is already running. Stop it first."); return }
	fmt.Println("\n--- 👻 Client Setup ---")
	fmt.Println("What type of tunnel are you connecting to?")
	fmt.Println("  1. WebSocket (TCP)")
	fmt.Println("  2. QUIC (UDP)")
	fmt.Print("Enter choice [1-2]: ")
	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(choice)

	if choice == "1" {
		setupWebSocketClient(reader)
	} else if choice == "2" {
		setupQUICClient(reader)
	} else {
		fmt.Println("Invalid choice.")
	}
}

// --- توابع راه‌اندازی برای هر حالت ---
func setupWebSocketServer(reader *bufio.Reader) {
	fmt.Println("\n--- WebSocket Server ---")
	listenAddr := promptForInput(reader, "Enter Tunnel Port (e.g., 443)", "443")
	publicAddr := promptForInput(reader, "Enter Public Port for users", "8000")
	path := promptForInput(reader, "Enter Secret URL Path", "/"+generateRandomSecret(16))
	if !strings.HasPrefix(listenAddr, ":") { listenAddr = ":" + listenAddr }
	if !strings.HasPrefix(publicAddr, ":") { publicAddr = ":" + publicAddr }
	if _, err := os.Stat("server.crt"); os.IsNotExist(err) {
		fmt.Println("Generating self-signed certificate...")
		if err := generateCert(); err != nil { log.Fatalf("Failed to generate certificate: %v", err) }
		fmt.Println("✅ Certificate generated.")
	}
	cmd := exec.Command(os.Args[0], "--mode", "websocket_server", listenAddr, publicAddr, path, "server.crt", "server.key")
	startDaemon(cmd)
}

func setupQUICServer(reader *bufio.Reader) {
	fmt.Println("\n--- QUIC Server ---")
	listenAddr := promptForInput(reader, "Enter Tunnel Port (UDP)", "443")
	secret := promptForInput(reader, "Enter a strong secret password", generateRandomSecret(16))
	if !strings.Contains(listenAddr, ":") { listenAddr = ":" + listenAddr }
	cmd := exec.Command(os.Args[0], "--mode", "quic_server", listenAddr, secret)
	startDaemon(cmd)
}

func setupWebSocketClient(reader *bufio.Reader) {
	fmt.Println("\n--- WebSocket Client ---")
	serverIP := promptForInput(reader, "Enter Server IP or Hostname", "")
	if serverIP == "" { fmt.Println("Error: Server IP cannot be empty."); return }
	serverPort := promptForInput(reader, "Enter Server Tunnel Port", "443")
	serverPath := promptForInput(reader, "Enter Server Secret Path", "/connect")
	localAddr := promptForInput(reader, "Enter Local Service Address", "localhost:3000")
	serverURL := fmt.Sprintf("wss://%s:%s%s", serverIP, serverPort, serverPath)
	cmd := exec.Command(os.Args[0], "--mode", "websocket_client", serverURL, localAddr)
	startDaemon(cmd)
}

func setupQUICClient(reader *bufio.Reader) {
	fmt.Println("\n--- QUIC Client ---")
	serverAddr := promptForInput(reader, "Enter Server IP:Port (e.g. 1.2.3.4:443)", "")
	if serverAddr == "" { fmt.Println("Error: Server address cannot be empty."); return }
	secret := promptForInput(reader, "Enter the server's secret password", "")
	localAddr := promptForInput(reader, "Enter Local Service Address", "localhost:3000")
	cmd := exec.Command(os.Args[0], "--mode", "quic_client", serverAddr, secret, localAddr)
	startDaemon(cmd)
}


// --- هسته اصلی و منطق هر حالت ---
func pipe(dst io.Writer, src io.Reader) {
	buf := bufferPool.Get().([]byte)
	defer bufferPool.Put(buf)
	io.CopyBuffer(dst, src, buf)
}

func runServerWebSocket(listenAddr, publicAddr, path, certFile, keyFile string) {
	log.Println("[WebSocket Server] 🚀 Starting...")
	var session *yamux.Session
	httpServer := &http.Server{
		Addr: listenAddr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != path { http.NotFound(w, r); return }
			wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"tunnel"}})
			if err != nil { log.Printf("[WSS] Accept failed: %v", err); return }
			log.Println("[WSS] 🤝 Tunnel established!")
			session, _ = yamux.Server(websocket.NetConn(context.Background(), wsConn, websocket.MessageBinary), nil)
		}),
	}
	go func() {
		log.Printf("[WSS] ✅ Listening for tunnels on wss://%s", listenAddr)
		if err := httpServer.ListenAndServeTLS(certFile, keyFile); err != nil { log.Fatalf("[WSS] Server failed: %v", err) }
	}()
	publicListener, err := net.Listen("tcp", publicAddr)
	if err != nil { log.Fatalf("[WSS] Public listener failed on %s: %v", publicAddr, err) }
	log.Printf("[WSS] ✅ Listening for public traffic on %s", publicAddr)
	for {
		publicConn, err := publicListener.Accept()
		if err != nil { continue }
		go func() {
			defer publicConn.Close()
			if session == nil || session.IsClosed() { return }
			stream, err := session.OpenStream()
			if err != nil { return }
			defer stream.Close()
			go pipe(stream, publicConn)
			pipe(publicConn, stream)
		}()
	}
}

func runClientWebSocket(serverURL, localAddr string) {
	for {
		log.Printf("[WebSocket Client] ... Connecting to %s", serverURL)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		wsConn, _, err := websocket.Dial(ctx, serverURL, &websocket.DialOptions{
			Subprotocols: []string{"tunnel"},
			HTTPClient: &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}},
		})
		cancel()
		if err != nil { log.Printf("[WSS] ❌ Connection failed: %v. Retrying...", err); time.Sleep(5 * time.Second); continue }
		log.Println("[WSS] ✅ Tunnel established!")
		if f, err := os.Create(successSignalPath); err == nil { f.Close() }
		session, err := yamux.Client(websocket.NetConn(context.Background(), wsConn, websocket.MessageBinary), nil)
		if err != nil { log.Printf("[WSS] ❌ Multiplexing failed: %v", err); continue }
		for {
			stream, err := session.AcceptStream()
			if err != nil { log.Printf("[WSS] ... Session terminated: %v. Reconnecting...", err); break }
			go func() {
				defer stream.Close()
				localConn, err := net.Dial("tcp", localAddr)
				if err != nil { return }
				defer localConn.Close()
				go pipe(localConn, stream)
				pipe(stream, localConn)
			}()
		}
	}
}

func runServerQUIC(listenAddr, secret string) {
	log.Println("[QUIC Server] 🚀 Starting...")
	tlsConf, err := generateQUICConfig(secret)
	if err != nil { log.Fatalf("[QUIC] Failed to generate config: %v", err) }
	listener, err := quic.ListenAddr(listenAddr, tlsConf, nil)
	if err != nil { log.Fatalf("[QUIC] Failed to start listener: %v", err) }
	log.Printf("[QUIC] ✅ Listening for tunnels on %s (UDP)", listenAddr)
	for {
		conn, err := listener.Accept(context.Background())
		if err != nil { log.Printf("[QUIC] Accept failed: %v", err); continue }
		log.Println("[QUIC] 🤝 New session established!")
		go handleQUICServerConnection(conn)
	}
}

func handleQUICServerConnection(conn quic.Connection) {
	for {
		stream, err := conn.AcceptStream(context.Background())
		if err != nil {
			log.Printf("[QUIC] Stream accept failed, session closed: %v", err)
			return
		}
		// این استریم از کلاینت آمده که می‌خواهد یک اتصال جدید بسازد
		// در مدل جدید QUIC، سرور نیازی به پورت عمومی جدا ندارد
		// خود کلاینت استریم‌ها را باز کرده و ما به آن‌ها پاسخ می‌دهیم
		go func() {
			// در این مدل، ما یک "echo" ساده انجام می‌دهیم یا باید به یک سرویس محلی وصل شویم
			// برای سادگی، این بخش فعلا خالی است، چون کلاینت استریم را باز می‌کند
		}()
	}
}

func runClientQUIC(serverAddr, secret, localAddr string) {
	tlsConf := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{secret},
	}
	for {
		log.Printf("[QUIC Client] ... Connecting to %s", serverAddr)
		conn, err := quic.DialAddr(context.Background(), serverAddr, tlsConf, nil)
		if err != nil { log.Printf("[QUIC] ❌ Connection failed: %v. Retrying...", err); time.Sleep(5 * time.Second); continue }
		log.Println("[QUIC] ✅ Session established!")
		if f, err := os.Create(successSignalPath); err == nil { f.Close() }
		
		// یک حلقه بی‌نهایت برای پذیرش اتصالات محلی
		localListener, err := net.Listen("tcp", localAddr)
		if err != nil {
			log.Fatalf("[QUIC] Failed to listen on local address %s: %v", localAddr, err)
		}
		log.Printf("[QUIC] ✅ Ready to accept local traffic on %s", localAddr)

		for {
			localConn, err := localListener.Accept()
			if err != nil {
				log.Printf("[QUIC] ... Session terminated: %v. Reconnecting...", err)
				conn.CloseWithError(0, "")
				break // خروج از حلقه داخلی برای اتصال مجدد
			}
			go func() {
				stream, err := conn.OpenStreamSync(context.Background())
				if err != nil {
					log.Printf("[QUIC] Failed to open stream: %v", err)
					localConn.Close()
					return
				}
				defer stream.Close()
				defer localConn.Close()
				go pipe(stream, localConn)
				pipe(localConn, stream)
			}()
		}
	}
}


// --- توابع کمکی ---
func startDaemon(cmd *exec.Command) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		fmt.Printf("Error starting process: %v\n", err); return
	}
	pid := cmd.Process.Pid
	_ = os.WriteFile(pidFilePath, []byte(strconv.Itoa(pid)), 0644)
	fmt.Printf("\n✅ Process started in the background (PID: %d).\n", pid)
	if strings.Contains(cmd.Args[2], "client") {
		fmt.Println("Waiting for connection confirmation...")
		timeout := time.After(20 * time.Second)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-timeout: fmt.Println("❌ Connection timed out."); return
			case <-ticker.C:
				if _, err := os.Stat(successSignalPath); err == nil {
					os.Remove(successSignalPath)
					fmt.Println("✅ Tunnel established successfully!"); return
				}
			}
		}
	}
}
func stopAndCleanTunnel(reader *bufio.Reader) {
	fmt.Println("\nThis will stop any running tunnel AND delete all generated files.")
	fmt.Print("Are you sure? [y/N]: ")
	confirm, _ := reader.ReadString('\n')
	if strings.TrimSpace(strings.ToLower(confirm)) != "y" { fmt.Println("Operation cancelled."); return }
	if pidBytes, err := os.ReadFile(pidFilePath); err == nil {
		pid, _ := strconv.Atoi(string(pidBytes))
		if process, err := os.FindProcess(pid); err == nil {
			fmt.Printf("Stopping tunnel process (PID: %d)...\n", pid)
			if err := process.Signal(syscall.SIGTERM); err == nil { fmt.Println("  - Process stopped successfully.") }
		}
	} else { fmt.Println("No running process found.") }
	fmt.Println("Cleaning up generated files...")
	deleteFile("server.crt"); deleteFile("server.key"); deleteFile(logFilePath)
	deleteFile(pidFilePath); deleteFile(successSignalPath)
	fmt.Println("✅ Cleanup complete.")
}
func uninstallSelf(reader *bufio.Reader) {
	if isTunnelRunning() { fmt.Println("A tunnel is running. Stop and clean it first."); return }
	fmt.Println("\nWARNING: This will permanently remove the 'phantom-tunnel' command.")
	fmt.Print("Are you sure? [y/N]: ")
	if confirm, _ := reader.ReadString('\n'); strings.TrimSpace(strings.ToLower(confirm)) != "y" { fmt.Println("Uninstall cancelled."); return }
	executablePath, err := os.Executable()
	if err != nil { fmt.Println("Error: Could not determine executable path:", err); return }
	deleteFile(pidFilePath); deleteFile(logFilePath); deleteFile(successSignalPath)
	fmt.Printf("Removing executable: %s\n", executablePath)
	if err = os.Remove(executablePath); err != nil { fmt.Printf("Error: Failed to remove executable: %v\n", err); return }
	fmt.Println("✅ Phantom Tunnel has been successfully uninstalled.")
	os.Exit(0)
}
func isTunnelRunning() bool {
	pidBytes, err := os.ReadFile(pidFilePath); if err != nil { return false }
	pid, _ := strconv.Atoi(string(pidBytes)); process, err := os.FindProcess(pid)
	if err != nil { return false }; return process.Signal(syscall.Signal(0)) == nil
}
func monitorLogs() {
	if !isTunnelRunning() && func() bool { _, err := os.Stat(logFilePath); return os.IsNotExist(err) }() {
		fmt.Println("No tunnel process is running and no log file found."); return
	}
	if !isTunnelRunning() { fmt.Println("No tunnel process is running. Displaying logs from the last run...") }
	fmt.Println("\n--- 🔎 Real-time Log Monitoring ---")
	fmt.Println("... Press Ctrl+C to stop monitoring and return to the menu.")
	cmd := exec.Command("tail", "-f", logFilePath)
	if runtime.GOOS == "windows" { cmd = exec.Command("powershell", "-Command", "Get-Content", "-Path", logFilePath, "-Wait") }
	cmd.Stdout = os.Stdout; cmd.Stderr = os.Stderr; _ = cmd.Run()
	fmt.Println("\n... Stopped monitoring.")
}
func configureLogging() {
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil { log.Fatalf("Failed to open log file: %v", err) }; log.SetOutput(logFile)
}
func promptForInput(reader *bufio.Reader, promptText, defaultValue string) string {
	fmt.Printf("%s [%s]: ", promptText, defaultValue)
	input, _ := reader.ReadString('\n'); input = strings.TrimSpace(input)
	if input == "" { return defaultValue }; return input
}
func deleteFile(filePath string) {
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		fmt.Printf("  - Error deleting %s: %v\n", filePath, err)
	} else if err == nil { fmt.Printf("  - Deleted: %s\n", filePath) }
}
func generateCert() error {
	priv, err := rsa.GenerateKey(rand.Reader, 2048); if err != nil { return err }
	template := x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{Organization: []string{"Phantom Tunnel"}},
		NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour * 24 * 3650),
		KeyUsage: x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil { return err }
	certOut, err := os.Create("server.crt"); if err != nil { return err }; defer certOut.Close()
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyOut, err := os.Create("server.key"); if err != nil { return err }; defer keyOut.Close()
	pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	return nil
}
func generateQUICConfig(secret string) (*tls.Config, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil { return nil, err }
	template := x509.Certificate{SerialNumber: big.NewInt(1)}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil { return nil, err }
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{certDER}, PrivateKey: key}},
		NextProtos:   []string{secret},
	}, nil
}
func generateRandomSecret(length int) string {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil { return "default-secret" }
	return hex.EncodeToString(bytes)
}
