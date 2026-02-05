package terminal

// GetTerminalHTML returns the HTML page for the web terminal
func GetTerminalHTML() string {
	return `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Terminal</title>
    <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/@xterm/xterm@5.5.0/css/xterm.css">
    <style>
        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }
        html, body {
            height: 100%;
            width: 100%;
            overflow: hidden;
            background: #1a1b26;
        }
        #terminal {
            height: 100%;
            width: 100%;
        }
        .xterm {
            height: 100%;
            padding: 8px;
        }
        #connection-status {
            position: fixed;
            top: 8px;
            right: 8px;
            padding: 4px 12px;
            border-radius: 4px;
            font-family: monospace;
            font-size: 12px;
            z-index: 1000;
            transition: opacity 0.3s;
        }
        .status-connecting {
            background: #e0af68;
            color: #1a1b26;
        }
        .status-connected {
            background: #9ece6a;
            color: #1a1b26;
            opacity: 0;
        }
        .status-disconnected {
            background: #f7768e;
            color: #1a1b26;
        }
    </style>
</head>
<body>
    <div id="connection-status" class="status-connecting">Connecting...</div>
    <div id="terminal"></div>

    <script src="https://cdn.jsdelivr.net/npm/@xterm/xterm@5.5.0/lib/xterm.min.js"></script>
    <script src="https://cdn.jsdelivr.net/npm/@xterm/addon-fit@0.10.0/lib/addon-fit.min.js"></script>
    <script src="https://cdn.jsdelivr.net/npm/@xterm/addon-web-links@0.11.0/lib/addon-web-links.min.js"></script>
    <script>
        const statusEl = document.getElementById('connection-status');

        function setStatus(status, text) {
            statusEl.className = 'status-' + status;
            statusEl.textContent = text;
        }

        // Tokyo Night theme
        const theme = {
            background: '#1a1b26',
            foreground: '#c0caf5',
            cursor: '#c0caf5',
            cursorAccent: '#1a1b26',
            selectionBackground: '#33467c',
            black: '#15161e',
            red: '#f7768e',
            green: '#9ece6a',
            yellow: '#e0af68',
            blue: '#7aa2f7',
            magenta: '#bb9af7',
            cyan: '#7dcfff',
            white: '#a9b1d6',
            brightBlack: '#414868',
            brightRed: '#f7768e',
            brightGreen: '#9ece6a',
            brightYellow: '#e0af68',
            brightBlue: '#7aa2f7',
            brightMagenta: '#bb9af7',
            brightCyan: '#7dcfff',
            brightWhite: '#c0caf5'
        };

        const term = new Terminal({
            cursorBlink: true,
            cursorStyle: 'block',
            fontSize: 14,
            fontFamily: 'Menlo, Monaco, "Courier New", monospace',
            theme: theme,
            allowProposedApi: true
        });

        const fitAddon = new FitAddon.FitAddon();
        const webLinksAddon = new WebLinksAddon.WebLinksAddon();

        term.loadAddon(fitAddon);
        term.loadAddon(webLinksAddon);
        term.open(document.getElementById('terminal'));
        fitAddon.fit();

        // Build WebSocket URL
        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const urlParams = new URLSearchParams(window.location.search);
        const token = urlParams.get('token');
        let wsUrl = protocol + '//' + window.location.host + '/terminal/ws?cols=' + term.cols + '&rows=' + term.rows;
        if (token) {
            wsUrl += '&token=' + encodeURIComponent(token);
        }

        let ws = null;
        let reconnectAttempts = 0;
        const maxReconnectAttempts = 5;

        function connect() {
            setStatus('connecting', 'Connecting...');
            ws = new WebSocket(wsUrl);

            ws.onopen = function() {
                setStatus('connected', 'Connected');
                reconnectAttempts = 0;
                term.focus();
            };

            ws.onmessage = function(event) {
                try {
                    const msg = JSON.parse(event.data);
                    if (msg.type === 'output') {
                        term.write(msg.data);
                    } else if (msg.type === 'error') {
                        term.write('\r\n\x1b[31mError: ' + msg.data + '\x1b[0m\r\n');
                    }
                } catch (e) {
                    console.error('Failed to parse message:', e);
                }
            };

            ws.onclose = function() {
                setStatus('disconnected', 'Disconnected');
                if (reconnectAttempts < maxReconnectAttempts) {
                    reconnectAttempts++;
                    setTimeout(connect, 1000 * reconnectAttempts);
                } else {
                    term.write('\r\n\x1b[31mConnection lost. Refresh the page to reconnect.\x1b[0m\r\n');
                }
            };

            ws.onerror = function(error) {
                console.error('WebSocket error:', error);
            };
        }

        // Handle terminal input
        term.onData(function(data) {
            if (ws && ws.readyState === WebSocket.OPEN) {
                ws.send(JSON.stringify({ type: 'input', data: data }));
            }
        });

        // Handle terminal resize
        function sendResize() {
            if (ws && ws.readyState === WebSocket.OPEN) {
                ws.send(JSON.stringify({
                    type: 'resize',
                    cols: term.cols,
                    rows: term.rows
                }));
            }
        }

        window.addEventListener('resize', function() {
            fitAddon.fit();
            sendResize();
        });

        // Initial connection
        connect();
    </script>
</body>
</html>`
}
