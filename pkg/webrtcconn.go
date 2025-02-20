package pkg

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

type WebRTCConn struct {
	connection    *Connection
	writeMutex    sync.Mutex
	localAddr     net.Addr
	remoteAddr    net.Addr
	bearerToken   string
	closed        bool
	readBuffer    []byte
	bufferSize    int
	bufferPos     int
	maxBufferSize int
}

func DialWebRTCConn(signalServer string, targetName string, bearerToken string, detached bool) (*WebRTCConn, error) {
	c, err := DialClientConnection(signalServer, targetName, bearerToken, detached)
	if err != nil {
		return nil, err
	}

	return &WebRTCConn{
		connection:    c,
		localAddr:     nil,
		remoteAddr:    nil,
		bearerToken:   bearerToken,
		closed:        false,
		readBuffer:    make([]byte, maxBufferSize),
		bufferSize:    0,
		bufferPos:     0,
		maxBufferSize: maxBufferSize,
	}, nil
}

// Create a new WebRTCConn from a listener connection on the server side
func ListenerWebRTCConn(connection *Connection) (*WebRTCConn, error) {
	return &WebRTCConn{
		connection:    connection,
		localAddr:     nil,
		remoteAddr:    nil,
		bearerToken:   "",
		closed:        false,
		readBuffer:    make([]byte, maxBufferSize),
		bufferSize:    0,
		bufferPos:     0,
		maxBufferSize: maxBufferSize,
	}, nil
}

func (c *WebRTCConn) Read(b []byte) (n int, err error) {
	// we'll need to use an internal buffer to read up to maxBufferSize bytes to
	// prevent the dreaded 'short buffer' error

	// Refill the buffer if it's empty
	if c.bufferSize == 0 || c.bufferPos == c.bufferSize {
		c.bufferSize, err = c.connection.ReceiveRaw(c.readBuffer)
		if err != nil {
			return 0, err
		}

		if c.bufferSize == 0 {
			return 0, io.EOF
		}

		c.bufferPos = 0
	}

	// Copy data from the read buffer to b
	copied := copy(b, c.readBuffer[c.bufferPos:c.bufferSize])
	c.bufferPos += copied
	return copied, nil
}

func (c *WebRTCConn) Write(b []byte) (n int, err error) {
	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()
	err = c.connection.SendRawDataChannel(b)

	return len(b), err
}

func (c *WebRTCConn) Close() error {
	if !c.closed {
		c.closed = true

		// wait for the data channel to drain before closing the connection
		// this is necessary because the data channel is buffered and we may have
		// data that has not been read yet.  We don't want to wait forever, so we
		// give up after 1 second.  Sometimes the data channel BufferedAmount does
		// not always decrease (invalid tracking?).
		lcount := 0
		for {
			bamount := c.connection.DataChannel().BufferedAmount()
			if bamount > 0 {
				time.Sleep(10 * time.Millisecond)
				lcount++
				if lcount > 100 {
					fmt.Println("Close - Buffered amount not decreasing, closing connection")
					break
				}
				continue
			}
			break
		}

		// close the ice connection
		c.connection.PeerConnection().Close()
		// call the "DELETE" on the host ResourceUrl if one was provided
		resourceURL := c.connection.ResourceURL()
		if resourceURL != "" {
			req, err := http.NewRequest("DELETE", resourceURL, nil)
			if err != nil {
				return fmt.Errorf("unexpected error building http request. %v", err)
			}

			bearerToken := c.connection.BearerToken()
			if bearerToken != "" {
				req.Header.Add("Authorization", "Bearer "+bearerToken)
			}

			client := getHttpClient()
			_, err = client.Do(req)
			if err != nil {
				return fmt.Errorf("failed http DELETE request: %s", err)
			}
		}
	}
	return nil
}

func (c *WebRTCConn) LocalAddr() net.Addr {
	return c.localAddr
}

func (c *WebRTCConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}

func (c *WebRTCConn) SetDeadline(t time.Time) error {
	// Not implemented
	return nil
}

func (c *WebRTCConn) SetReadDeadline(t time.Time) error {
	// Not implemented
	return nil
}

func (c *WebRTCConn) SetWriteDeadline(t time.Time) error {
	// Not implemented
	return nil
}
