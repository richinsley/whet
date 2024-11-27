package main

import (
	"fmt"
)

import "C"

//export HelloGopher
func HelloGopher() {
	fmt.Println("Hello Gopher!")
}

func main() {}

// GOOS=ios GOARCH=arm64 go build -v -o libwhap.a -buildmode=c-archive lib/main.go
