import { useEffect, useRef, useCallback, useState } from 'react';
import '@xterm/xterm/css/xterm.css';
import { cn } from '@/lib/utils';
import { getToken } from '@/lib/api';
import {
  decodeTerminalOutput,
  TERMINAL_MAX_RECONNECT_ATTEMPTS,
  terminalReconnectDelay,
} from '@/lib/terminalTransport';

interface WebTerminalProps {
  isActive: boolean;
  fontSize: number;
  node?: string;
  onReady?: () => void;
  onClose?: () => void;
  sendCommandRef?: React.MutableRefObject<((cmd: string) => void) | null>;
  sendInputRef?: React.MutableRefObject<((data: string) => void) | null>;
  clearTerminalRef?: React.MutableRefObject<(() => void) | null>;
}

type ConnectionStatus = 'connecting' | 'connected' | 'reconnecting' | 'disconnected';

const TERMINAL_PING_INTERVAL_MS = 15_000;
const TERMINAL_PONG_TIMEOUT_MS = 35_000;

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type AnyRef = React.MutableRefObject<any>;

export function WebTerminal({
  isActive,
  fontSize,
  node = 'local',
  onReady,
  onClose,
  sendCommandRef,
  sendInputRef,
  clearTerminalRef,
}: WebTerminalProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const termRef: AnyRef = useRef(null);
  const fitAddonRef: AnyRef = useRef(null);
  const wsRef = useRef<WebSocket | null>(null);
  const watchdogRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const reconnectAttemptRef = useRef(0);
  const autoReconnectRef = useRef(true);
  const connectRef = useRef<() => void>(() => undefined);
  const [status, setStatus] = useState<ConnectionStatus>('connecting');
  const [disconnectMessage, setDisconnectMessage] = useState('Terminal session ended');
  const mountedRef = useRef(true);

  const clearWatchdog = useCallback(() => {
    if (watchdogRef.current) {
      clearTimeout(watchdogRef.current);
      watchdogRef.current = null;
    }
  }, []);

  const clearReconnect = useCallback(() => {
    if (reconnectTimerRef.current) {
      clearTimeout(reconnectTimerRef.current);
      reconnectTimerRef.current = null;
    }
  }, []);

  const scheduleReconnect = useCallback(() => {
    if (!mountedRef.current || !autoReconnectRef.current || reconnectTimerRef.current) return;

    const attempt = reconnectAttemptRef.current + 1;
    reconnectAttemptRef.current = attempt;
    if (attempt > TERMINAL_MAX_RECONNECT_ATTEMPTS) {
      setDisconnectMessage('Could not reconnect to terminal');
      setStatus('disconnected');
      return;
    }

    setStatus('reconnecting');
    reconnectTimerRef.current = setTimeout(() => {
      reconnectTimerRef.current = null;
      connectRef.current();
    }, terminalReconnectDelay(attempt));
  }, []);

  const armWatchdog = useCallback((socket: WebSocket) => {
    clearWatchdog();
    watchdogRef.current = setTimeout(() => {
      if (!mountedRef.current || wsRef.current !== socket) return;
      socket.close();
    }, TERMINAL_PONG_TIMEOUT_MS);
  }, [clearWatchdog]);

  const connectWebSocket = useCallback(() => {
    if (!mountedRef.current) return;

    const previousSocket = wsRef.current;
    wsRef.current = null;
    previousSocket?.close();
    clearWatchdog();

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const cols = termRef.current?.cols ?? 80;
    const rows = termRef.current?.rows ?? 24;
    // Same-origin cookies cover current sessions. The JWT subprotocol keeps
    // older localStorage-only sessions working without putting credentials in
    // browser history, proxy diagnostics or copied connection URLs.
    const wsUrl = `${protocol}//${window.location.host}/api/terminal/ws?cols=${cols}&rows=${rows}&node=${encodeURIComponent(node)}&encoding=base64`;
	const token = getToken();
	const protocols = token
	  ? ['hserver-terminal', `jwt.${token}`]
	  : ['hserver-terminal'];

    const ws = new WebSocket(wsUrl, protocols);
    wsRef.current = ws;

    ws.onopen = () => {
      if (!mountedRef.current || wsRef.current !== ws) return;
      setStatus('connecting');
      ws.send(JSON.stringify({
        type: 'resize',
        cols: termRef.current?.cols ?? 80,
        rows: termRef.current?.rows ?? 24,
      }));
      armWatchdog(ws);
    };

    ws.onmessage = (event: MessageEvent<string>) => {
      if (!mountedRef.current || wsRef.current !== ws) return;
      armWatchdog(ws);
      try {
        const msg = JSON.parse(event.data) as {
          type: string;
          data?: string;
          encoding?: string;
        };

        switch (msg.type) {
          case 'ready':
            clearReconnect();
            reconnectAttemptRef.current = 0;
            autoReconnectRef.current = true;
            setStatus('connected');
            onReady?.();
            break;
          case 'output':
            if (msg.data) {
              termRef.current?.write(
                decodeTerminalOutput(msg.data, msg.encoding)
              );
            }
            break;
          case 'close':
            autoReconnectRef.current = false;
            clearReconnect();
            setDisconnectMessage(msg.data || 'Terminal session ended');
            setStatus('disconnected');
            if (msg.data) {
              termRef.current?.writeln(`\r\n\x1b[31m${msg.data}\x1b[0m`);
            }
            onClose?.();
            break;
          case 'error':
            if (msg.data) {
              termRef.current?.writeln(`\r\n\x1b[31mError: ${msg.data}\x1b[0m`);
            }
            break;
          case 'pong':
            break;
        }
      } catch {
        // ignore parse errors
      }
    };

    ws.onclose = () => {
      if (wsRef.current !== ws) return;
      clearWatchdog();
      if (mountedRef.current) {
        if (autoReconnectRef.current) scheduleReconnect();
        else setStatus('disconnected');
      }
    };
    ws.onerror = () => {
      if (wsRef.current !== ws) return;
      clearWatchdog();
    };
  }, [armWatchdog, clearReconnect, clearWatchdog, node, onReady, onClose, scheduleReconnect]);

  useEffect(() => {
    connectRef.current = connectWebSocket;
  }, [connectWebSocket]);

  // Initialize xterm
  useEffect(() => {
    if (!containerRef.current) return;
    mountedRef.current = true;
    autoReconnectRef.current = true;

    const init = async () => {
      const { Terminal } = await import('@xterm/xterm');
      const { FitAddon } = await import('@xterm/addon-fit');
      const { SearchAddon } = await import('@xterm/addon-search');
      const { WebLinksAddon } = await import('@xterm/addon-web-links');

      if (!mountedRef.current || !containerRef.current) return;

      const term = new Terminal({
        fontFamily: "'JetBrains Mono', 'Fira Code', 'Cascadia Code', monospace",
        fontSize,
        lineHeight: 1.2,
        cursorBlink: true,
        cursorStyle: 'block',
        scrollback: 10000,
        theme: {
          background: '#0d1117',
          foreground: '#e6edf3',
          cursor: '#58a6ff',
          cursorAccent: '#0d1117',
          selectionBackground: '#264f78',
          black: '#484f58',
          red: '#ff7b72',
          green: '#3fb950',
          yellow: '#d29922',
          blue: '#58a6ff',
          magenta: '#bc8cff',
          cyan: '#39c5cf',
          white: '#b1bac4',
          brightBlack: '#6e7681',
          brightRed: '#ffa198',
          brightGreen: '#56d364',
          brightYellow: '#e3b341',
          brightBlue: '#79c0ff',
          brightMagenta: '#d2a8ff',
          brightCyan: '#56d4dd',
          brightWhite: '#f0f6fc',
        },
        allowProposedApi: true,
      });

      const fitAddon = new FitAddon();
      const searchAddon = new SearchAddon();
      const webLinksAddon = new WebLinksAddon();

      term.loadAddon(fitAddon);
      term.loadAddon(searchAddon);
      term.loadAddon(webLinksAddon);

      term.open(containerRef.current);
      fitAddon.fit();

      termRef.current = term;
      fitAddonRef.current = fitAddon;

      term.onData((data: string) => {
        if (wsRef.current?.readyState === WebSocket.OPEN) {
          wsRef.current.send(JSON.stringify({ type: 'input', data }));
        }
      });

      term.onResize(({ cols, rows }: { cols: number; rows: number }) => {
        if (wsRef.current?.readyState === WebSocket.OPEN) {
          wsRef.current.send(JSON.stringify({ type: 'resize', cols, rows }));
        }
      });

      connectWebSocket();
    };

    void init();

    return () => {
      mountedRef.current = false;
      autoReconnectRef.current = false;
      clearWatchdog();
      clearReconnect();
      wsRef.current?.close();
      termRef.current?.dispose();
    };
  // connectWebSocket is stable — safe to omit from deps
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Font size changes
  useEffect(() => {
    if (termRef.current) {
      termRef.current.options.fontSize = fontSize;
      fitAddonRef.current?.fit();
    }
  }, [fontSize]);

  // Focus + fit when tab becomes active
  useEffect(() => {
    if (!isActive) return;
    const t = setTimeout(() => {
      fitAddonRef.current?.fit();
      termRef.current?.focus();
    }, 50);
    return () => clearTimeout(t);
  }, [isActive]);

  // Window resize
  useEffect(() => {
    const onResize = () => {
      if (isActive) fitAddonRef.current?.fit();
    };
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, [isActive]);

  // Expose sendCommand
  useEffect(() => {
    if (sendCommandRef) {
      sendCommandRef.current = (cmd: string) => {
        if (wsRef.current?.readyState === WebSocket.OPEN) {
          wsRef.current.send(JSON.stringify({ type: 'input', data: cmd + '\n' }));
        }
      };
    }
  }, [sendCommandRef]);

  // Expose raw terminal input for mobile helper keys and clipboard paste.
  useEffect(() => {
    if (!sendInputRef) return;
    sendInputRef.current = (data: string) => {
      if (wsRef.current?.readyState === WebSocket.OPEN) {
        wsRef.current.send(JSON.stringify({ type: 'input', data }));
      }
    };
    return () => {
      sendInputRef.current = null;
    };
  }, [sendInputRef]);

  useEffect(() => {
    if (!clearTerminalRef) return;
    clearTerminalRef.current = () => {
      termRef.current?.write('\x1b[2J\x1b[3J\x1b[H');
      termRef.current?.scrollToBottom();
    };
    return () => {
      clearTerminalRef.current = null;
    };
  }, [clearTerminalRef]);

  // Heartbeat
  useEffect(() => {
    const iv = setInterval(() => {
      if (wsRef.current?.readyState === WebSocket.OPEN) {
        wsRef.current.send(JSON.stringify({ type: 'ping' }));
      }
    }, TERMINAL_PING_INTERVAL_MS);
    return () => clearInterval(iv);
  }, []);

  return (
    <div className="relative size-full overflow-hidden bg-[#0d1117]">
      {/* Status badge */}
      <div className="pointer-events-none absolute right-3 top-2 z-10">
        <span
          className={cn(
            'inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium',
            status === 'connected' && 'bg-emerald-500/20 text-emerald-400',
            (status === 'connecting' || status === 'reconnecting') && 'bg-amber-500/20 text-amber-400',
            status === 'disconnected' && 'bg-red-500/20 text-red-400'
          )}
        >
          <span
            className={cn(
              'size-1.5 rounded-full',
              status === 'connected' && 'bg-emerald-400',
              (status === 'connecting' || status === 'reconnecting') && 'animate-pulse bg-amber-400',
              status === 'disconnected' && 'bg-red-400'
            )}
          />
          {status}
        </span>
      </div>

      {/* xterm container */}
      <div ref={containerRef} className="size-full p-2" />

      {/* Disconnected overlay */}
      {status === 'disconnected' && (
        <div className="absolute inset-0 flex items-center justify-center bg-black/60 backdrop-blur-sm">
          <div className="flex flex-col items-center gap-3 rounded-xl border border-[var(--color-border)] bg-[var(--color-card)] p-6 text-center">
            <p className="text-sm text-[var(--color-muted-foreground)]">{disconnectMessage}</p>
            <button
              onClick={() => {
                clearReconnect();
                reconnectAttemptRef.current = 0;
                autoReconnectRef.current = true;
                setDisconnectMessage('Terminal session ended');
                setStatus('connecting');
                mountedRef.current = true;
                connectRef.current();
              }}
              className="rounded-lg bg-[var(--color-primary)] px-4 py-1.5 text-sm font-medium text-[var(--color-primary-foreground)] transition-opacity hover:opacity-90"
            >
              Reconnect
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
