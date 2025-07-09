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
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
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

// --- نقطه شروع اصلی برنامه ---
func main() {
	mode := flag.String("mode", "", "internal: 'server' or 'client'")
	flag.Parse()

	if *mode != "" {
		configureLogging()
		args := flag.Args()
		if *mode == "server" {
			if len(args) < 5 { log.Fatal("Internal error: Not enough arguments for server mode.") }
			runServer(args[0], args[1], args[2], args[3], args[4])
		} else if *mode == "client" {
			if len(args) < 2 { log.Fatal("Internal error: Not enough arguments for client mode.") }
			runClient(args[0], args[1])
		}
		return
	}
	showInteractiveMenu()
}

// --- منوی تعاملی ---
func showInteractiveMenu() {
	fmt.Println("=======================================")
	fmt.Println("   👻 Phantom Tunnel (Core v2.0)     ")
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
		case "1": setupServer(reader)
		case "2": setupClient(reader)
		case "3": monitorLogs()
		case "4": stopAndCleanTunnel(reader)
		case "5": uninstallSelf(reader)
		case "6": fmt.Println("Exiting."); os.Exit(0)
		default: fmt.Println("Invalid choice. Please try again.")
		}
	}
}

// --- توابع مدیریتی ---
func setupServer(reader *bufio.Reader) {
	if isTunnelRunning() {
		fmt.Println("A tunnel is already running. Stop it first with option '4'.")
		return
	}
	fmt.Println("\n--- 👻 Server Setup ---")
	listenAddr := promptForInput(reader, "Enter Tunnel Port", "443")
	publicAddr := promptForInput(reader, "Enter Public Port", "8000")
	path := promptForInput(reader, "Enter Secret URL Path", "/"+generateRandomPath())
	if !strings.HasPrefix(listenAddr, ":") { listenAddr = ":" + listenAddr }
	if !strings.HasPrefix(publicAddr, ":") { publicAddr = ":" + publicAddr }

	if _, err := os.Stat("server.crt"); os.IsNotExist(err) {
		fmt.Println("SSL certificate not found. Generating a new one...")
		if err := generateSelfSignedCert(); err != nil { log.Fatalf("Failed to generate SSL: %v", err) }
		fmt.Println("✅ SSL certificate 'server.crt' and 'server.key' generated.")
	} else {
		fmt.Println("✅ Existing SSL certificate found.")
	}
	cmd := exec.Command(os.Args[0], "--mode", "server", listenAddr, publicAddr, path, "server.crt", "server.key")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		fmt.Printf("Error starting server process: %v\n", err); return
	}
	pid := cmd.Process.Pid
	_ = os.WriteFile(pidFilePath, []byte(strconv.Itoa(pid)), 0644)
	fmt.Printf("\n✅ Server process started in the background (PID: %d).\n", pid)
}

func setupClient(reader *bufio.Reader) {
	if isTunnelRunning() {
		fmt.Println("A tunnel is already running. Stop it first with option '4'."); return
	}
	fmt.Println("\n--- 👻 Client Setup ---")
	serverIP := promptForInput(reader, "Enter Server IP or Hostname", "")
	if serverIP == "" { fmt.Println("Error: Server IP cannot be empty."); return }
	serverPort := promptForInput(reader, "Enter Server Tunnel Port", "443")
	serverPath := promptForInput(reader, "Enter Server Secret Path", "/connect")
	localAddr := promptForInput(reader, "Enter Local Service Address", "localhost:3000")
	serverURL := fmt.Sprintf("wss://%s:%s%s", serverIP, serverPort, serverPath)
	
	cmd := exec.Command(os.Args[0], "--mode", "client", serverURL, localAddr)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		fmt.Printf("Error starting client process: %v\n", err); return
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
			fmt.Println("❌ Could not confirm initial connection within 20 seconds."); return
		case <-ticker.C:
			if _, err := os.Stat(successSignalPath); err == nil {
				os.Remove(successSignalPath)
				fmt.Println("✅ Tunnel connection established successfully! Running in the background."); return
			}
		}
	}
}

func stopAndCleanTunnel(reader *bufio.Reader) {
	fmt.Println("\nThis will stop any running tunnel AND delete all generated files.")
	fmt.Print("Are you sure? [y/N]: ")
	confirm, _ := reader.ReadString('\n')
	if strings.TrimSpace(strings.ToLower(confirm)) != "y" {
		fmt.Println("Operation cancelled."); return
	}
	if pidBytes, err := os.ReadFile(pidFilePath); err == nil {
		pid, _ := strconv.Atoi(string(pidBytes))
		if process, err := os.FindProcess(pid); err == nil {
			fmt.Printf("Stopping tunnel process (PID: %d)...\n", pid)
			if err := process.Signal(syscall.SIGTERM); err == nil { fmt.Println("  - Process stopped successfully.") }
		}
	} else {
		fmt.Println("No running process found, proceeding with file cleanup.")
	}
	fmt.Println("Cleaning up generated files...")
	deleteFile("server.crt"); deleteFile("server.key"); deleteFile(logFilePath)
	deleteFile(pidFilePath); deleteFile(successSignalPath)
	fmt.Println("✅ Cleanup complete.")
}

func uninstallSelf(reader *bufio.Reader) {
	if isTunnelRunning() { fmt.Println("A tunnel is running. Stop and clean it first."); return }
	fmt.Println("\nWARNING: This will permanently remove the 'phantom-tunnel' command.")
	fmt.Print("Are you sure? [y/N]: ")
	if confirm, _ := reader.ReadString('\n'); strings.TrimSpace(strings.ToLower(confirm)) != "y" {
		fmt.Println("Uninstall cancelled."); return
	}
	executablePath, err := os.Executable()
	if err != nil { fmt.Println("Error: Could not determine executable path:", err); return }
	deleteFile(pidFilePath); deleteFile(logFilePath); deleteFile(successSignalPath)
	fmt.Printf("Removing executable: %s\n", executablePath)
	if err = os.Remove(executablePath); err != nil {
		fmt.Printf("Error: Failed to remove executable: %v\n", err); return
	}
	fmt.Println("✅ Phantom Tunnel has been successfully uninstalled.")
	os.Exit(0)
}

// --- هسته اصلی و منطق تونل ---
func runServer(listenAddr, publicAddr, path, certFile, keyFile string) {
	log.Println("[Server Mode] 🚀 Starting process...")
	var session *yamux.Session
	httpServer := &http.Server{
		Addr: listenAddr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != path { http.NotFound(w, r); return }
			wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"tunnel"}})
			if err != nil { log.Printf("[Server] Websocket accept failed: %v", err); return }
			conn := websocket.NetConn(context.Background(), wsConn, websocket.MessageBinary)
			log.Println("[Server] 🤝 WebSocket tunnel established!")
			session, _ = yamux.Server(conn, nil)
		}),
	}
	go func() {
		log.Printf("[Server] ✅ Listening for tunnel on wss://%s", listenAddr)
		if err := httpServer.ListenAndServeTLS(certFile, keyFile); err != nil {
			log.Fatalf("[Server] HTTPS server failed: %v", err)
		}
	}()
	publicListener, err := net.Listen("tcp", publicAddr)
	if err != nil { log.Fatalf("[Server] Public listener failed on %s: %v", publicAddr, err) }
	log.Printf("[Server] ✅ Listening for public traffic on %s", publicAddr)
	for {
		publicConn, err := publicListener.Accept()
		if err != nil { continue }
		go func() {
			defer publicConn.Close()
			if session == nil || session.IsClosed() { return }
			stream, err := session.OpenStream()
			if err != nil { return }
			defer stream.Close()
			go func() { _, _ = io.Copy(stream, publicConn) }()
			_, _ = io.Copy(publicConn, stream)
		}()
	}
}

func runClient(serverURL, localAddr string) {
	for {
		log.Printf("[Client] ... Attempting connection to %s", serverURL)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		wsConn, _, err := websocket.Dial(ctx, serverURL, &websocket.DialOptions{
			Subprotocols: []string{"tunnel"},
			HTTPClient: &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}},
		})
		cancel()
		if err != nil {
			log.Printf("[Client] ❌ Connection failed: %v. Retrying...", err)
			time.Sleep(5 * time.Second); continue
		}
		log.Println("[Client] ✅ Tunnel connection established!")
		if f, err := os.Create(successSignalPath); err == nil { f.Close() }
		session, err := yamux.Client(websocket.NetConn(context.Background(), wsConn, websocket.MessageBinary), nil)
		if err != nil { log.Printf("[Client] ❌ Multiplexing failed: %v", err); continue }
		for {
			stream, err := session.AcceptStream()
			if err != nil {
				log.Printf("[Client] ... Session terminated: %v. Reconnecting...", err); break
			}
			go func() {
				defer stream.Close()
				localConn, err := net.Dial("tcp", localAddr)
				if err != nil { return }
				defer localConn.Close()
				go func() { _, _ = io.Copy(localConn, stream) }()
				_, _ = io.Copy(stream, localConn)
			}()
		}
	}
}

// --- توابع کمکی ---
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
	if err != nil { log.Fatalf("Failed to open log file: %v", err) }
	log.SetOutput(logFile)
}
func promptForInput(reader *bufio.Reader, promptText, defaultValue string) string {
	fmt.Printf("%s [%s]: ", promptText, defaultValue)
	input, _ := reader.ReadString('\n'); input = strings.TrimSpace(input)
	if input == "" { return defaultValue }; return input
}
func deleteFile(filePath string) {
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		fmt.Printf("  - Error deleting %s: %v\n", filePath, err)
	} else if err == nil {
		fmt.Printf("  - Deleted: %s\n", filePath)
	}
}
func generateSelfSignedCert() error {
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
func generateRandomPath() string {
	bytes := make([]byte, 8); if _, err := rand.Read(bytes); err != nil { return "secret-path" }
	return hex.EncodeToString(bytes)
}
