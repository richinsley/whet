package pkg

import (
	"bytes"
	"fmt"
	"sync"
)

// handleHandshake manages the SERVER_READY/CLIENT_READY handshake protocol
func handleHandshake(conn *Connection, isServer bool, wg *sync.WaitGroup) error {
	if conn.detached {
		return handleDetachedHandshake(conn, isServer, wg)
	}
	return handleAttachedHandshake(conn, isServer, wg)
}

func handleDetachedHandshake(conn *Connection, isServer bool, wg *sync.WaitGroup) error {
	readybuf := make([]byte, 12)

	if isServer {
		// SERVER_READY should have been sent by the server immediately after the data channel opens
		// We just need to ensure the first message from the client must be the CLIENT_READY message
		n, err := conn.rawDetached.Read(readybuf)
		if err != nil {
			conn.conn.Close()
			conn.closed = true
			return fmt.Errorf("error reading from rawDetached: %v", err)
		}

		if n != 12 || !bytes.Equal(readybuf, []byte("CLIENT_READY")) {
			conn.conn.Close()
			conn.closed = true
			return fmt.Errorf("handshake failed, closing connection")
		}
	} else {
		// Client waits for SERVER_READY
		n, err := conn.ReceiveRaw(readybuf)
		if err != nil || n != 12 || !bytes.Equal(readybuf, []byte("SERVER_READY")) {
			return fmt.Errorf("handshake failed")
		}

		// Send CLIENT_READY
		if err := conn.SendRaw([]byte("CLIENT_READY")); err != nil {
			return err
		}
	}

	conn.clientReady = true
	if wg != nil {
		wg.Done()
	}
	return nil
}

func handleAttachedHandshake(conn *Connection, isServer bool, wg *sync.WaitGroup) error {
	if isServer {
		if err := conn.dataChannel.Send([]byte("SERVER_READY")); err != nil {
			return err
		}
	}
	return nil // The rest is handled in OnMessage callbacks
}
