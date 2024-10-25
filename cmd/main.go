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
	"sync"

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
	isNGROK := flag.Bool("ngrok", false, "Run in ngrok server mode")
	serverAddr := flag.String("server", "localhost:8080", "Server address for signaling")
	// listenAddr := flag.String("listen", "localhost:8081", "Address to listen on for incoming TCP connections")
	btoken := flag.String("token", "", "Bearer token for authorization")
	detached := flag.Bool("detached", false, "Run in detached mode")

	var tcplisteners targetAddrList
	flag.Var(&tcplisteners, "tcplisten", "Address to listen on for incoming TCP connections(can specify multiple)")

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
		// parse the listener addresses
		if len(tcplisteners) == 0 {
			log.Fatal("No listener addresses specified")
		}
		listeners, err := pkg.ParseListenTargetPortsFromStringSlice(tcplisteners)
		if err != nil {
			log.Fatalf("Failed to parse forward target addresses: %v", err)
		}
		runClient(*serverAddr, listeners, *detached)
	}
}

func runClient(whetServerAddr string, listeners map[string]*pkg.ListenTargetPort, detached bool) {

	// we'll use a channel to wait for all listeners to initialize
	var wg sync.WaitGroup
	wg.Add(len(listeners))
	for _, listener := range listeners {
		// start a goroutine for each listener
		go func() {
			localaddr := fmt.Sprintf("%s:%d", listener.LocalHost, listener.LocalPort)
			lsocket, err := net.Listen("tcp", localaddr)
			if err != nil {
				panic(err)
			}
			defer lsocket.Close()

			fmt.Printf("Listening for TCP connections on %s\n", localaddr)
			// signal the wait group that we're ready
			wg.Done()

			// continue accepting connections until the program is terminated
			for {
				conn, err := lsocket.Accept()
				if err != nil {
					fmt.Printf("Error accepting connection: %v\n", err)
					continue
				}

				go pkg.HandleClientConnection(conn, whetServerAddr, listener.TargetName, bearerToken, detached)
			}
		}()
	}

	// wait for all listeners to initialize
	wg.Wait()

	fmt.Println("WHET client running")

	select {}
}

func runServerNGROK(ctx context.Context, targets map[string]*pkg.ForwardTargetPort, detached bool) {
	// get ngrok AUTH_TOKEN and NGROK_DOMAIN from env NGROK_AUTHTOKEN (if you have a domain)
	token := os.Getenv("NGROK_AUTHTOKEN")
	domain := os.Getenv("NGROK_DOMAIN")
	var conf config.Tunnel = nil
	// cors := true

	// create the ngrok options
	options := make([]config.HTTPEndpointOption, 0)

	if domain != "" {
		options = append(options, config.WithDomain(domain))
	}

	conf = config.HTTPEndpoint(options...)

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
