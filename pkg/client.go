package pkg

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

var dataChannelConfig = &webrtc.DataChannelInit{
	// ensures that data messages are delivered in the order they were sent
	Ordered: &[]bool{true}[0],
	// ensures that data messages are delivered exactly once
	// MaxRetransmits: &[]uint16{0}[0],
}

func HandleClientConnection(conn net.Conn, signalServer string, targetName string, bearerToken string, detached bool) {
	defer conn.Close()

	// Create a SettingEngine and enable Detach
	s := webrtc.SettingEngine{}
	if detached {
		s.DetachDataChannels()
	}

	// Create an API object with the engine
	api := webrtc.NewAPI(webrtc.WithSettingEngine(s))

	peerConnectionConfig := DefaultPeerConnectionConfig()
	peerConnection, err := api.NewPeerConnection(peerConnectionConfig)
	if err != nil {
		fmt.Printf("Failed to create peer connection: %v\n", err)
		return
	}
	defer peerConnection.Close()
	done := make(chan struct{})

	dataChannel, err := peerConnection.CreateDataChannel("data", dataChannelConfig)
	if err != nil {
		fmt.Printf("Failed to create data channel: %v\n", err)
		return
	}

	// out channel object
	c := &Connection{}
	c.sendMoreCh = make(chan struct{}, 1)
	c.detached = detached
	c.bearerToken = bearerToken
	c.closed = false

	// wait for both the data channel to open and the server handshake to complete
	var wg sync.WaitGroup
	wg.Add(2)

	dataChannel.OnOpen(func() {
		if detached {
			rawDetached, err := dataChannel.Detach()
			if err != nil {
				fmt.Printf("Failed to detach data channel: %v\n", err)
				return
			}
			c.rawDetached = rawDetached
		}

		fmt.Println("Data channel opened")
		wg.Done()

		go func() {
			readybuf := make([]byte, 12)
			_, err := c.ReceiveRaw(readybuf)
			if err != nil {
				fmt.Printf("Error receiving ready message: %v\n", err)
				return
			}

			// The first message must be the "SERVER_READY" message
			if bytes.Equal(readybuf, []byte("SERVER_READY")) {
				fmt.Println("Received SERVER_READY, sending CLIENT_READY")
				c.SendRaw([]byte("CLIENT_READY"))
				c.clientReady = true
				wg.Done()
			} else {
				// handshake failed, close the connection
				fmt.Println("Handshake failed, closing connection")
				conn.Close()
				return
			}

			bufferSize := maxBufferSize
			buffer := make([]byte, bufferSize)
			for {
				n, err := c.ReceiveRaw(buffer)
				if n == 0 || err != nil {
					fmt.Println("Connection closed by client")
					break
				}

				_, err = conn.Write(buffer[:n])
				if err != nil {
					fmt.Printf("Error writing to TCP connection: %v\n", err)
					break
				}
			}
		}()
	})

	// Set bufferedAmountLowThreshold so that we can get notified when
	// we can send more
	dataChannel.SetBufferedAmountLowThreshold(bufferedAmountLowThreshold)

	// This callback is made when the current bufferedAmount becomes lower than the threshold
	dataChannel.OnBufferedAmountLow(func() {
		// fmt.Println("Buffered amount low, sending more")
		// Make sure to not block this channel or perform long running operations in this callback
		// This callback is executed by pion/sctp. If this callback is blocking it will stop operations
		select {
		case c.sendMoreCh <- struct{}{}:
		default:
		}
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

	if strings.HasPrefix(signalServer, "http") {
		signalServer = fmt.Sprintf("%s/whet/%s", signalServer, targetName)
	} else {
		signalServer = fmt.Sprintf("http://%s/whet/%s", signalServer, targetName)
	}

	// post the request to the whet server
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}

	fmt.Printf("WHET client using endpoint%s\n", signalServer)
	req, err := http.NewRequest("POST", signalServer, bytes.NewBuffer([]byte(offerString)))
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
	base, err := url.Parse(signalServer)
	if err != nil {
		return
		// return nil, nil, err
	}

	// Get the connection ID from the resource URL
	connectionID := resourceUrl.Path[strings.LastIndex(resourceUrl.Path, "/")+1:]

	// // Get the connection ID
	// connectionID := resp.Header.Get("Connection-ID")
	// if connectionID == "" {
	// 	fmt.Println("Connection-ID not found in response")
	// 	return
	// }

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
	c.peerConnection = peerConnection
	c.dataChannel = dataChannel
	c.conn = conn
	c.resourceURL = base.ResolveReference(resourceUrl).String()
	c.clientReady = false

	// Client side WebRTC to TCP proxy (input from WebRTC, output to local TCP)
	if !detached {
		dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
			if !c.clientReady {
				// The first message must be the "SERVER_READY" message
				if bytes.Equal(msg.Data, []byte("SERVER_READY")) {
					fmt.Println("Received SERVER_READY, sending CLIENT_READY")
					dataChannel.Send([]byte("CLIENT_READY"))
					c.clientReady = true
					wg.Done()
					done <- struct{}{}
					return
				} else {
					// handshake failed, close the connection
					fmt.Println("Handshake failed, closing connection")
					conn.Close()
					done <- struct{}{}
					return
				}
			}
			_, err := conn.Write(msg.Data)
			if err != nil {
				fmt.Printf("Error writing to TCP connection: %v\n", err)
			}
		})
	}
	connectionsLock.Lock()
	OpenConnections[connectionID] = c
	connectionsLock.Unlock()

	fmt.Println("WebRTC connection established, starting to proxy data")

	// Proxy data from TCP to WebRTC
	go func() {
		// Wait for data channel to open and the handshake to complete before sending data
		wg.Wait()

		bufferSize := maxBufferSize
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
				} else {
					// Wait until the bufferedAmount becomes zero
					// sleep for a short duration to avoid busy waiting
					for dataChannel.BufferedAmount() > 0 {
						time.Sleep(10 * time.Millisecond)
					}
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
				done <- struct{}{}
				return
			}
			if !c.detached {
				err = dataChannel.Send(buffer[:n])
				if err != nil {
					fmt.Printf("Error sending data: %v\n", err)
					done <- struct{}{}
					return
				}
			} else {
				err = c.SendRaw(buffer[:n])
				if err != nil {
					fmt.Printf("Error sending raw data: %v\n", err)
					done <- struct{}{}
					return
				}
			}

			// check if we can send more
			if dataChannel.BufferedAmount() > MaxBufferedAmount {
				// Wait until the bufferedAmount becomes lower than the threshold
				// fmt.Println("Buffered amount too high, waiting")
				<-c.sendMoreCh
			}
		}
	}()

	<-done
	fmt.Println("Connection closed")
}

func DialClientConnection(signalServer string, targetName string, bearerToken string) (*Connection, error) {
	// Create a SettingEngine and enable Detach
	detached := true
	errstr := ""

	s := webrtc.SettingEngine{}
	if detached {
		s.DetachDataChannels()
	}

	// Create an API object with the engine
	api := webrtc.NewAPI(webrtc.WithSettingEngine(s))

	peerConnectionConfig := DefaultPeerConnectionConfig()
	peerConnection, err := api.NewPeerConnection(peerConnectionConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create peer connection: %v", err)
	}
	// defer peerConnection.Close()

	dataChannel, err := peerConnection.CreateDataChannel("data", dataChannelConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create data channel: %v", err)
	}

	// out channel object
	c := &Connection{}
	c.sendMoreCh = make(chan struct{}, 1)
	c.detached = detached
	c.bearerToken = bearerToken

	// wait for both the data channel to open and the server handshake to complete
	var wg sync.WaitGroup
	wg.Add(1)

	dataChannel.OnOpen(func() {
		defer wg.Done()
		if detached {
			rawDetached, err := dataChannel.Detach()
			if err != nil {
				errstr = fmt.Sprintf("failed to detach data channel: %v\n", err)
				return
			}
			c.rawDetached = rawDetached
		}

		// go func() {
		readybuf := make([]byte, 12)
		_, err := c.ReceiveRaw(readybuf)
		if err != nil {
			errstr = fmt.Sprintf("error receiving ready message: %v\n", err)
			return
		}

		// The first message must be the "SERVER_READY" message
		if bytes.Equal(readybuf, []byte("SERVER_READY")) {
			fmt.Println("Received SERVER_READY, sending CLIENT_READY")
			c.SendRaw([]byte("CLIENT_READY"))
			c.clientReady = true
		} else {
			// handshake failed, close the connection
			errstr = "handshake failed, closing connection"
			return
		}
	})

	// Set bufferedAmountLowThreshold so that we can get notified when
	// we can send more
	dataChannel.SetBufferedAmountLowThreshold(bufferedAmountLowThreshold)

	// This callback is made when the current bufferedAmount becomes lower than the threshold
	dataChannel.OnBufferedAmountLow(func() {
		// fmt.Println("Buffered amount low, sending more")
		// Make sure to not block this channel or perform long running operations in this callback
		// This callback is executed by pion/sctp. If this callback is blocking it will stop operations
		select {
		case c.sendMoreCh <- struct{}{}:
		default:
		}
	})

	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		return nil, err
	}

	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)

	err = peerConnection.SetLocalDescription(offer)
	if err != nil {
		return nil, err
	}

	<-gatherComplete

	offerString := peerConnection.LocalDescription().SDP

	fmt.Println(offerString)

	if strings.HasPrefix(signalServer, "http") {
		signalServer = fmt.Sprintf("%s/whet/%s", signalServer, targetName)
	} else {
		signalServer = fmt.Sprintf("http://%s/whet/%s", signalServer, targetName)
	}

	// post the request to the whet server
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}

	fmt.Printf("WHET client using endpoint%s\n", signalServer)
	req, err := http.NewRequest("POST", signalServer, bytes.NewBuffer([]byte(offerString)))
	if err != nil {
		return nil, err
	}

	req.Header.Add("Content-Type", "application/sdp")
	if bearerToken != "" {
		req.Header.Add("Authorization", "Bearer "+bearerToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		return nil, fmt.Errorf("non successful POST: %d", resp.StatusCode)
	}

	// location provides the resource URL that is used to manage the connection
	// the last part of the URL is the connection ID
	location := resp.Header.Get("Location")
	if location == "" {
		return nil, fmt.Errorf("location not found in response")
	}

	resourceUrl, err := url.Parse(location)
	log.Default().Println("Resource URL: ", resourceUrl)
	fmt.Printf("Resource URL %s\n", resourceUrl)
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(signalServer)
	if err != nil {
		return nil, err
	}

	// Get the connection ID from the resource URL
	connectionID := resourceUrl.Path[strings.LastIndex(resourceUrl.Path, "/")+1:]

	// connectionID := resp.Header.Get("Connection-ID")
	// if connectionID == "" {
	// 	return nil, fmt.Errorf("Connection-ID not found in response: %s", resourceUrl)
	// }

	answer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  string(body),
	}

	err = peerConnection.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.SDP})
	if err != nil {
		return nil, fmt.Errorf("failed to set remote description: %v", err)
	}

	// store the connection in the map
	c.peerConnection = peerConnection
	c.dataChannel = dataChannel
	c.conn = nil
	c.resourceURL = base.ResolveReference(resourceUrl).String()
	c.clientReady = false

	connectionsLock.Lock()
	defer connectionsLock.Unlock()
	OpenConnections[connectionID] = c

	// wait for the connection handshake to complete
	wg.Wait()

	if errstr != "" {
		return nil, errors.New(errstr)
	}

	return c, nil
}
