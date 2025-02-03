const VERSION = '1.0.14';

// Add MIME type mapping
const MIME_TYPES = {
    '.html': 'text/html',
    '.css': 'text/css',
    '.js': 'application/javascript',
    '.mjs': 'application/javascript',
    '.json': 'application/json',
    '.png': 'image/png',
    '.jpg': 'image/jpeg',
    '.jpeg': 'image/jpeg',
    '.gif': 'image/gif',
    '.svg': 'image/svg+xml',
    '.ico': 'image/x-icon',
    '.woff': 'font/woff',
    '.woff2': 'font/woff2',
    '.ttf': 'font/ttf',
    '.eot': 'application/vnd.ms-fontobject'
};

function getMimeType(path) {
    const ext = path.substring(path.lastIndexOf('.')).toLowerCase();
    return MIME_TYPES[ext] || 'application/octet-stream';
}

self.addEventListener('install', (event) => {
    console.log('Service Worker installing, version:', VERSION);
    event.waitUntil(self.skipWaiting());
});

self.addEventListener('activate', (event) => {
    console.log('Service Worker activating, version:', VERSION);
    event.waitUntil(Promise.all([self.clients.claim()]));
});

self.addEventListener('fetch', async (event) => {
    // Handle any request under /whet/
    if (!event.request.url.includes('/whet/')) {
        return;
    }

    event.respondWith((async () => {
        console.log('Processing fetch for:', event.request.url);
        
        const stream = new ReadableStream({
            async start(controller) {
                const clients = await self.clients.matchAll({
                    type: 'window',
                    includeUncontrolled: true
                });
                const client = clients[0];
                if (!client) {
                    controller.error('No client found');
                    return;
                }

                const channel = new MessageChannel();
                const port = channel.port1;

                client.postMessage({
                    type: 'tcp-connection',
                    url: event.request.url,
                    port: channel.port2
                }, [channel.port2]);

                await new Promise(resolve => {
                    port.onmessage = (msg) => {
                        if (msg.data.type === 'connection-ready') {
                            resolve();
                        }
                    };
                });

                // Parse the URL to get the correct path for the proxied request
                const url = new URL(event.request.url);
                const match = url.pathname.match(/^\/whet\/([^/]+)(\/.*)$/);
                if (!match) {
                    controller.error('Invalid proxy path');
                    return;
                }
                
                const proxyTarget = match[1];
                const finalPath = match[2];  // Use the full remaining path
                
                // Copy original headers
                const headers = new Headers(event.request.headers);
                headers.set('Host', proxyTarget);
                headers.set('Connection', 'close');

                // Include original query string if present
                const requestPath = finalPath + (url.search || '');
                
                const requestLine = `${event.request.method} ${requestPath} HTTP/1.1\r\n`;
                const headerLines = Array.from(headers.entries())
                    .map(([key, value]) => `${key}: ${value}`)
                    .join('\r\n');
                const requestHeader = requestLine + headerLines + '\r\n\r\n';
                
                console.log('Sending request:', requestLine);
                
                port.postMessage({
                    type: 'data',
                    chunk: new TextEncoder().encode(requestHeader)
                });

                // Handle response
                let responseHeaders = null;
                let headerBuffer = '';
                
                port.onmessage = (msg) => {
                    if (msg.data.type === 'data') {
                        const chunk = msg.data.chunk;
                        if (!responseHeaders) {
                            headerBuffer += new TextDecoder().decode(chunk);
                            const headerEnd = headerBuffer.indexOf('\r\n\r\n');
                            if (headerEnd !== -1) {
                                // Parse headers
                                const headerText = headerBuffer.substring(0, headerEnd);
                                const headerLines = headerText.split('\r\n');
                                const statusLine = headerLines.shift();
                                const [_, status] = statusLine.match(/HTTP\/\d\.\d (\d{3})/);
                                
                                responseHeaders = new Headers();
                                for (const line of headerLines) {
                                    const [name, ...values] = line.split(': ');
                                    responseHeaders.set(name, values.join(': '));
                                }
                                
                                // Get body content after headers
                                const bodyContent = headerBuffer.substring(headerEnd + 4);
                                if (bodyContent.length > 0) {
                                    controller.enqueue(new TextEncoder().encode(bodyContent));
                                }
                                headerBuffer = '';
                            }                            
                            // if (headerEnd !== -1) {
                            //     // Parse headers
                            //     const headerText = headerBuffer.substring(0, headerEnd);
                            //     const headerLines = headerText.split('\r\n');
                            //     const statusLine = headerLines.shift();
                            //     const [_, status] = statusLine.match(/HTTP\/\d\.\d (\d{3})/);
                                
                            //     responseHeaders = new Headers();
                            //     for (const line of headerLines) {
                            //         const [name, ...values] = line.split(': ');
                            //         responseHeaders.set(name, values.join(': '));
                            //     }
                                
                            //     // Get body content after headers
                            //     const bodyContent = headerBuffer.substring(headerEnd + 4);
                            //     if (bodyContent.length > 0) {
                            //         controller.enqueue(new TextEncoder().encode(bodyContent));
                            //     }
                            //     headerBuffer = '';
                            // }
                        } else {
                            // Direct body content
                            controller.enqueue(chunk);
                        }
                    } else if (msg.data.type === 'end') {
                        console.log('Response complete for:', event.request.url);
                        controller.close();
                    } else if (msg.data.type === 'error') {
                        controller.error(msg.data.error);
                    }
                };
            }
        });

        // Wait for headers before creating response
        const response = new Response(stream, {
            status: 200,
            headers: new Headers({
                'Content-Type': getMimeType(event.request.url),
                'Content-Security-Policy': "default-src 'self' 'unsafe-inline' 'unsafe-eval'"
            })
        });

        return response;
    })());
});
