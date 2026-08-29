import { useState, useRef, useCallback, useEffect, type MutableRefObject } from 'react';
import {
  Plus,
  X,
  ZoomIn,
  ZoomOut,
  Trash2,
  Maximize2,
  Minimize2,
  Terminal as TerminalIcon,
  Command,
  RefreshCw,
  WifiOff,
  ClipboardPaste,
  Loader2,
  ShieldAlert,
} from 'lucide-react';
import { toast } from 'sonner';
import { cn } from '@/lib/utils';
import { api } from '@/lib/api';
import { WebTerminal } from '@/components/modules/web-terminal';
import { CommandPalette } from '@/components/modules/command-palette';
import { useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { useNow } from '@/hooks/useNow';
import { useCurrentUser } from '@/hooks/useAuth';
import { managedNodeOnline, SELECTED_SERVER_KEY } from '@/lib/serverNavigation';

interface Tab {
  id: string;
  label: string;
}

interface ManagedNodeTerminalState {
  id: string;
  name: string;
  hostname: string;
  capabilities: string[];
  last_seen_at?: string;
	online?: boolean;
}

function heartbeatAge(lastSeenAt: string | undefined, now: number): string | undefined {
  if (!lastSeenAt) return undefined;
  const timestamp = new Date(lastSeenAt).getTime();
  if (!Number.isFinite(timestamp)) return undefined;
  const seconds = Math.max(0, Math.floor((now - timestamp) / 1000));
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  return `${Math.floor(minutes / 60)}h ago`;
}

function genId(): string {
  return crypto.randomUUID();
}

const MAX_TABS = 8;
const MIN_FONT = 10;
const MAX_FONT = 24;
const DEFAULT_FONT = 14;

// ─── TerminalPanel ────────────────────────────────────────────────────────────
// Wrapper that owns its sendCommandRef so the parent never reads a ref during render.

interface TerminalPanelProps {
  tabId: string;
  label: string;
  isActive: boolean;
  fontSize: number;
  node: string;
  onRegister: (
    tabId: string,
    sendRef: MutableRefObject<((cmd: string) => void) | null>,
    inputRef: MutableRefObject<((data: string) => void) | null>,
    clearRef: MutableRefObject<(() => void) | null>,
  ) => void;
  onUnregister: (tabId: string) => void;
}

function TerminalPanel({ tabId, label, isActive, fontSize, node, onRegister, onUnregister }: TerminalPanelProps) {
  const sendCommandRef = useRef<((cmd: string) => void) | null>(null);
  const sendInputRef = useRef<((data: string) => void) | null>(null);
  const clearTerminalRef = useRef<(() => void) | null>(null);

  useEffect(() => {
    onRegister(tabId, sendCommandRef, sendInputRef, clearTerminalRef);
    return () => onUnregister(tabId);
  // onRegister/onUnregister are stable useCallback refs — safe to omit from deps
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tabId]);

  return (
    <div
      id={`terminal-panel-${tabId}`}
      role="tabpanel"
      aria-label={label}
      aria-hidden={!isActive}
      data-terminal-tab-id={tabId}
      className={cn('absolute inset-0', isActive ? 'block' : 'hidden')}
    >
      <WebTerminal
        key={`${tabId}:${node}`}
        node={node}
        isActive={isActive}
        fontSize={fontSize}
        sendCommandRef={sendCommandRef}
        sendInputRef={sendInputRef}
        clearTerminalRef={clearTerminalRef}
        onClose={() => {/* session ended */}}
      />
    </div>
  );
}

function TerminalPermissionState({
  loading,
  error,
  role,
  retry,
  retrying,
}: {
  loading: boolean;
  error?: Error | null;
  role?: string;
  retry: () => void;
  retrying: boolean;
}) {
  const denied = !loading && !error;
  return (
    <div className="absolute inset-0 grid place-items-center bg-[#0d1117] p-6">
      <div className="w-full max-w-md rounded-xl border border-red-500/25 bg-red-500/[0.06] p-6 text-center">
        {loading ? <Loader2 className="mx-auto size-8 animate-spin text-amber-400" /> : <ShieldAlert className="mx-auto size-8 text-red-400" />}
        <h2 className="mt-3 text-sm font-semibold text-[var(--color-foreground)]">
          {loading ? 'Checking terminal permission' : error ? 'Terminal permission is unavailable' : 'Terminal access denied'}
        </h2>
        <p className="mt-2 text-xs leading-5 text-[var(--color-muted-foreground)]">
          {loading
            ? 'HServer is verifying the signed-in account before opening a writable shell.'
            : error
              ? 'The panel could not verify the signed-in account, so it did not open a terminal session.'
              : <>Writable terminal sessions require the <code>admin</code> role. The current <code>{role || 'unknown'}</code> role was not upgraded to a shell session.</>}
        </p>
        {error && <p className="mt-3 break-words rounded bg-zinc-950/60 px-2.5 py-2 font-mono text-[11px] text-zinc-500">{error.message || 'Account verification failed'}</p>}
        {!denied && !loading && (
          <button
            type="button"
            onClick={retry}
            disabled={retrying}
            className="mt-4 inline-flex items-center gap-1.5 rounded-lg border border-red-500/30 px-3 py-1.5 text-xs font-medium text-red-300 transition-colors hover:bg-red-500/10 disabled:opacity-50"
          >
            <RefreshCw className={cn('size-3.5', retrying && 'animate-spin')} />
            Retry permission
          </button>
        )}
      </div>
    </div>
  );
}

// ─── TerminalPage ─────────────────────────────────────────────────────────────

export function TerminalPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const requestedNode = searchParams.get('node');
  const node = requestedNode && requestedNode !== 'local' ? requestedNode : 'local';
  const initialId = genId();
  const [tabs, setTabs] = useState<Tab[]>([{ id: initialId, label: 'Terminal 1' }]);
  const [activeTabId, setActiveTabId] = useState(initialId);
  const [fontSize, setFontSize] = useState(DEFAULT_FONT);
  const [isFullscreen, setIsFullscreen] = useState(false);
  const [isPaletteOpen, setIsPaletteOpen] = useState(false);
  const [editingTabId, setEditingTabId] = useState<string | null>(null);
  const [editingLabel, setEditingLabel] = useState('');
  const now = useNow();
  const currentUserQuery = useCurrentUser();
  const currentUser = currentUserQuery.data;
  const terminalPermissionLoading = !currentUser && currentUserQuery.isLoading;
  const terminalPermissionError = !currentUser && currentUserQuery.isError
    ? currentUserQuery.error
    : null;
  const terminalPermitted = currentUser?.role === 'admin';
  const nodesQuery = useQuery<ManagedNodeTerminalState[]>({
    queryKey: ['managed-nodes'],
    queryFn: () => api.get('/nodes'),
    enabled: terminalPermitted,
    refetchInterval: 5_000,
  });
  const remoteSelected = node !== 'local';
  const selectedNode = nodesQuery.data?.find((candidate) => candidate.id === node);
  const selectedNodeOnline = managedNodeOnline(selectedNode, now);
  const selectedNodeTerminalAvailable = selectedNode?.capabilities?.includes('terminal') ?? false;
  const remoteTerminalReady = !remoteSelected
    || (!nodesQuery.isLoading && !nodesQuery.isError && selectedNodeOnline && selectedNodeTerminalAvailable);
  const selectedNodeHeartbeatAge = heartbeatAge(selectedNode?.last_seen_at, now);
  const serverLabel = remoteSelected
    ? selectedNode?.name || selectedNode?.hostname || node
    : 'HServer';
  const terminalControlsReady = terminalPermitted && remoteTerminalReady;

  const selectNode = useCallback((next: string) => {
    const managedNode = next && next !== 'local' ? next : 'local';
    try { localStorage.setItem(SELECTED_SERVER_KEY, managedNode); } catch { /* ignore */ }
    window.dispatchEvent(new CustomEvent('hserver:server-changed', { detail: managedNode }));
    setSearchParams(managedNode === 'local' ? {} : { node: managedNode });
  }, [setSearchParams]);

  // Registry of sendCommand refs keyed by tab id — only accessed in event handlers/effects
  const sendCommandRefs = useRef<Map<string, MutableRefObject<((cmd: string) => void) | null>>>(new Map());
  const sendInputRefs = useRef<Map<string, MutableRefObject<((data: string) => void) | null>>>(new Map());
  const clearTerminalRefs = useRef<Map<string, MutableRefObject<(() => void) | null>>>(new Map());

  const registerTab = useCallback((
    tabId: string,
    sendRef: MutableRefObject<((cmd: string) => void) | null>,
    inputRef: MutableRefObject<((data: string) => void) | null>,
    clearRef: MutableRefObject<(() => void) | null>,
  ) => {
    sendCommandRefs.current.set(tabId, sendRef);
    sendInputRefs.current.set(tabId, inputRef);
    clearTerminalRefs.current.set(tabId, clearRef);
  }, []);

  const unregisterTab = useCallback((tabId: string) => {
    sendCommandRefs.current.delete(tabId);
    sendInputRefs.current.delete(tabId);
    clearTerminalRefs.current.delete(tabId);
  }, []);

  const addTab = useCallback(() => {
    if (!terminalControlsReady || tabs.length >= MAX_TABS) return;
    const id = genId();
    const newTab: Tab = { id, label: `Terminal ${tabs.length + 1}` };
    setTabs((prev) => [...prev, newTab]);
    setActiveTabId(id);
  }, [tabs.length, terminalControlsReady]);

  const closeTab = useCallback(
    (tabId: string, e: React.MouseEvent) => {
      e.stopPropagation();
      setTabs((prev) => {
        const next = prev.filter((t) => t.id !== tabId);
        if (tabId === activeTabId && next.length > 0) {
          const idx = prev.findIndex((t) => t.id === tabId);
          setActiveTabId(next[Math.max(0, idx - 1)].id);
        }
        return next;
      });
    },
    [activeTabId]
  );

  const startRename = useCallback((tab: Tab, e: React.MouseEvent) => {
    e.stopPropagation();
    setEditingTabId(tab.id);
    setEditingLabel(tab.label);
  }, []);

  const commitRename = useCallback(() => {
    if (!editingTabId) return;
    setTabs((prev) =>
      prev.map((t) =>
        t.id === editingTabId ? { ...t, label: editingLabel.trim() || t.label } : t
      )
    );
    setEditingTabId(null);
  }, [editingTabId, editingLabel]);

  const sendToActive = useCallback(
    (command: string) => {
      // Access ref map only in event handler — not during render
      const ref = sendCommandRefs.current.get(activeTabId);
      ref?.current?.(command);
    },
    [activeTabId]
  );

  const focusActiveTerminal = useCallback(() => {
    requestAnimationFrame(() => {
      document
        .querySelector<HTMLElement>(`[data-terminal-tab-id="${activeTabId}"] .xterm-helper-textarea`)
        ?.focus();
    });
  }, [activeTabId]);

  const sendInputToActive = useCallback((data: string) => {
    sendInputRefs.current.get(activeTabId)?.current?.(data);
    focusActiveTerminal();
  }, [activeTabId, focusActiveTerminal]);

  const pasteToActive = useCallback(async () => {
    try {
      const text = await navigator.clipboard.readText();
      if (!text) {
        toast.info('Clipboard is empty');
        focusActiveTerminal();
        return;
      }
      sendInputRefs.current.get(activeTabId)?.current?.(text);
      focusActiveTerminal();
    } catch {
      toast.error('Clipboard access was blocked by the browser');
      focusActiveTerminal();
    }
  }, [activeTabId, focusActiveTerminal]);

  const clearActiveTerminal = useCallback(() => {
    clearTerminalRefs.current.get(activeTabId)?.current?.();
    focusActiveTerminal();
  }, [activeTabId, focusActiveTerminal]);

  const closeCommandPalette = useCallback(() => {
    setIsPaletteOpen(false);
    focusActiveTerminal();
  }, [focusActiveTerminal]);

  // Keyboard shortcuts
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.ctrlKey && e.key === '`') {
        e.preventDefault();
        if (terminalControlsReady) setIsPaletteOpen((o) => !o);
      }
      if (e.ctrlKey && e.key === 't' && !e.shiftKey) {
        e.preventDefault();
        addTab();
      }
      if (e.ctrlKey && e.shiftKey && e.key.toLowerCase() === 'f') {
        e.preventDefault();
        setIsFullscreen((fullscreen) => !fullscreen);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [addTab, terminalControlsReady]);

  if (tabs.length === 0) {
    return (
      <div className="flex size-full items-center justify-center">
        <button
          onClick={addTab}
          className="flex flex-col items-center gap-3 rounded-xl border border-dashed border-[var(--color-border)] p-10 text-[var(--color-muted-foreground)] transition-colors hover:border-[var(--color-primary)] hover:text-[var(--color-primary)]"
        >
          <TerminalIcon className="size-10" />
          <span className="text-sm">Open Terminal</span>
        </button>
      </div>
    );
  }

  return (
    <>
      <div
        className={cn(
          'flex flex-col',
          isFullscreen ? 'fixed inset-0 z-40 bg-[var(--color-background)]' : 'size-full'
        )}
      >
        {/* Header bar */}
        <div className="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-2 border-b border-[var(--color-border)] px-4 py-2">
          <div className="flex min-w-0 flex-1 items-center gap-2">
            <TerminalIcon className="size-4 text-[var(--color-muted-foreground)]" />
            <h1 className="text-sm font-semibold text-[var(--color-foreground)]">Web Terminal</h1>
            <select
              value={node}
              onChange={(event) => selectNode(event.target.value)}
              className="rounded-md border border-[var(--color-border)] bg-[var(--color-muted)] px-2 py-1 text-xs text-[var(--color-foreground)]"
              aria-label="Terminal server"
            >
              <option value="local">HServer</option>
              {nodesQuery.data?.map((candidate) => (
                <option key={candidate.id} value={candidate.id}>
                  {candidate.name || candidate.hostname || candidate.id} · {!managedNodeOnline(candidate, now) ? 'Offline' : candidate.capabilities?.includes('terminal') ? 'Terminal ready' : 'Terminal disabled'}
                </option>
              ))}
              {remoteSelected && !selectedNode && (
                <option value={node}>{node} · Unavailable</option>
              )}
            </select>
          </div>

          {/* Toolbar */}
          <div className="order-3 flex w-full items-center justify-end gap-1 sm:order-none sm:w-auto">
            <button
              onClick={() => setIsPaletteOpen(true)}
              disabled={!terminalControlsReady}
              title="Command Palette (Ctrl+`)"
              className="flex items-center gap-1.5 rounded-md px-2 py-1 text-xs text-[var(--color-muted-foreground)] transition-colors hover:bg-[var(--color-accent)] hover:text-[var(--color-foreground)] disabled:pointer-events-none disabled:opacity-40"
            >
              <Command className="size-3.5" />
              <span className="hidden sm:inline">Commands</span>
              <kbd className="hidden rounded border border-[var(--color-border)] bg-[var(--color-muted)] px-1 py-0.5 text-[10px] sm:inline">
                Ctrl+`
              </kbd>
            </button>

            <div className="mx-1 h-4 w-px bg-[var(--color-border)]" />

            <button
              onClick={() => setFontSize((s) => Math.max(MIN_FONT, s - 1))}
              title="Decrease font size"
              disabled={fontSize <= MIN_FONT}
              className="rounded-md p-1.5 text-[var(--color-muted-foreground)] transition-colors hover:bg-[var(--color-accent)] hover:text-[var(--color-foreground)] disabled:opacity-40"
            >
              <ZoomOut className="size-3.5" />
            </button>
            <span className="min-w-[2ch] text-center text-xs tabular-nums text-[var(--color-muted-foreground)]">
              {fontSize}
            </span>
            <button
              onClick={() => setFontSize((s) => Math.min(MAX_FONT, s + 1))}
              title="Increase font size"
              disabled={fontSize >= MAX_FONT}
              className="rounded-md p-1.5 text-[var(--color-muted-foreground)] transition-colors hover:bg-[var(--color-accent)] hover:text-[var(--color-foreground)] disabled:opacity-40"
            >
              <ZoomIn className="size-3.5" />
            </button>

            <div className="mx-1 h-4 w-px bg-[var(--color-border)]" />

            <button
              onClick={clearActiveTerminal}
              disabled={!terminalControlsReady}
              title="Clear terminal"
              aria-label="Clear active terminal display"
              className="rounded-md p-1.5 text-[var(--color-muted-foreground)] transition-colors hover:bg-[var(--color-accent)] hover:text-[var(--color-foreground)] disabled:pointer-events-none disabled:opacity-40"
            >
              <Trash2 className="size-3.5" />
            </button>

            <button
              onClick={() => setIsFullscreen((f) => !f)}
              title={isFullscreen ? 'Exit fullscreen (Ctrl+Shift+F)' : 'Fullscreen (Ctrl+Shift+F)'}
              aria-label={isFullscreen ? 'Exit terminal fullscreen' : 'Enter terminal fullscreen'}
              className="rounded-md p-1.5 text-[var(--color-muted-foreground)] transition-colors hover:bg-[var(--color-accent)] hover:text-[var(--color-foreground)]"
            >
              {isFullscreen ? <Minimize2 className="size-3.5" /> : <Maximize2 className="size-3.5" />}
            </button>
          </div>
        </div>

        {/* Tab bar */}
        <div role="tablist" aria-label="Terminal sessions" className="flex shrink-0 items-center gap-0 overflow-x-auto border-b border-[var(--color-border)] bg-[var(--color-muted)]/30 px-2 scrollbar-hide">
          {tabs.map((tab) => (
            <div
              key={tab.id}
              className={cn(
                'group flex min-w-0 max-w-[160px] items-center gap-1.5 border-b-2 px-3 py-2 text-xs transition-colors',
                tab.id === activeTabId
                  ? 'border-[var(--color-primary)] bg-[var(--color-background)] text-[var(--color-foreground)]'
                  : 'border-transparent text-[var(--color-muted-foreground)] hover:bg-[var(--color-accent)]/50 hover:text-[var(--color-foreground)]'
              )}
            >
              <TerminalIcon className="size-3 shrink-0" />

              {editingTabId === tab.id ? (
                <input
                  autoFocus
                  value={editingLabel}
                  onChange={(e) => setEditingLabel(e.target.value)}
                  onBlur={commitRename}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') commitRename();
                    if (e.key === 'Escape') setEditingTabId(null);
                    e.stopPropagation();
                  }}
                  className="w-20 bg-transparent text-xs focus:outline-none"
                  onClick={(e) => e.stopPropagation()}
                />
              ) : (
                <button
                  id={`terminal-tab-${tab.id}`}
                  type="button"
                  role="tab"
                  aria-selected={tab.id === activeTabId}
                  aria-controls={`terminal-panel-${tab.id}`}
                  tabIndex={tab.id === activeTabId ? 0 : -1}
                  onClick={() => setActiveTabId(tab.id)}
                  onKeyDown={(event) => {
                    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
                    event.preventDefault();
                    const index = tabs.findIndex((candidate) => candidate.id === tab.id);
                    const offset = event.key === 'ArrowRight' ? 1 : -1;
                    const next = tabs[(index + offset + tabs.length) % tabs.length];
                    setActiveTabId(next.id);
                    window.setTimeout(() => document.getElementById(`terminal-tab-${next.id}`)?.focus(), 75);
                  }}
                  className="truncate"
                  onDoubleClick={(e) => startRename(tab, e)}
                  title="Double-click to rename"
                >
                  {tab.label}
                </button>
              )}

              <button
                onClick={(e) => closeTab(tab.id, e)}
                onKeyDown={(event) => event.stopPropagation()}
                aria-label={`Close ${tab.label}`}
                className={cn(
                  'ml-auto shrink-0 rounded p-0.5 transition-colors',
                  tabs.length === 1 ? 'invisible' : 'invisible group-hover:visible hover:bg-[var(--color-accent)]',
                  tab.id === activeTabId && 'visible'
                )}
              >
                <X className="size-3" />
              </button>
            </div>
          ))}

          {tabs.length < MAX_TABS && (
            <button
              onClick={addTab}
              disabled={!terminalControlsReady}
              title="New tab (Ctrl+T)"
              aria-label="New terminal tab"
              className="ml-1 shrink-0 rounded-md p-1.5 text-[var(--color-muted-foreground)] transition-colors hover:bg-[var(--color-accent)] hover:text-[var(--color-foreground)] disabled:pointer-events-none disabled:opacity-40"
            >
              <Plus className="size-3.5" />
            </button>
          )}
        </div>

        {/* Touch keyboards often hide terminal control keys; keep them one tap away on mobile. */}
        <div aria-label="Mobile terminal keys" className="flex shrink-0 items-center gap-1 overflow-x-auto border-b border-[var(--color-border)] bg-[#0d1117] px-2 py-1.5 sm:hidden">
          {[
            { label: 'Esc', ariaLabel: 'Send Escape', data: '\x1b' },
            { label: 'Tab', ariaLabel: 'Send Tab', data: '\t' },
            { label: 'Ctrl+C', ariaLabel: 'Send Control C', data: '\x03' },
            { label: 'Ctrl+L', ariaLabel: 'Send Control L', data: '\x0c' },
            { label: '↑', ariaLabel: 'Send Arrow Up', data: '\x1b[A' },
            { label: '↓', ariaLabel: 'Send Arrow Down', data: '\x1b[B' },
          ].map((key) => (
            <button
              key={key.ariaLabel}
              type="button"
              onClick={() => sendInputToActive(key.data)}
              disabled={!terminalControlsReady}
              aria-label={key.ariaLabel}
              className="min-h-9 shrink-0 rounded-md border border-zinc-700 bg-zinc-900 px-3 font-mono text-xs font-medium text-zinc-300 transition-colors active:bg-zinc-700 active:text-white disabled:opacity-40"
            >
              {key.label}
            </button>
          ))}
          <button
            type="button"
            onClick={() => void pasteToActive()}
            disabled={!terminalControlsReady}
            aria-label="Paste clipboard into terminal"
            className="flex min-h-9 shrink-0 items-center gap-1.5 rounded-md border border-zinc-700 bg-zinc-900 px-3 text-xs font-medium text-zinc-300 transition-colors active:bg-zinc-700 active:text-white disabled:opacity-40"
          >
            <ClipboardPaste className="size-3.5" />
            Paste
          </button>
        </div>

        {/* Terminal panels */}
        <div className="relative flex-1 overflow-hidden">
          {!terminalPermitted ? (
            <TerminalPermissionState
              loading={terminalPermissionLoading}
              error={terminalPermissionError}
              role={currentUser?.role}
              retry={() => { void currentUserQuery.refetch() }}
              retrying={currentUserQuery.isFetching}
            />
          ) : remoteTerminalReady ? tabs.map((tab) => (
              <TerminalPanel
                key={`${tab.id}-${node}`}
                tabId={tab.id}
                label={tab.label}
                isActive={tab.id === activeTabId}
                fontSize={fontSize}
                node={node}
                onRegister={registerTab}
                onUnregister={unregisterTab}
              />
            )) : (
              <div className="absolute inset-0 grid place-items-center bg-[#0d1117] p-6">
                <div className="w-full max-w-md rounded-xl border border-amber-500/25 bg-amber-500/[0.06] p-6 text-center">
                  <WifiOff className="mx-auto size-8 text-amber-400" />
                  <h2 className="mt-3 text-sm font-semibold text-[var(--color-foreground)]">
                    {nodesQuery.isError
                      ? `${serverLabel} status is unavailable`
                      : nodesQuery.isLoading
                        ? `Checking ${serverLabel} heartbeat`
                        : selectedNodeOnline && !selectedNodeTerminalAvailable
                          ? `Writable terminal is not enabled on ${serverLabel}`
                          : `${serverLabel} is offline`}
                  </h2>
                  <p className="mt-2 text-xs leading-5 text-[var(--color-muted-foreground)]">
                    {nodesQuery.isError
                      ? 'The panel could not verify the managed node, so it did not open a remote shell.'
                      : nodesQuery.isLoading
                        ? 'Waiting for the control plane before opening a remote shell.'
                        : selectedNodeOnline && !selectedNodeTerminalAvailable
                          ? <>Set <code>HSERVER_AGENT_ALLOW_TERMINAL=true</code> in the agent environment, then run the agent lifecycle upgrade so its systemd sandbox is regenerated. The terminal will connect automatically after the agent advertises the capability.</>
                        : `No fresh agent heartbeat was received${selectedNodeHeartbeatAge ? ` · last seen ${selectedNodeHeartbeatAge}` : ''}. The terminal will connect automatically when ${serverLabel} returns online.`}
                  </p>
                  <div className="mt-4 flex flex-wrap justify-center gap-2">
                    <button
                      type="button"
                      onClick={() => nodesQuery.refetch()}
                      disabled={nodesQuery.isFetching}
                      className="inline-flex items-center gap-1.5 rounded-lg border border-amber-500/30 px-3 py-1.5 text-xs font-medium text-amber-300 transition-colors hover:bg-amber-500/10 disabled:opacity-50"
                    >
                      <RefreshCw className={cn('size-3.5', nodesQuery.isFetching && 'animate-spin')} />
                      Retry status
                    </button>
                    <button
                      type="button"
                      onClick={() => selectNode('local')}
                      className="rounded-lg bg-[var(--color-primary)] px-3 py-1.5 text-xs font-medium text-[var(--color-primary-foreground)] transition-opacity hover:opacity-90"
                    >
                      Open HServer terminal
                    </button>
                  </div>
                </div>
              </div>
            )}
        </div>
      </div>

      <CommandPalette
        key={isPaletteOpen ? 'open' : 'closed'}
        isOpen={terminalControlsReady && isPaletteOpen}
        onClose={closeCommandPalette}
        onSelectCommand={sendToActive}
        serverLabel={serverLabel}
      />
    </>
  );
}
