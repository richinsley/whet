// server.go
package pkg

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/pion/webrtc/v4"
)

func WhetHandler(w http.ResponseWriter, r *http.Request, targets map[string]*ForwardTargetPort, bearerToken string, detached bool) {
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
		if bearerToken != "" {
			if r.Header.Get("Authorization") != fmt.Sprintf("Bearer %s", bearerToken) {
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
		target, ok := targets[parts[0]]
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
		_, peerConnection, err := setupWebRTCConnection(detached)
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
			detached:       detached,
			bearerToken:    bearerToken,
		}

		var wg sync.WaitGroup
		wg.Add(2)

		// we only support the single port forwading for now, so we'll generate a new random UUID for each request
		distroUUID := uuid.New()

		peerConnection.OnDataChannel(func(dataChannel *webrtc.DataChannel) {
			fmt.Println("New data channel:", dataChannel.Label())

			// handle the data channel opening
			dataChannel.OnOpen(func() {
				// detach the channel if we're in detached mode
				if detached {
					raw, dErr := dataChannel.Detach()
					if dErr != nil {
						panic(dErr)
					}
					c.rawDetached = raw

					go func() {
						// The first message from the client must be the "CLIENT_READY" message
						readymsg := make([]byte, 12)
						n, err := c.rawDetached.Read(readymsg)
						if err != nil {
							fmt.Printf("Error reading from rawDetached: %v\n", err)
							c.conn.Close()
							c.closed = true
							return
						}
						if n != 12 || !bytes.Equal(readymsg, []byte("CLIENT_READY")) {
							fmt.Println("Handshake failed, closing connection")
							c.conn.Close()
							c.closed = true
							return
						}

						c.clientReady = true
						wg.Done()

						defer c.conn.Close()
						buffer := make([]byte, maxBufferSize)
						for {
							n, err := c.ReceiveRaw(buffer)
							if n == 0 || err != nil {
								fmt.Println("Connection closed by client")
								break
							}

							// write all the data to the TCP connection
							err = c.SendTCP(buffer[:n])
							if err != nil {
								fmt.Printf("Error writing to target connection: %v\n", err)
								break
							}
						}
					}()
				}

				fmt.Println("Data channel opened")
				// try to open the TCP connection to our target
				conn, err := net.Dial("tcp", targetAddr)
				if err != nil {
					if c.rawDetached != nil {
						c.rawDetached.Write([]byte("SERVER_ERROR"))
					} else {
						dataChannel.Send([]byte("SERVER_ERROR"))
					}
					fmt.Printf("Error connecting to target: %v\n", err)

					// clean up the connection
					peerConnection.Close()

					return
				} else {
					// Send SERVER_READY as soon as the channel opens
					c.conn = conn
					c.dataChannel = dataChannel
					fmt.Println("Sending SERVER_READY message")
					if c.rawDetached != nil {
						_, err := c.rawDetached.Write([]byte("SERVER_READY"))
						if err != nil {
							fmt.Printf("Error sending SERVER_READY: %v\n", err)
						}
					} else {
						err := dataChannel.Send([]byte("SERVER_READY"))
						if err != nil {
							fmt.Printf("Error sending SERVER_READY: %v\n", err)
						}
					}
					wg.Done()

					go func() {
						createServerSideConnection(peerConnection, dataChannel, &wg, c)
					}()
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

			if !detached {
				// handle the data channel messages
				// Server side WebRTC to TCP proxy (input from WebRTC, output to local TCP)
				dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
					if !c.clientReady {
						// The first message from the client must be the "CLIENT_READY" message
						if bytes.Equal(msg.Data, []byte("CLIENT_READY")) {
							fmt.Println("received CLIENT_READY message")
							c.clientReady = true
							wg.Done()
							return
						} else {
							// handshake failed, close the connection
							fmt.Println("Handshake failed, closing connection")
							c.conn.Close()
							c.closed = true
							return
						}
					}

					_, err := c.conn.Write(msg.Data)
					if err != nil {
						fmt.Printf("Error writing to target connection: %v\n", err)
					}
				})
			}
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
