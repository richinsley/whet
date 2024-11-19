class WebRTCProxyConnection {
    constructor(signalServer, targetName, bearerToken) {
        this.signalServer = signalServer;
        this.targetName = targetName;
        this.bearerToken = bearerToken;
        this.pc = null;
        this.dataChannel = null;
        this.resourceUrl = null;
        this.dataCallback = null;
        this.buffer = new Uint8Array();
        this.handshakeComplete = false;
    }

    async connect() {
        // Create RTCPeerConnection
        this.pc = new RTCPeerConnection({
            iceServers: [{urls: 'stun:stun.l.google.com:19302'}]
        });

        // Create data channel
        this.dataChannel = this.pc.createDataChannel('data', {
            ordered: true
        });

        // Setup data channel handlers before creating offer
        const channelReady = this._setupDataChannel();

        // Create and send offer
        const offer = await this.pc.createOffer();
        await this.pc.setLocalDescription(offer);

        // Wait for ICE gathering to complete
        await new Promise(resolve => {
            if (this.pc.iceGatheringState === 'complete') {
                resolve();
            } else {
                this.pc.addEventListener('icegatheringstatechange', () => {
                    if (this.pc.iceGatheringState === 'complete') {
                        resolve();
                    }
                });
            }
        });

        console.log('Sending offer to signal server');
        
        // Send offer to signal server
        const response = await fetch(`${this.signalServer}/whet/${this.targetName}`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/sdp',
                'Authorization': this.bearerToken ? `Bearer ${this.bearerToken}` : ''
            },
            body: this.pc.localDescription.sdp
        });

        // Handle response
        if (!response.ok) {
            throw new Error(`HTTP error! status: ${response.status}`);
        }

        // Get answer SDP and resource URL
        const answerSdp = await response.text();
        this.resourceUrl = response.headers.get('Location');

        console.log('Received answer from signal server');

        // Set remote description
        await this.pc.setRemoteDescription({
            type: 'answer',
            sdp: answerSdp
        });

        // Wait for data channel to be ready
        await channelReady;
        
        console.log('WebRTC connection established');
    }

    _setupDataChannel() {
        return new Promise((resolve, reject) => {
            let handshakeTimeout = setTimeout(() => {
                reject(new Error('Handshake timeout'));
            }, 10000); // 10 second timeout

            this.dataChannel.onopen = () => {
                console.log('Data channel opened');
                
                // Handle incoming messages
                this.dataChannel.onmessage = ({data}) => {
                    console.log('Received message:', data);
                    
                    if (!this.handshakeComplete) {
                        const decoder = new TextDecoder();
                        if (decoder.decode(data) === 'SERVER_READY') {
                            console.log('Received SERVER_READY, sending CLIENT_READY');
                            this.dataChannel.send('CLIENT_READY');
                            this.handshakeComplete = true;
                            clearTimeout(handshakeTimeout);
                            resolve();
                        }
                    } else if (this.dataCallback) {
                        // Handle incoming binary data
                        if (data instanceof ArrayBuffer) {
                            this.dataCallback(new Uint8Array(data));
                        }
                    }
                };
            };

            this.dataChannel.onerror = (error) => {
                console.error('Data channel error:', error);
                reject(error);
            };

            this.dataChannel.onclose = () => {
                console.log('Data channel closed');
                if (!this.handshakeComplete) {
                    reject(new Error('Data channel closed before handshake completed'));
                }
            };
        });
    }

    onData(callback) {
        this.dataCallback = callback;
    }

    async close() {
        if (this.resourceUrl) {
            try {
                await fetch(this.resourceUrl, {
                    method: 'DELETE',
                    headers: {
                        'Authorization': this.bearerToken ? `Bearer ${this.bearerToken}` : ''
                    }
                });
            } catch (e) {
                console.error('Error closing proxy connection:', e);
            }
        }

        if (this.dataChannel) {
            this.dataChannel.close();
        }
        if (this.pc) {
            this.pc.close();
        }
    }
}

// // Example usage:
// async function testConnection() {
//     try {
//         const proxy = new WebRTCProxyConnection(
//             'http://192.168.0.28:8083',  // Your signal server
//             'prism',                     // Your target name
//             ''                           // Bearer token if needed
//         );

//         console.log('Creating proxy connection...');
//         await proxy.connect();
//         console.log('Connected!');

//         // Set up data handler
//         proxy.onData((data) => {
//             console.log('Received data:', data);
//         });

//     } catch (error) {
//         console.error('Connection failed:', error);
//     }
// }

// // Test the connection
// testConnection();