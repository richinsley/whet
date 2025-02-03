package pkg

import (
	"fmt"
	"net"
)

func SimpleMirrorServer(address string) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		panic(err)
	}
	defer listener.Close()

	for {
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
				fmt.Printf("Error reading buffer: %v", err)
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
				fmt.Printf("Error writing buffer: %v", err)
				break
			}
			totalWritten += n
			fmt.Printf("Simple server Wrote %d bytes of %d\n", totalWritten, length)
		}

		conn.Close()
		fmt.Println("Simple server sent data back to client")
	}
}
