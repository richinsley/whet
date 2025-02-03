// wasm/main.go

//go:build wasm

package main

import (
	"fmt"
	"syscall/js"

	whet "github.com/richinsley/whet/pkg"
)

func main() {
	fmt.Println("Whet WASM Initialized")

	c := make(chan struct{}, 0)

	js.Global().Set("createWhetConnection", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		promise := js.Global().Get("Promise").New(js.FuncOf(func(this js.Value, promiseArgs []js.Value) interface{} {
			resolve := promiseArgs[0]
			reject := promiseArgs[1]

			// Create a goroutine for the async work
			go func() {
				whetHandlerAddr := args[0].String()
				targetID := args[1].String()
				bearerToken := args[2].String()

				conn, err := whet.DialWebRTCConn(whetHandlerAddr, targetID, bearerToken, true)
				if err != nil {
					// Need to run callback on main JS thread
					js.Global().Get("setTimeout").Invoke(js.FuncOf(func(this js.Value, args []js.Value) interface{} {
						reject.Invoke(err.Error())
						return nil
					}), 0)
					return
				}

				connInterface := map[string]interface{}{
					"write": js.FuncOf(func(this js.Value, args []js.Value) interface{} {
						// Return a promise for write operation
						return js.Global().Get("Promise").New(js.FuncOf(func(this js.Value, promiseArgs []js.Value) interface{} {
							resolve := promiseArgs[0]
							reject := promiseArgs[1]

							go func() {
								data := make([]byte, args[0].Length())
								js.CopyBytesToGo(data, args[0])

								n, err := conn.Write(data)
								if err != nil {
									js.Global().Get("setTimeout").Invoke(js.FuncOf(func(this js.Value, args []js.Value) interface{} {
										reject.Invoke(err.Error())
										return nil
									}), 0)
									return
								}

								js.Global().Get("setTimeout").Invoke(js.FuncOf(func(this js.Value, args []js.Value) interface{} {
									resolve.Invoke(n)
									return nil
								}), 0)
							}()
							return nil
						}))
					}),
					"read": js.FuncOf(func(this js.Value, args []js.Value) interface{} {
						// Return a promise for read operation
						return js.Global().Get("Promise").New(js.FuncOf(func(this js.Value, promiseArgs []js.Value) interface{} {
							resolve := promiseArgs[0]
							reject := promiseArgs[1]

							go func() {
								size := args[0].Int()
								buffer := make([]byte, size)

								fmt.Println("Reading", size, "bytes")
								n, err := conn.Read(buffer)
								fmt.Println("Read", n, "bytes")
								if err != nil {
									js.Global().Get("setTimeout").Invoke(js.FuncOf(func(this js.Value, args []js.Value) interface{} {
										fmt.Println("Read error", err)
										reject.Invoke(err.Error())
										return nil
									}), 0)
									return
								}

								js.Global().Get("setTimeout").Invoke(js.FuncOf(func(this js.Value, args []js.Value) interface{} {
									uint8Array := js.Global().Get("Uint8Array").New(n)
									js.CopyBytesToJS(uint8Array, buffer[:n])
									resolve.Invoke(uint8Array)
									return nil
								}), 0)
							}()
							return nil
						}))
					}),
					"close": js.FuncOf(func(this js.Value, args []js.Value) interface{} {
						// Return a promise for close operation
						return js.Global().Get("Promise").New(js.FuncOf(func(this js.Value, promiseArgs []js.Value) interface{} {
							resolve := promiseArgs[0]
							reject := promiseArgs[1]

							go func() {
								err := conn.Close()
								if err != nil {
									js.Global().Get("setTimeout").Invoke(js.FuncOf(func(this js.Value, args []js.Value) interface{} {
										reject.Invoke(err.Error())
										return nil
									}), 0)
									return
								}

								js.Global().Get("setTimeout").Invoke(js.FuncOf(func(this js.Value, args []js.Value) interface{} {
									resolve.Invoke(nil)
									return nil
								}), 0)
							}()
							return nil
						}))
					}),
				}

				// Need to run callback on main JS thread
				js.Global().Get("setTimeout").Invoke(js.FuncOf(func(this js.Value, args []js.Value) interface{} {
					resolve.Invoke(js.ValueOf(connInterface))
					return nil
				}), 0)
			}()
			return nil
		}))
		return promise
	}))

	<-c
}
