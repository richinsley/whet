// server.go
package pkg

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/pion/webrtc/v4"
)

// type to represent proxy targets
type ProxyTarget struct {
	Subdomain string
	Address   string
}

type WhetServer struct {
	mut          *sync.Mutex
	Mux          *http.ServeMux
	Http         *http.Server
	Targets      map[string]*ForwardTargetPort
	ServeFolders []string
	ProxyTargets []ProxyTarget
	Detached     bool
	BearerToken  string
	Addr         string
	Listeners    map[string]*WhetListener
	Id           string
}

type WhetListener struct {
	Server    *WhetServer
	ConnsChan chan net.Conn
	isopen    bool
}

func (ws *WhetServer) configureSignalServer() error {
	// Create a reverse proxy handler
	proxyHandler := func(target string) http.Handler {
		return &httputil.ReverseProxy{
			Director: func(req *http.Request) {
				targetURL, _ := url.Parse("http://" + target)
				req.URL.Scheme = targetURL.Scheme
				req.URL.Host = targetURL.Host

				// Remove the first subdomain from the path
				parts := strings.Split(req.URL.Path, "/")
				if len(parts) > 2 {
					req.URL.Path = "/" + strings.Join(parts[2:], "/")
				}

				// Update the Host header
				req.Host = targetURL.Host
			},
		}
	}

	// Handle proxy targets first
	for _, proxy := range ws.ProxyTargets {
		pattern := fmt.Sprintf("/%s/", proxy.Subdomain)
		ws.Mux.Handle(pattern, proxyHandler(proxy.Address))
	}

	// Handle WHET signals
	ws.Mux.HandleFunc("/whet/", func(w http.ResponseWriter, r *http.Request) {
		ws.WhetHandler(w, r)
	})

	// Add catch-all handler last
	ws.Mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "404 page not found", http.StatusNotFound)
	})

	// Set up file servers for each folder in serveFolders
	for _, folderSpec := range ws.ServeFolders {
		parts := strings.Split(folderSpec, "=")
		if len(parts) != 2 {
			log.Printf("Invalid folder specification: %s (expected format: subdomain=/path)", folderSpec)
			continue
		}

		subdomain := strings.Trim(parts[0], "/")
		path := parts[1]

		fs := http.FileServer(http.Dir(path))
		pattern := fmt.Sprintf("/%s/", subdomain)
		ws.Mux.Handle(pattern, http.StripPrefix(pattern, fs))
	}

	return nil
}

func (ws *WhetServer) StartWithListener(listener net.Listener, block bool) error {
	ws.Addr = listener.Addr().String()
	if block {
		ws.Http = &http.Server{
			Handler: ws.Mux,
		}
		return ws.Http.Serve(listener)
		// return http.Serve(listener, ws.Mux)
	} else {
		// we'll use a channel to catch any errors that occur when starting the server
		errChan := make(chan error, 1)

		go func() {
			// Start the server and send any error to the channel
			ws.Http = &http.Server{
				Handler: ws.Mux,
			}
			if err := http.Serve(listener, ws.Mux); err != nil {
				errChan <- err
			}
		}()

		// Wait a short time to catch immediate failures
		select {
		case err := <-errChan:
			return err
		case <-time.After(100 * time.Millisecond):
			return nil
		}
	}
}

func (ws *WhetServer) Close() error {
	// close all the listeners
	for _, listener := range ws.Listeners {
		listener.Close()
	}

	// close the Http server
	ws.Http.Close()

	return nil
}

func (ws *WhetServer) StartWithAddress(serverAddr string, block bool) error {
	ws.Addr = serverAddr
	if block {
		ws.Http = &http.Server{
			Addr:    serverAddr,
			Handler: ws.Mux,
		}
		return ws.Http.ListenAndServe()
	} else {
		// we'll use a channel to catch any errors that occur when starting the server
		errChan := make(chan error, 1)

		go func() {
			// Start the server and send any error to the channel
			ws.Http = &http.Server{
				Addr:    serverAddr,
				Handler: ws.Mux,
			}

			if err := ws.Http.ListenAndServe(); err != nil {
				errChan <- err
			}
		}()

		// Wait a short time to catch immediate failures
		select {
		case err := <-errChan:
			return err
		case <-time.After(100 * time.Millisecond):
			return nil
		}
	}
}

func NewWhetServer(bearerToken string, targets map[string]*ForwardTargetPort, serveFolders []string, proxyTargets []ProxyTarget, detached bool) (*WhetServer, error) {
	mux := http.NewServeMux()
	if targets == nil {
		targets = make(map[string]*ForwardTargetPort)
	}

	if serveFolders == nil {
		serveFolders = make([]string, 0)
	}

	if proxyTargets == nil {
		proxyTargets = make([]ProxyTarget, 0)
	}

	retv := &WhetServer{
		mut:          &sync.Mutex{},
		Mux:          mux,
		Targets:      targets,
		ServeFolders: serveFolders,
		ProxyTargets: proxyTargets,
		Detached:     detached,
		BearerToken:  bearerToken,
		Listeners:    make(map[string]*WhetListener),
	}
	err := retv.configureSignalServer()
	return retv, err
}

func (ws *WhetServer) WhetHandler(w http.ResponseWriter, r *http.Request) {
	// Extract the path suffix after "/whet/"
	whetPath := "/whet/"
	pathSuffix := strings.TrimPrefix(r.URL.Path, whetPath)

	// Set CORS headers for all responses
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Access-Control-Allow-Credentials", "true")

	// Allow the Location  header to be exposed
	w.Header().Set("Access-Control-Expose-Headers", "Location")

	var err error
	if r.Method == "POST" {
		// Check bearer token if set before checking the target to prevent probing
		if ws.BearerToken != "" {
			if r.Header.Get("Authorization") != fmt.Sprintf("Bearer %s", ws.BearerToken) {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}

		// Check if the target exists in the targets map
		// the target may have a hyphen to deliniate the target port in a range
		portoffset := 0

		// try to split the pathSuffix by a hyphen, if there are more than 2 parts, it's not a valid target
		// if there are 2 parts, the second part should be a number
		// if there is only 1 part, it should be a valid target and the portoffset should be 0
		parts := strings.Split(pathSuffix, "-")
		if len(parts) == 2 {
			// try to parse the second part as an integer
			portoffset, err = strconv.Atoi(parts[1])
			if err != nil {
				http.Error(w, "Invalid target", http.StatusBadRequest)
				return
			}
		} else if len(parts) > 2 {
			http.Error(w, "Invalid target", http.StatusBadRequest)
			return
		}

		// check if the target exists in the map and get the target address
		target, ok := ws.Targets[parts[0]]
		if !ok {
			http.Error(w, "Invalid target", http.StatusBadRequest)
			return
		}

		// check if the portoffset is within the range
		if portoffset < 0 || (portoffset != 0 && portoffset >= target.PortCount) {
			http.Error(w, "Invalid target", http.StatusBadRequest)
			return
		}

		// create the target address from the target name and the port offset
		targetAddr := fmt.Sprintf("%s:%d", target.Host, target.StartPort+portoffset)

		originProto := "http://"
		if strings.HasPrefix(r.Proto, "HTTPS") {
			originProto = "https://"
		}

		// build a WebRTC peer connection
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read body", http.StatusBadRequest)
			return
		}

		// create the WebRTC peer connection
		_, peerConnection, err := setupWebRTCConnection(ws.Detached)
		if err != nil {
			http.Error(w, "Failed to create peer connection", http.StatusInternalServerError)
			return
		}

		// our connection object
		c := &Connection{
			peerConnection: peerConnection,
			dataChannel:    nil,
			conn:           nil,
			clientReady:    false,
			sendMoreCh:     make(chan struct{}, 1),
			detached:       ws.Detached,
			bearerToken:    ws.BearerToken,
		}

		var wg sync.WaitGroup
		wg.Add(1)

		// wait group that is signaled when the net.conn is attached to the Connection
		var connWg sync.WaitGroup
		connWg.Add(1)

		// we only support the single port forwading for now, so we'll generate a new random UUID for each request
		distroUUID := uuid.New()

		peerConnection.OnDataChannel(func(dataChannel *webrtc.DataChannel) {
			fmt.Println("New data channel:", dataChannel.Label())

			// handle the data channel opening
			dataChannel.OnOpen(func() {
				// detach the channel if we're in detached mode
				rawDetached, dErr := dataChannel.Detach()
				if dErr != nil {
					panic(dErr)
				}
				c.rawDetached = rawDetached

				// handle the handshake and tcp proxying in a separate goroutine
				go func() {
					// Handshake
					err := handleHandshake(c, true, &wg)
					if err != nil {
						fmt.Printf("Error handling handshake: %v\n", err)
						if c.conn != nil {
							c.conn.Close()
						}
						c.closed = true
					}

					connWg.Wait()
					if c.conn != nil {
						// we have a TCP connection, read from the data channel and write to the TCP connection
						// until the data channel is closed
						defer c.conn.Close()
						buffer := make([]byte, maxBufferSize)
						fmt.Println("Server side data channel opened - receiving data")
						for {
							n, err := c.ReceiveRaw(buffer)
							if n == 0 || err != nil {
								fmt.Println("Connection closed by client")
								break
							}

							// write all the data to the TCP connection
							err = c.SendData(buffer[:n])
							if err != nil {
								fmt.Printf("Error writing to target connection: %v\n", err)
								break
							}
						}
					}
				}()

				fmt.Println("Data channel opened")
				c.dataChannel = dataChannel
				// create the target type
				if target.ForwardTargetType == ForwardTargetTypeTCP {
					// try to open the TCP connection to our target if the target type is TCP
					conn, err := net.Dial("tcp", targetAddr)
					if err != nil {
						if c.rawDetached != nil {
							c.rawDetached.Write([]byte("SERVER_ERROR"))
						}
						fmt.Printf("Error connecting to target: %v\n", err)

						// clean up the connection
						peerConnection.Close()

						return
					} else {
						// we have a connection, store it in the connection object and signal the wait group
						// so the tcp proxying can start
						c.conn = conn
						connWg.Done()

						go func() {
							createServerSideConnection(peerConnection, dataChannel, &wg, c)
						}()
					}
				} else if target.ForwardTargetType == ForwardTargetTypeListener {
					// wait for the handshake that is managed in the above proxy goroutine
					wg.Wait()

					// we need to create a WebRTCConn
					listernconn, _ := ListenerWebRTCConn(c)
					wl := ws.Listeners[parts[0]]
					if wl != nil {
						wl.ConnsChan <- listernconn
					} else {
						http.Error(w, "Listener not found", http.StatusBadRequest)
						fmt.Println("Listener not found")
					}
				}
			})

			// Set bufferedAmountLowThreshold so that we can get notified when
			// we can send more
			dataChannel.SetBufferedAmountLowThreshold(bufferedAmountLowThreshold)

			// This callback is made when the current bufferedAmount becomes lower than the threshold
			dataChannel.OnBufferedAmountLow(func() {
				fmt.Println("Buffered amount low, sending more")
				// Make sure to not block this channel or perform long running operations in this callback
				// This callback is executed by pion/sctp. If this callback is blocking it will stop operations
				select {
				case c.sendMoreCh <- struct{}{}:
				default:
				}
			})
		})

		// set the remote description
		err = peerConnection.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: string(body)})
		if err != nil {
			http.Error(w, "Failed to set remote description", http.StatusInternalServerError)
			return
		}

		// create the answer
		answer, err := peerConnection.CreateAnswer(nil)
		if err != nil {
			http.Error(w, "Failed to create answer", http.StatusInternalServerError)
			return
		}

		// get the channel that is closed when gathering is complete
		gatherComplete := webrtc.GatheringCompletePromise(peerConnection)

		// set the local description
		err = peerConnection.SetLocalDescription(answer)
		if err != nil {
			http.Error(w, "Failed to set local description", http.StatusInternalServerError)
			return
		}

		// wait for the gathering to complete
		<-gatherComplete

		// get the SDP response
		responseSDP := peerConnection.LocalDescription().SDP

		// store the connection in the map
		connectionsLock.Lock()
		OpenConnections[distroUUID.String()] = c
		connectionsLock.Unlock()

		// Before writing the response, set the Location header
		// This is REQUIRED for the http DELETE handler to be called on teardown
		// The connectionID MUST be the last part of the path and SHOULD be a UUID
		location := originProto + r.Host + whetPath + distroUUID.String()
		w.Header().Set("Location", location)

		// write out the SDP response to the client in the response body
		// we set the content type to application/sdp similar to the WHEP spec
		w.Header().Set("Content-Type", "application/sdp")
		w.Write([]byte(responseSDP))
	} else if r.Method == "DELETE" {
		// the pathSuffix will contain the UUID for the distro to remove
		id, err := uuid.Parse(pathSuffix)
		if err == nil {
			var c *Connection = nil
			connectionsLock.Lock()
			c, ok := OpenConnections[id.String()]
			if ok {
				delete(OpenConnections, id.String())
			}
			connectionsLock.Unlock()
			fmt.Println("Deleting peer with ID:", id)

			// stop the peer connection
			if c != nil && c.conn != nil {
				// closing the net.Conn will also close the data channel and the peer connection
				if !c.closed {
					c.conn.Close()
					c.closed = true
				}
			}
		}
	} else if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	} else {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func createServerSideConnection(peer *webrtc.PeerConnection, dataChannel *webrtc.DataChannel, wg *sync.WaitGroup, c *Connection) {
	wg.Wait()

	// The buffer size for reading from the TCP connection should be approximately the same as the data channel buffer size.
	// In webrtc, a message can safely be up to 16KB, so we'll use a buffer size of 16KB for reading from the TCP connection.
	bufferSize := maxBufferSize
	buffer := make([]byte, bufferSize)
	for {
		// read in up to bufferSize bytes from our TCP connection
		n, err := c.conn.Read(buffer)
		if n == 0 {
			fmt.Println("Connection closed by client")
			err = io.EOF
		}

		if err != nil {
			if err != io.EOF {
				fmt.Printf("Error reading from target connection: %v\n", err)
			} else {
				// Wait until the bufferedAmount becomes zero
				// sleep for a short duration to avoid busy waiting
				for dataChannel.BufferedAmount() > 0 {
					time.Sleep(10 * time.Millisecond)
				}
			}
			dataChannel.Close()
			peer.Close()
			return
		}

		// push the data to the data channel
		if c.rawDetached != nil {
			err = c.SendRaw(buffer[:n])
		} else {
			err = dataChannel.Send(buffer[:n])
		}

		if err != nil {
			fmt.Printf("Error sending data over WebRTC: %v\n", err)
			return
		}

		// check if we need to wait before sending more
		if c.dataChannel.BufferedAmount() > MaxBufferedAmount {
			// Wait until the bufferedAmount becomes lower than the threshold
			fmt.Println("Buffered amount too high, waiting")
			<-c.sendMoreCh
		}
	}
}

func (ws *WhetServer) AddListener(targetid string) (*WhetListener, error) {
	ws.mut.Lock()
	defer ws.mut.Unlock()

	// check the mux if the targetid is already in use
	// Check if handler exists
	_, pattern := ws.Mux.Handler(&http.Request{URL: &url.URL{Path: targetid}})

	// If the returned pattern matches your input pattern, a handler exists
	if pattern == "/whet/"+targetid {
		return nil, fmt.Errorf("handler already exists for this path")
	}

	retv := &WhetListener{
		Server:    ws,
		ConnsChan: make(chan net.Conn),
		isopen:    true,
	}

	forwarder := &ForwardTargetPort{
		TargetName:        targetid,
		Host:              "",
		StartPort:         0,
		PortCount:         0,
		ForwardTargetType: ForwardTargetTypeListener,
	}

	// add the forwarder to the server's targets
	ws.Targets[targetid] = forwarder

	// Add the listener to the map
	ws.Listeners[targetid] = retv

	return retv, nil
}

// Accept waits for and returns the next connection to the listener.
func (wl *WhetListener) Accept() (net.Conn, error) {
	retv := <-wl.ConnsChan
	if retv == nil {
		return nil, fmt.Errorf("listener closed")
	}
	return retv, nil
}

// Close closes the listener.
func (wl *WhetListener) Close() error {
	// close the channel so accept will return an error and the listener will be closed
	if wl.isopen {
		close(wl.ConnsChan)
		wl.isopen = false
	}
	return nil
}

// Addr returns the listener's network address.
func (wl *WhetListener) Addr() net.Addr {
	// TODO - this should allow for whet network style addresses
	addr, err := net.ResolveTCPAddr("tcp", wl.Server.Addr)
	if err != nil {
		return nil
	}
	return addr
}
