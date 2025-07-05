package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/hashicorp/yamux"
	"nhooyr.io/websocket"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		return
	}
	switch os.Args[1] {
	case "server":
		runServer()
	case "client":
		runClient()
	default:
		printUsage()
	}
}

func printUsage() {
	fmt.Println("Usage: better-tunnel <command> [arguments]")
	fmt.Println("\nA high-performance, stealthy, and resilient tunneling tool.")
	fmt.Println("\nCommands:")
	fmt.Println("  server    Run in stealth server mode (WebSocket over TLS)")
	fmt.Println("  client    Run in stealth client mode (WebSocket over TLS)")
	fmt.Println("\nUse \"better-tunnel <command> -h\" for more information on a specific command.")
}

// SERVER LOGIC
func runServer() {
	serverCmd := flag.NewFlagSet("server", flag.ExitOnError)
	listenAddr := serverCmd.String("listen", ":443", "Address to listen on for stealth connections (e.g., :443)")
	publicAddr := serverCmd.String("public", ":8000", "Public port to listen for incoming user traffic")
	path := serverCmd.String("path", "/connect", "WebSocket URL path for the tunnel")
	certFile := serverCmd.String("cert", "server.crt", "Path to TLS certificate file")
	keyFile := serverCmd.String("key", "server.key", "Path to TLS key file")
	serverCmd.Parse(os.Args[2:])

	log.Println("[Server Mode] 🚀 Starting Ghost-Mode Server...")

	var session *yamux.Session

	httpServer := &http.Server{
		Addr: *listenAddr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != *path {
				http.NotFound(w, r)
				return
			}
			wsConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
				Subprotocols: []string{"tunnel"},
			})
			if err != nil {
				log.Printf("[Server Mode] Failed to accept websocket connection: %v", err)
				return
			}
			conn := websocket.NetConn(context.Background(), wsConn, websocket.MessageBinary)
			log.Println("[Server Mode] 🤝 WebSocket tunnel established!")
			session, _ = yamux.Server(conn, nil)
		}),
		TLSNextProto: make(map[string]func(*http.Server, *tls.Conn, http.Handler)),
	}
	
	go func() {
		log.Printf("[Server Mode] ✅ Ready to accept stealth tunnel on wss://%s%s", *listenAddr, *path)
		err := httpServer.ListenAndServeTLS(*certFile, *keyFile)
		log.Fatalf("[Server Mode] HTTPS server failed: %v", err)
	}()

	publicListener, err := net.Listen("tcp", *publicAddr)
	if err != nil {
		log.Fatalf("[Server Mode] Failed to listen on public port %s: %v", *publicAddr, err)
	}
	log.Printf("[Server Mode] ✅ Ready to accept public traffic on %s", *publicAddr)

	for {
		publicConn, err := publicListener.Accept()
		if err != nil {
			log.Printf("[Server Mode] Failed to accept public connection: %v", err)
			continue
		}
		
		go func() {
			defer publicConn.Close()
			if session == nil || session.IsClosed() {
				log.Println("[Server Mode] ❌ Request denied. Tunnel is not active.")
				return
			}
			stream, err := session.OpenStream()
			if err != nil {
				log.Printf("[Server Mode] Failed to open stream: %v", err)
				return
			}
			defer stream.Close()

			log.Printf("[Server Mode] 📥 New request from %s. Forwarding over tunnel.", publicConn.RemoteAddr())
			
			go func() { _, _ = io.Copy(stream, publicConn) }()
			_, _ = io.Copy(publicConn, stream)
			log.Printf("[Server Mode] ... Stream for %s closed.", publicConn.RemoteAddr())
		}()
	}
}

// CLIENT LOGIC
func runClient() {
	clientCmd := flag.NewFlagSet("client", flag.ExitOnError)
	serverURL := clientCmd.String("server", "", "Full server WebSocket URL (e.g., wss://your.ip/connect)")
	localAddr := clientCmd.String("local", "", "Local service address to expose (e.g., localhost:3000)")
	clientCmd.Parse(os.Args[2:])

	if *serverURL == "" || *localAddr == "" {
		log.Println("Error: Both -server and -local flags are required for client mode.")
		clientCmd.Usage()
		return
	}

	for {
		log.Printf("[Client Mode] ... Attempting stealth connection to %s", *serverURL)
		
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		
		wsConn, _, err := websocket.Dial(ctx, *serverURL, &websocket.DialOptions{
			Subprotocols: []string{"tunnel"},
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		})
		cancel()

		if err != nil {
			log.Printf("[Client Mode] ❌ Connection failed: %v. Retrying in 5 seconds...", err)
			time.Sleep(5 * time.Second)
			continue
		}
		
		conn := websocket.NetConn(context.Background(), wsConn, websocket.MessageBinary)
		log.Println("[Client Mode] ✅ Stealth tunnel established!")

		session, err := yamux.Client(conn, nil)
		if err != nil {
			log.Printf("[Client Mode] ❌ Multiplexing failed: %v", err)
			continue
		}

		for {
			stream, err := session.AcceptStream()
			if err != nil {
				log.Printf("[Client Mode] ... Session terminated: %v. Reconnecting...", err)
				break 
			}
			
			go func() {
				defer stream.Close()
				log.Printf("[Client Mode] ... New stream %d received, connecting to local service.", stream.StreamID())
				localConn, err := net.Dial("tcp", *localAddr)
				if err != nil {
					log.Printf("[Client Mode] ❌ Failed to connect to local service %s: %v", *localAddr, err)
					return
				}
				defer localConn.Close()

				go func() { _, _ = io.Copy(localConn, stream) }()
				_, _ = io.Copy(stream, localConn)
				log.Printf("[Client Mode] ... Stream %d finished.", stream.StreamID())
			}()
		}
	}
}
