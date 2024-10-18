package pkg

import (
	"errors"
	"net"
	"sync"

	"github.com/pion/datachannel"
	"github.com/pion/webrtc/v4"
)

const (
	bufferedAmountLowThreshold uint64 = 512 * 1024  // 512 KB
	maxBufferedAmount          uint64 = 1024 * 1024 // 1 MB
	// The buffer size for reading from the TCP connection should be approximately the same as the data channel buffer size.
	// In webrtc, a message can safely be up to 16KB, so we'll use a buffer size of 16KB for reading from the TCP connection.
	maxBufferSize int = 16 * 1024
)

var OpenConnections = make(map[string]*Connection)
var connectionsLock sync.Mutex

type Connection struct {
	peerConnection *webrtc.PeerConnection
	dataChannel    *webrtc.DataChannel
	conn           net.Conn
	resourceURL    string
	clientReady    bool
	detached       bool
	rawDetached    datachannel.ReadWriteCloser
	sendMoreCh     chan struct{} // rate control signal
}

func DefaultPeerConnectionConfig() webrtc.Configuration {
	return webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
		// Use a single transport for all media streams. In our case, we're not dealing with media streams,
		// but setting this to MaxBundle can potentially reduce overhead by minimizing the number of network connections used
		BundlePolicy: webrtc.BundlePolicyMaxBundle,
		// Require multiplexing of RTCP (Real-Time Control Protocol) with RTP (Real-time Transport Protocol) on a single port.
		// While we're not using RTP/RTCP directly, this setting can help reduce the number of ports used and simplify NAT traversal.
		RTCPMuxPolicy: webrtc.RTCPMuxPolicyRequire,
	}
}

// SendRaw sends data over the data channel and blocks until all data has been sent.
func (c *Connection) SendRaw(data []byte) error {
	if !c.detached {
		return errors.New("cannot send raw data on non-detached connection")
	}
	sentData := 0
	for sentData < len(data) {
		n, err := c.rawDetached.Write(data[sentData:])
		if err != nil {
			return err
		}
		sentData += n
	}
	return nil
}

// SendTCP sends data over the TCP connection until all data has been sent or an error occurs.
func (c *Connection) SendTCP(data []byte) error {
	sentData := 0
	for sentData < len(data) {
		n, err := c.conn.Write(data[sentData:])
		if err != nil {
			return err
		}
		sentData += n
	}
	return nil
}

// ReceiveRaw reads data from the data channel, returning the number of bytes read or any error that occurred.
func (c *Connection) ReceiveRaw(data []byte) (int, error) {
	if !c.detached {
		return 0, errors.New("cannot receive raw data on non-detached connection")
	}
	return c.rawDetached.Read(data)
}
