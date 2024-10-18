package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/richinsley/whet/pkg"
	"golang.ngrok.com/ngrok"
	"golang.ngrok.com/ngrok/config"
)

var bearerToken = ""

// Custom type to hold multiple (tcp/udp)target addresses
type targetAddrList []string

// Implement the Set method for targetAddrList to satisfy the flag.Value interface
func (t *targetAddrList) Set(value string) error {
	*t = append(*t, value)
	return nil
}

// Implement the String method for targetAddrList to satisfy the flag.Value interface
func (t *targetAddrList) String() string {
	return strings.Join(*t, ", ")
}

func main() {
	// -serve -server=localhost:9999 -target=localhost:22
	isServer := flag.Bool("serve", false, "Run in server mode")
	isNGROK := flag.Bool("ngrok", false, "Run in ngrok mode")
	serverAddr := flag.String("server", "localhost:8080", "Server address for signaling")
	listenAddr := flag.String("listen", "localhost:8081", "Address to listen on for incoming TCP connections")
	// targetAddr := flag.String("tcptarget", "localhost:22", "Target address for server-side TCP connections")
	btoken := flag.String("token", "", "Bearer token for authorization")
	detached := flag.Bool("detached", false, "Run in detached mode")

	var tcptargets targetAddrList
	flag.Var(&tcptargets, "tcptarget", "Target address for server-side TCP connections (can specify multiple)")

	if *btoken != "" {
		bearerToken = *btoken
	}

	flag.Parse()

	if *isServer || *isNGROK {
		// parse the forward target addresses
		if len(tcptargets) == 0 {
			log.Fatal("No forward target addresses specified")
		}
		targets, err := pkg.ParseForwardTargetPortsFromStringSlice(tcptargets)
		if err != nil {
			log.Fatalf("Failed to parse forward target addresses: %v", err)
		}

		if *isNGROK {
			ctx := context.Background()
			runServerNGROK(ctx, targets, *detached)
		} else {
			runServer(*serverAddr, targets, *detached)
		}
	} else {
		runClient(*serverAddr, *listenAddr, *detached)
	}
}

func runClient(serverAddr, listenAddr string, detached bool) {
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		panic(err)
	}
	defer listener.Close()

	fmt.Printf("Listening for TCP connections on %s\n", listenAddr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Printf("Error accepting connection: %v\n", err)
			continue
		}

		go pkg.HandleClientConnection(conn, serverAddr, bearerToken, detached)
	}
}

func runServerNGROK(ctx context.Context, targets map[string]*pkg.ForwardTargetPort, detached bool) {
	// get ngrok AUTH_TOKEN and NGROK_DOMAIN from env NGROK_AUTHTOKEN (if you have a domain)
	token := os.Getenv("NGROK_AUTHTOKEN")
	domain := os.Getenv("NGROK_DOMAIN")
	var conf config.Tunnel = nil
	if domain != "" {
		conf = config.HTTPEndpoint(
			config.WithDomain(domain),
		)
	} else {
		conf = config.HTTPEndpoint()
	}

	listener, err := ngrok.Listen(ctx,
		conf,
		ngrok.WithAuthtoken(token),
	)

	if err != nil {
		panic(err)
	}

	log.Println("App URL", listener.URL())
	http.HandleFunc("/whet/", func(w http.ResponseWriter, r *http.Request) {
		pkg.WhetHandler(w, r, targets, bearerToken, detached)
	})
	panic(http.Serve(listener, nil))
}

func runServer(serverAddr string, targets map[string]*pkg.ForwardTargetPort, detached bool) {
	http.HandleFunc("/whet/", func(w http.ResponseWriter, r *http.Request) {
		pkg.WhetHandler(w, r, targets, bearerToken, detached)
	})
	fmt.Printf("WHET signaling server running on http://%s\n", serverAddr)
	panic(http.ListenAndServe(serverAddr, nil))
}
