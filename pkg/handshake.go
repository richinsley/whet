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
	ctx := context.Background()

	if isServer {
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

func (c *Connection) sendReadySignal() error {
	if c.detached {
		return c.SendRaw([]byte(ReadyMessage))
	} else {
		return c.dataChannel.Send([]byte(ReadyMessage))
	}
}

func (c *Connection) performClientHandshake(ctx context.Context) error {
	// Wait for peer's ready signal
	if err := c.waitForPeerReady(false, ctx); err != nil {
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
	if err := c.waitForPeerReady(true, ctx); err != nil {
		return fmt.Errorf("peer handshake failed: %w", err)
	}

	return nil
}

func (c *Connection) waitForPeerReady(servermode bool, ctx context.Context) error {
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
			mode := "client"
			if servermode {
				mode = "server"
			}
			errCh <- fmt.Errorf("invalid handshake message %s mode", mode)
			return
		}

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
