package pkg

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/pion/webrtc/v4"
)

func WhepHandler(w http.ResponseWriter, r *http.Request, targetAddr string, bearerToken string) {
	// Extract the path suffix after "/whep/"
	whepPath := "/whep/"
	pathSuffix := strings.TrimPrefix(r.URL.Path, whepPath)

	if r.Method == "POST" {
		// Check bearer token if set
		if bearerToken != "" {
			if r.Header.Get("Authorization") != fmt.Sprintf("Bearer %s", bearerToken) {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}

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

		// get the defaul peer connection config
		defaultPeerConnectionConfig := DefaultPeerConnectionConfig()

		// create a new peer connection
		peerConnection, err := webrtc.NewPeerConnection(defaultPeerConnectionConfig)
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
		}

		var wg sync.WaitGroup
		wg.Add(2)

		// we only support the single port forwading for now, so we'll generate a new random UUID for each request
		distroUUID := uuid.New()

		peerConnection.OnDataChannel(func(dataChannel *webrtc.DataChannel) {
			fmt.Println("New data channel:", dataChannel.Label())

			// handle the data channel opening
			dataChannel.OnOpen(func() {
				fmt.Println("Data channel opened")
				// try to open the TCP connection to our target
				conn, err := net.Dial("tcp", targetAddr)
				if err != nil {
					dataChannel.Send([]byte("SERVER_ERROR"))
					fmt.Printf("Error connecting to target: %v\n", err)

					// clean up the connection
					peerConnection.Close()

					return
				} else {
					// Send SERVER_READY as soon as the channel opens
					c.conn = conn
					c.dataChannel = dataChannel
					fmt.Println("Sending SERVER_READY message")
					err := dataChannel.Send([]byte("SERVER_READY"))
					if err != nil {
						fmt.Printf("Error sending SERVER_READY: %v\n", err)
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
						return
					}
				}

				_, err := c.conn.Write(msg.Data)
				if err != nil {
					fmt.Printf("Error writing to target connection: %v\n", err)
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
		location := originProto + r.Host + whepPath + distroUUID.String()
		w.Header().Set("Location", location)
		w.Header().Set("Connection-ID", distroUUID.String())

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
			if c != nil {
				// closing the net.Conn will also close the data channel and the peer connection
				c.conn.Close()
			}
		}
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
			}
			// Wait until the bufferedAmount becomes zero
			// sleep for a short duration to avoid busy waiting
			for dataChannel.BufferedAmount() > 0 {
				time.Sleep(10 * time.Millisecond)
			}
			dataChannel.Close()
			peer.Close()
			return
		}

		// fmt.Printf("Read %d bytes from target\n", n)

		// push the data to the data channel
		err = dataChannel.Send(buffer[:n])
		if err != nil {
			fmt.Printf("Error sending data over WebRTC: %v\n", err)
			return
		}

		// check if we can send more
		if c.dataChannel.BufferedAmount() > maxBufferedAmount {
			// Wait until the bufferedAmount becomes lower than the threshold
			fmt.Println("Buffered amount too high, waiting")
			<-c.sendMoreCh
		}
	}
}
