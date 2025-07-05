// Filename: phantom.go
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
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/yamux"
	"nhooyr.io/websocket"
)

func main() {
	fmt.Println("=======================================")
	fmt.Println("    👻 Phantom Tunnel v1.0    ")
	fmt.Println("    Make your traffic disappear.    ")
	fmt.Println("=======================================")

	reader := bufio.NewReader(os.Stdin)

	fmt.Println("Select the mode to run in:")
	fmt.Println("  1. Server Mode (Run this on your public VPS)")
	fmt.Println("  2. Client Mode (Run this on the machine with the local service)")
	fmt.Print("Enter your choice [1-2]: ")

	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(choice)

	switch choice {
	case "1":
		setupServer(reader)
	case "2":
		setupClient(reader)
	default:
		log.Fatalln("Invalid choice. Please run the application again.")
	}
}

// --- Interactive Setup ---

func promptForInput(reader *bufio.Reader, promptText, defaultValue string) string {
	fmt.Printf("%s [%s]: ", promptText, defaultValue)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return defaultValue
	}
	return input
}

func generateRandomPath() string {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return "secret-path"
	}
	return hex.EncodeToString(bytes)
}

func setupServer(reader *bufio.Reader) {
	fmt.Println("\n--- 👻 Server Setup ---")
	listenAddr := promptForInput(reader, "Enter Tunnel Port (for client to connect)", "443")
	publicAddr := promptForInput(reader, "Enter Public Port (for users to access)", "8000")
	path := promptForInput(reader, "Enter Secret URL Path", "/"+generateRandomPath())

	if !strings.HasPrefix(listenAddr, ":") {
		listenAddr = ":" + listenAddr
	}
	if !strings.HasPrefix(publicAddr, ":") {
		publicAddr = ":" + publicAddr
	}

	fmt.Println("\nChecking for SSL certificate...")
	if _, err := os.Stat("server.crt"); os.IsNotExist(err) {
		fmt.Println("SSL certificate not found. Generating a new one...")
		err := generateSelfSignedCert()
		if err != nil {
			log.Fatalf("Failed to generate SSL certificate: %v", err)
		}
		fmt.Println("✅ SSL certificate 'server.crt' and 'server.key' generated successfully.")
	} else {
		fmt.Println("✅ Existing SSL certificate found.")
	}

	fmt.Println("\n--- Configuration Summary ---")
	fmt.Printf("  Mode: Server\n")
	fmt.Printf("  Tunnel Listening on: %s\n", listenAddr)
	fmt.Printf("  Public Port for Users: %s\n", publicAddr)
	fmt.Printf("  Secret Path: %s\n", path)
	fmt.Println("-----------------------------")

	runServer(listenAddr, publicAddr, path, "server.crt", "server.key")
}

func setupClient(reader *bufio.Reader) {
	fmt.Println("\n--- 👻 Client Setup ---")
	serverIP := promptForInput(reader, "Enter Server IP or Hostname", "")
	if serverIP == "" {
		log.Fatalln("Server IP cannot be empty.")
	}
	serverPort := promptForInput(reader, "Enter Server Tunnel Port", "443")
	serverPath := promptForInput(reader, "Enter Server Secret Path", "/connect")
	localAddr := promptForInput(reader, "Enter Local Service Address (e.g. localhost:3000)", "localhost:3000")

	serverURL := fmt.Sprintf("wss://%s:%s%s", serverIP, serverPort, serverPath)

	fmt.Println("\n--- Configuration Summary ---")
	fmt.Printf("  Mode: Client\n")
	fmt.Printf("  Connecting to Server: %s\n", serverURL)
	fmt.Printf("  Forwarding to Local Service: %s\n", localAddr)
	fmt.Println("-----------------------------")

	runClient(serverURL, localAddr)
}

// --- Core Logic (largely unchanged, but accepts params now) ---

func runServer(listenAddr, publicAddr, path, certFile, keyFile string) {
    // ... server logic from previous response ... (see collapsed section below)
	log.Println("[Server Mode] 🚀 Starting Ghost-Mode Server...")

	var session *yamux.Session

	httpServer := &http.Server{
		Addr: listenAddr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != path { http.NotFound(w, r); return }
			wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"tunnel"}})
			if err != nil { log.Printf("[Server Mode] Failed to accept websocket: %v", err); return }
			conn := websocket.NetConn(context.Background(), wsConn, websocket.MessageBinary)
			log.Println("[Server Mode] 🤝 WebSocket tunnel established!")
			session, _ = yamux.Server(conn, nil)
		}),
		TLSNextProto: make(map[string]func(*http.Server, *tls.Conn, http.Handler)),
	}
	
	go func() {
		log.Printf("[Server Mode] ✅ Ready to accept stealth tunnel on wss://%s%s", listenAddr, path)
		if err := httpServer.ListenAndServeTLS(certFile, keyFile); err != nil {
			log.Fatalf("[Server Mode] HTTPS server failed: %v", err)
		}
	}()

	publicListener, err := net.Listen("tcp", publicAddr)
	if err != nil { log.Fatalf("[Server Mode] Failed to listen on public port %s: %v", publicAddr, err) }
	log.Printf("[Server Mode] ✅ Ready to accept public traffic on %s", publicAddr)

	for {
		publicConn, err := publicListener.Accept()
		if err != nil { continue }
		go func() {
			defer publicConn.Close()
			if session == nil || session.IsClosed() { publicConn.Close(); return }
			stream, err := session.OpenStream()
			if err != nil { publicConn.Close(); return }
			defer stream.Close()
			go func() { _, _ = io.Copy(stream, publicConn) }()
			_, _ = io.Copy(publicConn, stream)
		}()
	}
}


func runClient(serverURL, localAddr string) {
    // ... client logic from previous response ... (see collapsed section below)
	for {
		log.Printf("[Client Mode] ... Attempting stealth connection to %s", serverURL)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		wsConn, _, err := websocket.Dial(ctx, serverURL, &websocket.DialOptions{
			Subprotocols: []string{"tunnel"},
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		})
		cancel()

		if err != nil {
			log.Printf("[Client Mode] ❌ Connection failed: %v. Retrying...", err)
			time.Sleep(5 * time.Second)
			continue
		}
		
		conn := websocket.NetConn(context.Background(), wsConn, websocket.MessageBinary)
		log.Println("[Client Mode] ✅ Stealth tunnel established!")

		session, err := yamux.Client(conn, nil)
		if err != nil { log.Printf("[Client Mode] ❌ Multiplexing failed: %v", err); continue }

		for {
			stream, err := session.AcceptStream()
			if err != nil { log.Printf("[Client Mode] ... Session terminated: %v. Reconnecting...", err); break }
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

// --- SSL Generation ---
func generateSelfSignedCert() error {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Phantom Tunnel"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour * 24 * 3650), // 10 years
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
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
