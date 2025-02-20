package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/google/uuid"
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

// Custom type to hold multiple folder paths with subdomains
type serveFolderList []string

// Implement the Set method for serveFolderList to satisfy the flag.Value interface
func (t *serveFolderList) Set(value string) error {
	*t = append(*t, value)
	return nil
}

// Implement the String method for serveFolderList to satisfy the flag.Value interface
func (t *serveFolderList) String() string {
	return strings.Join(*t, ", ")
}

// Custom type to hold multiple proxy targets
type proxyTargetList []pkg.ProxyTarget

func (p *proxyTargetList) Set(value string) error {
	parts := strings.Split(value, "=")
	if len(parts) != 2 {
		return fmt.Errorf("invalid proxy target format: %s (expected format: subdomain=address:port)", value)
	}
	*p = append(*p, pkg.ProxyTarget{
		Subdomain: strings.Trim(parts[0], "/"),
		Address:   parts[1],
	})
	return nil
}

func (p *proxyTargetList) String() string {
	var strs []string
	for _, target := range *p {
		strs = append(strs, fmt.Sprintf("%s=%s", target.Subdomain, target.Address))
	}
	return strings.Join(strs, ", ")
}

func main() {
	// -serve -server=localhost:9999 -target=localhost:22
	isServer := flag.Bool("serve", false, "Run in server mode")
	isNGROK := flag.Bool("ngrok", false, "Run in ngrok server mode")
	serverAddr := flag.String("server", "localhost:8080", "Server address for signaling")
	btoken := flag.String("token", "", "Bearer token for authorization")
	gtoken := flag.Bool("gentoken", false, "Generate a new bearer token")
	detached := flag.Bool("detached", false, "Run in detached mode")
	sserve := flag.String("mirror", "", "Simple mirror server address (for testing)")

	var tcplisteners targetAddrList
	flag.Var(&tcplisteners, "tcplisten", "Address to listen on for incoming TCP connections(can specify multiple)")

	var tcptargets targetAddrList
	flag.Var(&tcptargets, "tcptarget", "Target address for server-side TCP connections (can specify multiple)")

	var serveFolders serveFolderList
	flag.Var(&serveFolders, "servefolder", "Folder path(s) to serve in the form subdomain=/absolute/path (can specify multiple)")

	var proxyTargets proxyTargetList
	flag.Var(&proxyTargets, "proxytarget", "Proxy target in the form subdomain=address:port (can specify multiple)")

	if *gtoken {
		// generate a new bearer token.  We'll use a random UUID for now
		bearerToken = uuid.New().String()
		fmt.Printf("Generated new bearer token: %s\n", bearerToken)
	} else if *btoken != "" {
		bearerToken = *btoken
	}

	flag.Parse()

	if *sserve != "" {
		go pkg.SimpleMirrorServer(*sserve)
	}

	if *isServer || *isNGROK {
		// parse the forward target addresses
		if len(tcptargets) == 0 && len(proxyTargets) == 0 && len(serveFolders) == 0 {
			log.Fatal("No server targets specified")
		}
		targets, err := pkg.ParseForwardTargetPortsFromStringSlice(tcptargets)
		if err != nil {
			log.Fatalf("Failed to parse forward target addresses: %v", err)
		}

		if *isNGROK {
			ctx := context.Background()
			runServerNGROK(ctx, targets, serveFolders, proxyTargets, *detached)
		} else {
			runServer(*serverAddr, targets, serveFolders, proxyTargets, *detached)
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

func runServerNGROK(ctx context.Context, targets map[string]*pkg.ForwardTargetPort, serveFolders []string, proxyTargets []pkg.ProxyTarget, detached bool) {
	token := os.Getenv("NGROK_AUTHTOKEN")
	domain := os.Getenv("NGROK_DOMAIN")
	var conf config.Tunnel = nil

	options := make([]config.HTTPEndpointOption, 0)
	if domain != "" {
		options = append(options, config.WithDomain(domain), config.WithScheme("https"))
	}
	conf = config.HTTPEndpoint(options...)

	listener, err := ngrok.Listen(ctx,
		conf,
		ngrok.WithAuthtoken(token),
	)
	if err != nil {
		panic(err)
	}

	s, err := pkg.NewWhetServer(bearerToken, targets, serveFolders, proxyTargets, detached)
	if err != nil {
		log.Fatalf("Failed to create WHET server: %v", err)
	}
	err = s.StartWithListener(listener, true)
	if err != nil {
		log.Fatalf("Failed to start WHET server: %v", err)
	}
}

func runServer(serverAddr string, targets map[string]*pkg.ForwardTargetPort, serveFolders []string, proxyTargets []pkg.ProxyTarget, detached bool) {
	// create the regular HTTP server
	s, err := pkg.NewWhetServer(bearerToken, targets, serveFolders, proxyTargets, detached)
	if err != nil {
		log.Fatalf("Failed to create WHET server: %v", err)
	}

	err = s.StartWithAddress(serverAddr, true)
	if err != nil {
		log.Fatalf("Failed to start WHET server: %v", err)
	}
}
