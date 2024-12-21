package pkg

import (
	"bytes"
	"context"
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

func handleAttachedHandshake(conn *Connection, isServer bool, wg *sync.WaitGroup) error {
	if isServer {
		if err := conn.dataChannel.Send([]byte("SERVER_READY")); err != nil {
			return err
		}
	}
	return nil // The rest is handled in OnMessage callbacks
}

func handleDetachedHandshake(conn *Connection, isServer bool, wg *sync.WaitGroup) error {
	// // make a buffer to read the ready message, it should be large enough to prevent the 'short buffer' error
	// readybuf := make([]byte, 12)

	ctx := context.Background()

	if isServer {
		// // SERVER_READY should have been sent by the server immediately after the data channel opens
		// // We just need to ensure the first message from the client must be the CLIENT_READY message
		// n, err := conn.rawDetached.Read(readybuf)
		// if err != nil {
		// 	if conn.conn != nil {
		// 		conn.conn.Close()
		// 	}
		// 	conn.closed = true
		// 	return fmt.Errorf("error reading from rawDetached: %v", err)
		// }

		// if n != 12 || !bytes.Equal(readybuf, []byte("CLIENT_READY")) {
		// 	if conn.conn != nil {
		// 		conn.conn.Close()
		// 	}
		// 	conn.closed = true
		// 	return fmt.Errorf("handshake failed, closing connection")
		// }
		fmt.Println("Server waiting for client ready signal")
		err := conn.performServerHandshake(ctx)
		if err != nil {
			if conn.conn != nil {
				conn.conn.Close()
			}
			conn.closed = true
			return fmt.Errorf("handshake failed, closing connection")
		}
		fmt.Println("Server received client ready signal")
	} else {
		// // Client waits for SERVER_READY
		// n, err := conn.ReceiveRaw(readybuf)
		// if err != nil || n != 12 || !bytes.Equal(readybuf, []byte("SERVER_READY")) {
		// 	return fmt.Errorf("handshake failed")
		// }

		// // Send CLIENT_READY
		// if err := conn.SendRaw([]byte("CLIENT_READY")); err != nil {
		// 	return err
		// }

		fmt.Println("Client waiting for server ready signal")
		err := conn.performClientHandshake(ctx)
		if err != nil {
			if conn.conn != nil {
				conn.conn.Close()
			}
			conn.closed = true
			return fmt.Errorf("handshake failed, closing connection")
		}
		fmt.Println("Client received server ready signal")
	}

	conn.clientReady = true
	if wg != nil {
		wg.Done()
	}
	return nil
}

/* --------- */

func (c *Connection) sendReadySignal() error {
	if c.detached {
		return c.SendRaw([]byte(ReadyMessage))
	} else {
		return c.dataChannel.Send([]byte(ReadyMessage))
	}
}

func (c *Connection) performClientHandshake(ctx context.Context) error {
	// Wait for peer's ready signal
	if err := c.waitForPeerReady(ctx); err != nil {
		return fmt.Errorf("peer handshake failed: %w", err)
	}

	// Signal our ready state
	if err := c.sendReadySignal(); err != nil {
		return fmt.Errorf("failed to send ready signal: %w", err)
	}

	return nil
}

func (c *Connection) performServerHandshake(ctx context.Context) error {
	// Signal our ready state
	if err := c.sendReadySignal(); err != nil {
		return fmt.Errorf("failed to send ready signal: %w", err)
	}

	// Wait for peer's ready signal
	if err := c.waitForPeerReady(ctx); err != nil {
		return fmt.Errorf("peer handshake failed: %w", err)
	}

	return nil
}

func (c *Connection) performHandshake(ctx context.Context) error {
	// Signal our ready state
	if err := c.sendReadySignal(); err != nil {
		return fmt.Errorf("failed to send ready signal: %w", err)
	}

	// Wait for peer's ready signal
	if err := c.waitForPeerReady(ctx); err != nil {
		return fmt.Errorf("peer handshake failed: %w", err)
	}

	return nil
}

func (c *Connection) waitForPeerReady(ctx context.Context) error {
	readyCh := make(chan struct{})
	errCh := make(chan error, 1)

	go func() {
		buffer := make([]byte, len(ReadyMessage))
		n, err := c.rawDetached.Read(buffer)
		if err != nil {
			errCh <- err
			return
		}

		if n != len(ReadyMessage) || !bytes.Equal(buffer[:n], []byte(ReadyMessage)) {
			errCh <- fmt.Errorf("invalid handshake message")
			return
		}

		// c.remoteReady.Store(true)
		// c.readyWg.Done()
		close(readyCh)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	case <-readyCh:
		return nil
	}
}
