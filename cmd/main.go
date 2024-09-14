package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/richinsley/whet/pkg"
	"golang.ngrok.com/ngrok"
	"golang.ngrok.com/ngrok/config"
)

var bearerToken = ""

func main() {
	// -serve -server=localhost:9999 -target=localhost:22
	isServer := flag.Bool("serve", false, "Run in server mode")
	isNGROK := flag.Bool("ngrok", false, "Run in ngrok mode")
	serverAddr := flag.String("server", "localhost:8080", "Server address for signaling")
	listenAddr := flag.String("listen", "localhost:8081", "Address to listen on for incoming TCP connections")
	targetAddr := flag.String("target", "localhost:22", "Target address for server-side TCP connections")
	btoken := flag.String("token", "", "Bearer token for authorization")
	if *btoken != "" {
		bearerToken = *btoken
	}

	flag.Parse()
	
	if *isServer {
		if *isNGROK {
			ctx := context.Background()
			runServerNGROK(ctx, *targetAddr)
		} else {
			runServer(*serverAddr, *targetAddr)
		}
	} else {
		runClient(*serverAddr, *listenAddr)
	}
}

func runClient(serverAddr, listenAddr string) {
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

		go pkg.HandleClientConnection(conn, serverAddr, bearerToken)
	}
}

func runServerNGROK(ctx context.Context, targetAddr string) {
	// get ngrok AUTH_TOKEN from env NGROK_AUTHTOKEN
	token := os.Getenv("NGROK_AUTHTOKEN")
	listener, err := ngrok.Listen(ctx,
		config.HTTPEndpoint(),
		ngrok.WithAuthtoken(token),
		//ngrok.WithAuthtokenFromEnv(),
	)

	if err != nil {
		panic(err)
	}

	log.Println("App URL", listener.URL())
	http.HandleFunc("/whep/", func(w http.ResponseWriter, r *http.Request) {
		pkg.WhepHandler(w, r, targetAddr, bearerToken)
	})
	panic(http.Serve(listener, nil))
}

func runServer(serverAddr, targetAddr string) {
	http.HandleFunc("/whep/", func(w http.ResponseWriter, r *http.Request) {
		pkg.WhepHandler(w, r, targetAddr, bearerToken)
	})
	fmt.Printf("WHEP signaling server running on http://%s\n", serverAddr)
	panic(http.ListenAndServe(serverAddr, nil))
}
