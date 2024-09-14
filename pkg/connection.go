package pkg

import (
	"net"
	"sync"

	"github.com/pion/webrtc/v3"
)

var OpenConnections = make(map[string]*Connection)
var connectionsLock sync.Mutex

type Connection struct {
	peerConnection *webrtc.PeerConnection
	dataChannel    *webrtc.DataChannel
	conn           net.Conn
	resourceURL    string
	clientReady    bool
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
