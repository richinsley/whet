package pkg

import (
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"testing"
	"time"
)

func simpleMirrorServer(address string, t *testing.T) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		panic(err)
	}
	defer listener.Close()

	// read 4 bytes from the connection which will be the length of the data
	conn, err := listener.Accept()
	if err != nil {
		fmt.Printf("Error accepting connection: %v\n", err)
		return
	}

	// read the length of the data
	lengthBuffer := make([]byte, 4)
	_, err = conn.Read(lengthBuffer)
	if err != nil {
		fmt.Printf("Error reading length: %v\n", err)
		return
	}

	// convert the length buffer to an integer
	length := int(lengthBuffer[0]) | int(lengthBuffer[1])<<8 | int(lengthBuffer[2])<<16 | int(lengthBuffer[3])<<24

	// read the length of the data into a buffer
	dataBuffer := make([]byte, length)

	totalRead := 0
	for totalRead < length {
		n, err := conn.Read(dataBuffer[totalRead:])
		if err != nil {
			t.Fatalf("Error reading buffer: %v", err)
		}
		totalRead += n
		fmt.Printf("Simple server Read %d bytes of %d\n", totalRead, length)
	}

	// write the size of the data back to the client
	_, err = conn.Write(lengthBuffer)
	if err != nil {
		fmt.Printf("Error writing length: %v\n", err)
		return
	}

	// write the data back to the client
	totalWritten := 0
	for totalWritten < length {
		n, err := conn.Write(dataBuffer[totalWritten:])
		if err != nil {
			t.Fatalf("Error writing buffer: %v", err)
		}
		totalWritten += n
		fmt.Printf("Simple server Wrote %d bytes of %d\n", totalWritten, length)
	}

	conn.Close()
	fmt.Println("Simple server sent data back to client")
}

// TestServerClient create a server that port forwards 9999 and listen for whep handler on 8088
// create a client that establishes a port forward to 10000
func TestServerClient(t *testing.T) {
	// name := "Gladys"
	// want := regexp.MustCompile(`\b`+name+`\b`)
	// msg, err := Hello("Gladys")
	// if !want.MatchString(msg) || err != nil {
	//     t.Fatalf(`Hello("Gladys") = %q, %v, want match for %#q, nil`, msg, err, want)
	// }

	whepHandlerAddr := "127.0.0.1:8088"
	serverTargetAddr := "127.0.0.1:9999"
	clientTargetAddr := "127.0.0.1:10000"
	bufferSize := 1024 * 1024
	bearerToken := ""

	// create our simple server.
	go func() {
		simpleMirrorServer(serverTargetAddr, t)
	}()

	// create the whep handler
	go func() {
		http.HandleFunc("/whep/", func(w http.ResponseWriter, r *http.Request) {
			WhepHandler(w, r, serverTargetAddr, bearerToken)
		})
		fmt.Printf("WHEP signaling server running on http://%s\n", whepHandlerAddr)
		http.ListenAndServe("127.0.0.1:8088", nil)
	}()

	go func() {
		listener, err := net.Listen("tcp", clientTargetAddr)
		if err != nil {
			panic(err)
		}
		defer listener.Close()

		fmt.Printf("Listening for TCP connections on %s\n", clientTargetAddr)

		for {
			conn, err := listener.Accept()
			if err != nil {
				fmt.Printf("Error accepting connection: %v\n", err)
				continue
			}

			go HandleClientConnection(conn, whepHandlerAddr, bearerToken)
		}
	}()

	// wait for 4 seconds for the server and client to start
	time.Sleep(4 * time.Second)

	// open a tcp connection to the client target address
	conn, err := net.Dial("tcp", clientTargetAddr)
	if err != nil {
		t.Fatalf("Error connecting to client target: %v", err)
	}

	// create a very large buffer and fill it with random data
	buffer := make([]byte, bufferSize)
	for i := range buffer {
		buffer[i] = byte(rand.Uint32())
	}

	// write the length of the buffer to the connection
	lengthBuffer := make([]byte, 4)
	lengthBuffer[0] = byte(bufferSize & 0xFF)
	lengthBuffer[1] = byte((bufferSize >> 8) & 0xFF)
	lengthBuffer[2] = byte((bufferSize >> 16) & 0xFF)
	lengthBuffer[3] = byte((bufferSize >> 24) & 0xFF)
	_, err = conn.Write(lengthBuffer)
	if err != nil {
		t.Fatalf("Error writing length: %v", err)
	}

	// write the buffer to the connection
	_, err = conn.Write(buffer)
	if err != nil {
		t.Fatalf("Error writing buffer: %v", err)
	}

	// read the 4 bytes that represent the length of the data
	_, err = conn.Read(lengthBuffer)
	if err != nil {
		t.Fatalf("Error reading length: %v", err)
	}

	// convert the length buffer to an integer
	length := int(lengthBuffer[0]) | int(lengthBuffer[1])<<8 | int(lengthBuffer[2])<<16 | int(lengthBuffer[3])<<24
	if length != bufferSize {
		t.Fatalf("Expected length %d, got %d", bufferSize, length)
	}

	// create a second buffer to read the data into
	readBuffer := make([]byte, bufferSize)

	// continue reading from the connection until we have read all the data
	totalRead := 0
	for totalRead < bufferSize {
		n, err := conn.Read(readBuffer[totalRead:])
		if err != nil {
			t.Fatalf("Error reading buffer: %v", err)
		}
		totalRead += n
		fmt.Printf("Client Read %d bytes of %d\n", totalRead, bufferSize)
	}

	// ensure we read the correct number of bytes
	if totalRead != bufferSize {
		t.Fatalf("Expected to read %d bytes, read %d", bufferSize, totalRead)
	}

	// compare the two buffers
	for i := range buffer {
		if buffer[i] != readBuffer[i] {
			t.Fatalf("Buffers do not match at index %d", i)
		}
	}

	t.Log("Buffers match")
}
