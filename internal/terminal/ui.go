//go:build linux

package terminal

var TerminalUI = `<!DOCTYPE html>
<html>
<head>
    <title>NanoContainer Terminal</title>
    <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/xterm@5.3.0/css/xterm.min.css">
    <style>
        * { box-sizing: border-box; }
        body { margin: 0; background: #0a0a0f; overflow: hidden; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; }
        #terminal { height: calc(100vh - 48px); padding: 0; }
        .terminal-header { 
            background: #0f0f14; 
            height: 48px;
            padding: 0 16px; 
            display: flex; 
            justify-content: space-between;
            align-items: center;
            border-bottom: 1px solid rgba(255,255,255,0.06);
            user-select: none;
        }
        .terminal-title { 
            color: #22d3ee; 
            font-family: 'JetBrains Mono', 'Fira Code', 'Cascadia Code', monospace; 
            font-size: 13px;
            letter-spacing: 0.3px;
        }
        .terminal-title .separator { 
            color: rgba(255,255,255,0.15); 
            margin: 0 8px;
        }
        .terminal-title .container-name { 
            color: #a5f3fc;
            font-weight: 500;
        }
        .terminal-status-group {
            display: flex;
            align-items: center;
            gap: 12px;
        }
        .terminal-status { 
            color: #22c55e; 
            font-size: 12px;
            font-family: monospace;
            display: flex;
            align-items: center;
            gap: 6px;
        }
        .terminal-status.disconnected { 
            color: #ef4444; 
        }
        .terminal-status .dot {
            width: 7px;
            height: 7px;
            border-radius: 50%;
            background: currentColor;
            display: inline-block;
            animation: pulse 2s infinite;
        }
        @keyframes pulse {
            0%, 100% { opacity: 1; }
            50% { opacity: 0.4; }
        }
        .terminal-btn { 
            background: rgba(255,255,255,0.05); 
            border: 1px solid rgba(255,255,255,0.1); 
            color: #e2e8f0; 
            padding: 5px 14px; 
            border-radius: 5px; 
            cursor: pointer; 
            font-size: 12px;
            font-family: monospace;
            transition: all 0.15s ease;
        }
        .terminal-btn:hover { 
            background: rgba(255,255,255,0.1);
            border-color: rgba(255,255,255,0.2);
        }
        .terminal-btn:active {
            transform: scale(0.97);
        }
        .terminal-btn.danger {
            border-color: rgba(239,68,68,0.3);
            color: #fca5a5;
        }
        .terminal-btn.danger:hover {
            background: rgba(239,68,68,0.15);
            border-color: rgba(239,68,68,0.5);
        }
        .terminal-dims {
            color: rgba(255,255,255,0.3);
            font-size: 11px;
            font-family: monospace;
            margin-right: 8px;
        }
    </style>
</head>
<body>
    <div class="terminal-header">
        <span class="terminal-title">
            NanoContainer Terminal
            <span class="separator">&mdash;</span>
            Container: <span class="container-name" id="container-id">...</span>
        </span>
        <div class="terminal-status-group">
            <span class="terminal-dims" id="dims">80x24</span>
            <span class="terminal-status" id="status"><span class="dot"></span> Connected</span>
            <button class="terminal-btn" onclick="reconnect()">Reconnect</button>
            <button class="terminal-btn" onclick="copyOutput()">Copy</button>
            <button class="terminal-btn danger" onclick="sendExit()">Exit</button>
        </div>
    </div>
    <div id="terminal"></div>
    <script src="https://cdn.jsdelivr.net/npm/xterm@5.3.0/lib/xterm.min.js"></script>
    <script src="https://cdn.jsdelivr.net/npm/xterm-addon-fit@0.8.0/lib/xterm-addon-fit.min.js"></script>
    <script src="https://cdn.jsdelivr.net/npm/xterm-addon-web-links@0.9.0/lib/xterm-addon-web-links.min.js"></script>
    <script>
        const term = new Terminal({
            cursorBlink: true,
            cursorStyle: 'bar',
            fontSize: 14,
            fontFamily: "'JetBrains Mono', 'Fira Code', 'Cascadia Code', monospace",
            fontWeight: '400',
            letterSpacing: 0.3,
            lineHeight: 1.2,
            scrollback: 10000,
            theme: {
                background: '#0a0a0f',
                foreground: '#e2e8f0',
                cursor: '#22d3ee',
                cursorAccent: '#0a0a0f',
                selectionBackground: 'rgba(34,211,238,0.3)',
                selectionForeground: '#ffffff',
                black: '#1e293b',
                red: '#f87171',
                green: '#4ade80',
                yellow: '#facc15',
                blue: '#60a5fa',
                magenta: '#c084fc',
                cyan: '#22d3ee',
                white: '#e2e8f0',
                brightBlack: '#475569',
                brightRed: '#fca5a5',
                brightGreen: '#86efac',
                brightYellow: '#fef08a',
                brightBlue: '#93c5fd',
                brightMagenta: '#d8b4fe',
                brightCyan: '#67e8f9',
                brightWhite: '#f8fafc'
            },
            allowProposedApi: true,
            convertEol: true,
            scrollOnUserInput: true
        });

        const fitAddon = new FitAddon.FitAddon();
        const webLinksAddon = new WebLinksAddon.WebLinksAddon();
        term.loadAddon(fitAddon);
        term.loadAddon(webLinksAddon);
        term.open(document.getElementById('terminal'));
        fitAddon.fit();

        const containerId = new URLSearchParams(window.location.search).get('container') || 'default';
        document.getElementById('container-id').textContent = containerId;

        let ws = null;
        let reconnectAttempts = 0;
        const maxReconnectAttempts = 10;
        const baseReconnectDelay = 1000;

        function updateStatus(connected) {
            const el = document.getElementById('status');
            if (connected) {
                el.innerHTML = '<span class="dot"></span> Connected';
                el.className = 'terminal-status';
                reconnectAttempts = 0;
            } else {
                el.innerHTML = '<span class="dot"></span> Disconnected';
                el.className = 'terminal-status disconnected';
            }
        }

        function updateDims() {
            document.getElementById('dims').textContent = term.cols + 'x' + term.rows;
        }

        function connect() {
            if (ws && (ws.readyState === WebSocket.CONNECTING || ws.readyState === WebSocket.OPEN)) {
                ws.close();
            }

            const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
            ws = new WebSocket(protocol + '//' + window.location.host + '/ws/terminal?container=' + containerId + '&cols=' + term.cols + '&rows=' + term.rows);
            ws.binaryType = 'arraybuffer';

            ws.onopen = () => {
                updateStatus(true);
                term.focus();
                term.writeln('\r\n\x1b[38;5;22m\x1b[1m[Connected to container: ' + containerId + ']\x1b[0m\r\n');
            };

            ws.onclose = (e) => {
                updateStatus(false);
                if (reconnectAttempts < maxReconnectAttempts) {
                    const delay = Math.min(baseReconnectDelay * Math.pow(2, reconnectAttempts), 30000);
                    reconnectAttempts++;
                    setTimeout(connect, delay);
                } else {
                    term.writeln('\r\n\x1b[38;5;1m[Connection lost. Click Reconnect to try again.]\x1b[0m\r\n');
                }
            };

            ws.onerror = () => {
                updateStatus(false);
            };

            ws.onmessage = (e) => {
                try {
                    const msg = JSON.parse(e.data);
                    if (msg.type === 'output') {
                        term.write(msg.data);
                    } else if (msg.type === 'exit') {
                        term.writeln('\r\n\x1b[38;5;1m[Container process exited]\x1b[0m\r\n');
                        ws.close();
                    }
                } catch (err) {
                    if (typeof e.data === 'string') {
                        term.write(e.data);
                    }
                }
            };
        }

        term.onData((data) => {
            if (ws && ws.readyState === WebSocket.OPEN) {
                ws.send(JSON.stringify({ type: 'input', data: data }));
            }
        });

        term.onResize(({ cols, rows }) => {
            updateDims();
            if (ws && ws.readyState === WebSocket.OPEN) {
                ws.send(JSON.stringify({ type: 'resize', cols: cols, rows: rows }));
            }
        });

        function reconnect() {
            reconnectAttempts = 0;
            term.clear();
            term.writeln('\x1b[38;5;22m[Reconnecting...]\x1b[0m');
            connect();
        }

        function copyOutput() {
            const active = term.buffer.active;
            let text = '';
            for (let i = 0; i < active.length; i++) {
                const line = active.getLine(i);
                if (line) {
                    text += line.translateToString(true) + '\n';
                }
            }
            navigator.clipboard.writeText(text).then(() => {
                const btn = event.target;
                btn.textContent = 'Copied!';
                setTimeout(() => { btn.textContent = 'Copy'; }, 1500);
            }).catch(() => {
                const textarea = document.createElement('textarea');
                textarea.value = text;
                document.body.appendChild(textarea);
                textarea.select();
                document.execCommand('copy');
                document.body.removeChild(textarea);
            });
        }

        function sendExit() {
            if (ws && ws.readyState === WebSocket.OPEN) {
                ws.send(JSON.stringify({ type: 'exit', data: '' }));
            }
            setTimeout(() => {
                if (ws) ws.close();
            }, 200);
        }

        let resizeTimeout;
        window.addEventListener('resize', () => {
            clearTimeout(resizeTimeout);
            resizeTimeout = setTimeout(() => {
                fitAddon.fit();
                updateDims();
            }, 100);
        });

        document.addEventListener('keydown', (e) => {
            if (e.ctrlKey && e.shiftKey && e.key === 'C') {
                e.preventDefault();
                copyOutput();
            }
            if (e.ctrlKey && e.shiftKey && e.key === 'R') {
                e.preventDefault();
                reconnect();
            }
        });

        connect();
        updateDims();
    </script>
</body>
</html>`
