// proxy.go
package pkg

// import (
// 	"fmt"
// 	"io"
// 	"time"

// 	"github.com/pion/webrtc/v4"
// )

// type ProxyDirection int

// const (
// 	ClientToServer ProxyDirection = iota
// 	ServerToClient
// )

// // startProxy initializes bidirectional proxying between WebRTC and TCP
// // It returns two channels - one for handling errors and one for done signals
// func startProxy(conn *Connection) (chan error, chan struct{}) {
// 	errCh := make(chan error, 2) // Buffer for both directions
// 	doneCh := make(chan struct{})

// 	// Start TCP -> WebRTC proxy
// 	go proxyTCPToWebRTC(conn, errCh)

// 	// Start WebRTC -> TCP proxy
// 	go proxyWebRTCToTCP(conn, errCh)

// 	// Start error handler
// 	go handleProxyErrors(conn, errCh, doneCh)

// 	return errCh, doneCh
// }

// func proxyTCPToWebRTC(conn *Connection, errCh chan error) {
// 	buffer := make([]byte, maxBufferSize)
// 	for {
// 		n, err := conn.conn.Read(buffer)
// 		if shouldStopProxy(n, err) {
// 			errCh <- fmt.Errorf("TCP read: %v", err)
// 			return
// 		}

// 		// Wait if buffered amount is too high
// 		if conn.dataChannel.BufferedAmount() > MaxBufferedAmount {
// 			select {
// 			case <-conn.sendMoreCh:
// 			case <-time.After(5 * time.Second):
// 				errCh <- fmt.Errorf("buffer full timeout")
// 				return
// 			}
// 		}

// 		// Send data over WebRTC
// 		var sendErr error
// 		if conn.detached {
// 			sendErr = conn.SendRawDataChannel(buffer[:n])
// 		} else {
// 			sendErr = conn.dataChannel.Send(buffer[:n])
// 		}

// 		if sendErr != nil {
// 			errCh <- fmt.Errorf("WebRTC send: %v", sendErr)
// 			return
// 		}
// 	}
// }

// func proxyWebRTCToTCP(conn *Connection, errCh chan error) {
// 	// For detached mode, we handle reading directly
// 	if conn.detached {
// 		buffer := make([]byte, maxBufferSize)
// 		for {
// 			n, err := conn.ReceiveRaw(buffer)
// 			if shouldStopProxy(n, err) {
// 				errCh <- fmt.Errorf("WebRTC read: %v", err)
// 				return
// 			}

// 			if err := writeTCP(conn, buffer[:n]); err != nil {
// 				errCh <- fmt.Errorf("TCP write: %v", err)
// 				return
// 			}
// 		}
// 	}
// 	// For non-detached mode, data handling is done in OnMessage callback
// 	// which is set up elsewhere
// }

// func writeTCP(conn *Connection, data []byte) error {
// 	written := 0
// 	for written < len(data) {
// 		n, err := conn.conn.Write(data[written:])
// 		if err != nil {
// 			return err
// 		}
// 		written += n
// 	}
// 	return nil
// }

// func shouldStopProxy(n int, err error) bool {
// 	return n == 0 || err != nil
// }

// func handleProxyErrors(conn *Connection, errCh chan error, doneCh chan struct{}) {
// 	var err error
// 	select {
// 	case err = <-errCh:
// 		// Log the first error that occurs
// 		if err != io.EOF {
// 			fmt.Printf("Proxy error: %v\n", err)
// 		}
// 	}

// 	// Wait for buffered data to be sent
// 	for conn.dataChannel.BufferedAmount() > 0 {
// 		time.Sleep(10 * time.Millisecond)
// 	}

// 	// Clean up
// 	conn.Close()
// 	close(doneCh)
// }

// // setupDataChannelCallbacks configures the callbacks for non-detached mode
// func setupDataChannelCallbacks(conn *Connection) {
// 	if !conn.detached {
// 		conn.dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
// 			if !conn.clientReady {
// 				handleHandshakeMessage(conn, msg.Data)
// 				return
// 			}

// 			if err := writeTCP(conn, msg.Data); err != nil {
// 				fmt.Printf("Error writing to TCP: %v\n", err)
// 				conn.Close()
// 			}
// 		})
// 	}
// }
