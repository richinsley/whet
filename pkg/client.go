package pkg

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/pion/webrtc/v3"
)

var dataChannelConfig = &webrtc.DataChannelInit{
	// ensures that data messages are delivered in the order they were sent
	Ordered: &[]bool{true}[0],
	// ensures that data messages are delivered exactly once
	// MaxRetransmits: &[]uint16{0}[0],
}

func HandleClientConnection(conn net.Conn, endpoint string, bearerToken string) {
	defer conn.Close()

	peerConnectionConfig := DefaultPeerConnectionConfig()
	peerConnection, err := webrtc.NewPeerConnection(peerConnectionConfig)
	if err != nil {
		fmt.Printf("Failed to create peer connection: %v\n", err)
		return
	}
	defer peerConnection.Close()

	dataChannel, err := peerConnection.CreateDataChannel("data", dataChannelConfig)
	if err != nil {
		fmt.Printf("Failed to create data channel: %v\n", err)
		return
	}

	// wait for both the data channel to open and the server handshake to complete
	var wg sync.WaitGroup
	wg.Add(2)

	dataChannel.OnOpen(func() {
		fmt.Println("Data channel opened")
		wg.Done()
	})

	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		fmt.Printf("Failed to create offer: %v\n", err)
		return
	}

	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)

	err = peerConnection.SetLocalDescription(offer)
	if err != nil {
		fmt.Printf("Failed to set local description: %v\n", err)
		return
	}

	<-gatherComplete

	offerString := peerConnection.LocalDescription().SDP

	fmt.Println(offerString)

	if strings.HasPrefix(endpoint, "http") {
		endpoint = fmt.Sprintf("%s/whep/tunnel", endpoint)
	} else {
		endpoint = fmt.Sprintf("http://%s/whep/tunnel", endpoint)
	}

	// post the request to the whep server
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}

	fmt.Printf("WHEP client using endpoint%s\n", endpoint)
	req, err := http.NewRequest("POST", endpoint, bytes.NewBuffer([]byte(offerString)))
	if err != nil {
		return
	}

	req.Header.Add("Content-Type", "application/sdp")
	if bearerToken != "" {
		req.Header.Add("Authorization", "Bearer "+bearerToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		fmt.Println(err.Error())
		return
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
		// return nil, nil, err
	}

	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		return
		// return nil, nil, fmt.Errorf(fmt.Sprintf("non successful POST: %d", resp.StatusCode))
	}

	resourceUrl, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		return
		// return nil, nil, err
	}
	base, err := url.Parse(endpoint)
	if err != nil {
		return
		// return nil, nil, err
	}

	// Get the connection ID
	connectionID := resp.Header.Get("Connection-ID")
	if connectionID == "" {
		fmt.Println("Connection-ID not found in response")
		return
	}

	answer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  string(body),
	}

	err = peerConnection.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.SDP})
	if err != nil {
		fmt.Printf("Failed to set remote description: %v\n", err)
		return
	}

	// store the connection in the map
	c := &Connection{
		peerConnection: peerConnection,
		dataChannel:    dataChannel,
		conn:           conn,
		resourceURL:    base.ResolveReference(resourceUrl).String(),
		clientReady:    false,
	}

	// Client side WebRTC to TCP proxy (input from WebRTC, output to local TCP)
	dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		if !c.clientReady {
			// The first message must be the "SERVER_READY" message
			if bytes.Equal(msg.Data, []byte("SERVER_READY")) {
				fmt.Println("Received SERVER_READY, sending CLIENT_READY")
				dataChannel.Send([]byte("CLIENT_READY"))
				c.clientReady = true
				wg.Done()
				return
			} else {
				// handshake failed, close the connection
				fmt.Println("Handshake failed, closing connection")
				conn.Close()
				return
			}
		}
		_, err := conn.Write(msg.Data)
		if err != nil {
			fmt.Printf("Error writing to TCP connection: %v\n", err)
		}
	})

	connectionsLock.Lock()
	OpenConnections[connectionID] = c
	connectionsLock.Unlock()

	fmt.Println("WebRTC connection established, starting to proxy data")

	// Proxy data from TCP to WebRTC
	go func() {
		// Wait for data channel to open and the handshake to complete before sending data
		wg.Wait()

		// The buffer size for reading from the TCP connection should be approximately the same as the data channel buffer size.
		// In webrtc, a message can safely be up to 16KB, so we'll use a buffer size of 16KB for reading from the TCP connection.
		bufferSize := 8 * 1024
		buffer := make([]byte, bufferSize)
		for {
			n, err := conn.Read(buffer)
			if n == 0 {
				fmt.Println("Connection closed by client")
				err = io.EOF
			}
			if err != nil {
				if err != io.EOF {
					fmt.Printf("Error reading from TCP connection: %v\n", err)
				}
				// close the ice connection
				c.peerConnection.Close()
				// call the "DELETE" on the host ResourceUrl if one was provided
				if c.resourceURL != "" {
					req, err := http.NewRequest("DELETE", c.resourceURL, nil)
					if err != nil {
						log.Fatal("Unexpected error building http request. ", err)
					}
					if bearerToken != "" {
						req.Header.Add("Authorization", "Bearer "+bearerToken)
					}

					client := &http.Client{
						Transport: &http.Transport{
							TLSClientConfig: &tls.Config{
								InsecureSkipVerify: true,
							},
						},
					}
					_, err = client.Do(req)
					if err != nil {
						fmt.Printf("Failed http DELETE request: %s\n", err)
					}
				}
				return
			}
			err = dataChannel.Send(buffer[:n])
			if err != nil {
				fmt.Printf("Error sending data: %v\n", err)
				return
			}
		}
	}()

	select {}
}
