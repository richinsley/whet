# we need the wasm_exec.js from GOROOT
cp "$(go env GOROOT)/misc/wasm/wasm_exec.js" .

# build wasm
GOOS=js GOARCH=wasm go build -o main.wasm main.go

# build wasm optimized
GOOS=js GOARCH=wasm go build -o main.wasm -ldflags="-s -w" main.go

# install binaryen https://github.com/WebAssembly/binaryen
```bash
cd ~/projects
git clone https://github.com/WebAssembly/binaryen.git
cd binaryen
git submodule init
git submodule update
mkdir build
cd build
cmake ..
make
cd bin
# optimize the wasm
./wasm-opt -Oz --enable-bulk-memory-opt ~/projects/whet/wasm/main.wasm -o main_optimized.wasm
cp main_optimized.wasm ~/projects/whet/wasm/main.wasm
```

## the file server can be modified to serve compressed versions of the files
## (for embedding purposes) with gz or brotli

# install brotli to compress main.wasm to main.wasm.br
sudo apt install brotli

# compress main.wasm to main.wasm.br
brotli --best main.wasm

# the file serve portion of whap would need to be modified to handle Content-Encoding for *.br files
# the browser would then be able to decompress the wasm (and html) on the fly when loading
https://chatgpt.com/share/679ce581-17c0-8005-bc84-eaf338c4c324


# information on service workers in chrome:
https://www.chromium.org/blink/serviceworker/service-worker-faq/
chrome://inspect/#service-workers

Q: I made a change to my service worker. How do I reload?

A: From Developer Tools > Application > Service Workers, check "Update on reload" and reload the page.



FFFFFUDGE!!!! service-workers can't handle webrtc

We need that have the main page create a Worker (or maybe a SharedWorker)