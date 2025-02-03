package main

import (
	"fmt"
	"runtime/cgo"
	"unsafe"

	"github.com/richinsley/whet/pkg"
)

// #cgo CFLAGS: -fPIC
// void runtime_init() {}
import "C"

//export runtime_init_darwin
func runtime_init_darwin() {
	C.runtime_init()
}

//export darwin_arm_init_mach_exception_handler
func darwin_arm_init_mach_exception_handler() {
	// Empty implementation
}

//export darwin_arm_init_thread_exception_handler
func darwin_arm_init_thread_exception_handler() {
	// Empty implementation
}

//export darwin_arm_init_thread_exception_port
func darwin_arm_init_thread_exception_port() {
	// Empty implementation
}

func cGoToSlice(cptr unsafe.Pointer, length int) []byte {
	return (*[1 << 30]byte)(cptr)[:length:length]
}

//export DialWhapConnection
func DialWhapConnection(whetHandlerAddr *C.char, targetID *C.char, bearerToken *C.char, detached bool) C.uint {
	handler := C.GoString(whetHandlerAddr)
	id := C.GoString(targetID)
	token := C.GoString(bearerToken)
	conn, err := pkg.DialWebRTCConn(handler, id, token, detached)
	if err != nil {
		fmt.Printf("Error dialing connection: %v\n", err)
		return 0
	}

	// wrap conn in a handle
	retv := cgo.NewHandle(conn)
	return C.uint(retv)
}

//export CloseWhapConnection
func CloseWhapConnection(handle C.uint) {
	h := cgo.Handle(handle)
	conn := h.Value().(*pkg.WebRTCConn)
	conn.Write([]byte("CLOSE"))
	conn.Close()
	h.Delete()
}

//export WriteWhapData
func WriteWhapData(handle C.uint, data *C.char, length C.int) C.int {
	h := cgo.Handle(handle)
	conn := h.Value().(*pkg.WebRTCConn)
	n, err := conn.Write(cGoToSlice(unsafe.Pointer(data), int(length)))
	if err != nil {
		fmt.Printf("Error writing data: %v\n", err)
		return -1
	}
	return C.int(n)
}

//export ReadWhapData
func ReadWhapData(handle C.uint, data *C.char, length C.int) C.int {
	h := cgo.Handle(handle)
	conn := h.Value().(*pkg.WebRTCConn)
	n, err := conn.Read(cGoToSlice(unsafe.Pointer(data), int(length)))
	if err != nil {
		fmt.Printf("Error reading data: %v\n", err)
		return -1
	}
	return C.int(n)
}

func main() {}

/*
# build lib for ios
export CGO_ENABLED=1
export GOOS=darwin
export GOARCH=arm64
export CC=$(xcrun -sdk iphoneos -find clang)
export CXX=$(xcrun -sdk iphoneos -find clang++)
export SDK=$(xcrun -sdk iphoneos --show-sdk-path)
export CGO_CFLAGS="-isysroot $SDK -mios-version-min=11.0 -arch arm64 -I$(xcrun -sdk iphoneos --show-sdk-path)/usr/include"
export CGO_LDFLAGS="-isysroot $SDK -mios-version-min=11.0 -arch arm64"
go build -buildmode=c-archive -o libwgap.a lib/main.go
*/
