import { useState, useEffect, useRef, useCallback } from 'react';
import { Search, Terminal, ChevronRight } from 'lucide-react';
import { cn } from '@/lib/utils';

interface Command {
  id: string;
  label: string;
  command: string;
  category: string;
  description?: string;
}

const PRESET_COMMANDS: Command[] = [
  // Codex
  { id: 'codex-open', label: 'Open Codex', command: 'codex', category: 'codex', description: 'Start the interactive Codex CLI on the selected server' },
  { id: 'codex-version', label: 'Codex version', command: 'codex --version', category: 'codex', description: 'Verify the Codex CLI installed on the selected server' },
  // nginx
  { id: 'nginx-test', label: 'nginx -t', command: 'nginx -t', category: 'nginx', description: 'Test nginx configuration syntax' },
  { id: 'nginx-reload', label: 'Reload nginx', command: 'systemctl reload nginx', category: 'nginx', description: 'Gracefully reload nginx config' },
  { id: 'nginx-restart', label: 'Restart nginx', command: 'systemctl restart nginx', category: 'nginx', description: 'Restart nginx service' },
  { id: 'nginx-status', label: 'nginx status', command: 'systemctl status nginx --no-pager', category: 'nginx', description: 'Show nginx service status and return to the prompt' },
  // PHP-FPM
  { id: 'php-version', label: 'php -v', command: 'php -v', category: 'php', description: 'Show active PHP version' },
  { id: 'php84-status', label: 'PHP 8.4-FPM status', command: 'systemctl status php8.4-fpm --no-pager', category: 'php', description: 'PHP 8.4 FPM service status without opening a pager' },
  { id: 'php85-status', label: 'PHP 8.5-FPM status', command: 'systemctl status php8.5-fpm --no-pager', category: 'php', description: 'PHP 8.5 FPM service status without opening a pager' },
  { id: 'php74-status', label: 'PHP 7.4-FPM status', command: 'systemctl status php7.4-fpm --no-pager', category: 'php', description: 'PHP 7.4 FPM service status without opening a pager' },
  // PM2
  { id: 'pm2-list', label: 'pm2 list', command: 'pm2 list', category: 'pm2', description: 'List all PM2 processes' },
  { id: 'pm2-logs', label: 'pm2 logs', command: 'pm2 logs --lines 50 --nostream', category: 'pm2', description: 'Show the last 50 PM2 log lines and return to the prompt' },
  { id: 'pm2-reload', label: 'pm2 reload all', command: 'pm2 reload all', category: 'pm2', description: 'Zero-downtime reload all processes' },
  // System
  { id: 'disk', label: 'df -h', command: 'df -h', category: 'system', description: 'Show disk usage in human-readable format' },
  { id: 'memory', label: 'free -h', command: 'free -h', category: 'system', description: 'Show memory usage' },
  { id: 'uptime', label: 'uptime', command: 'uptime', category: 'system', description: 'Show system uptime and load' },
  { id: 'top', label: 'htop', command: 'htop', category: 'system', description: 'Interactive process viewer' },
  // PostgreSQL
  { id: 'pg-status', label: 'PostgreSQL status', command: 'systemctl status postgresql --no-pager', category: 'database', description: 'Show PostgreSQL service status and return to the prompt' },
  { id: 'pg-list', label: 'psql list DBs', command: 'sudo -u postgres psql -l', category: 'database', description: 'List all PostgreSQL databases' },
  // Logs
  { id: 'nginx-error', label: 'nginx error log', command: 'tail -n 50 /var/log/nginx/error.log', category: 'logs', description: 'Tail nginx error log' },
  { id: 'journal', label: 'journalctl tail', command: 'journalctl -n 50 --no-pager', category: 'logs', description: 'Last 50 system log entries' },
  // SSL
  { id: 'certbot-dry', label: 'certbot renew --dry-run', command: 'certbot renew --dry-run', category: 'ssl', description: "Dry run Let's Encrypt renewal" },
  { id: 'certbot-list', label: 'certbot certificates', command: 'certbot certificates', category: 'ssl', description: 'List all managed certificates' },
];

const CATEGORY_COLORS: Record<string, string> = {
  codex: 'text-emerald-400',
  nginx: 'text-green-400',
  php: 'text-purple-400',
  pm2: 'text-blue-400',
  system: 'text-orange-400',
  database: 'text-cyan-400',
  logs: 'text-yellow-400',
  ssl: 'text-pink-400',
};

interface CommandPaletteProps {
  isOpen: boolean;
  onClose: () => void;
  onSelectCommand: (command: string) => void;
  serverLabel: string;
}

export function CommandPalette({ isOpen, onClose, onSelectCommand, serverLabel }: CommandPaletteProps) {
  const [query, setQuery] = useState('');
  const [selected, setSelected] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  const filtered = query.trim() === ''
    ? PRESET_COMMANDS
    : PRESET_COMMANDS.filter((cmd) => {
        const q = query.toLowerCase();
        return (
          cmd.label.toLowerCase().includes(q) ||
          cmd.command.toLowerCase().includes(q) ||
          cmd.category.toLowerCase().includes(q) ||
          (cmd.description?.toLowerCase().includes(q) ?? false)
        );
      });

  const handleSelect = useCallback(
    (cmd: Command) => {
      onSelectCommand(cmd.command);
      onClose();
      setQuery('');
      setSelected(0);
    },
    [onSelectCommand, onClose]
  );

  // Focus input on mount (parent provides key prop to remount on each open)
  useEffect(() => {
    if (isOpen) {
      const id = setTimeout(() => inputRef.current?.focus(), 50);
      return () => clearTimeout(id);
    }
  }, [isOpen]);

  useEffect(() => {
    const el = listRef.current?.children[selected] as HTMLElement | undefined;
    el?.scrollIntoView({ block: 'nearest' });
  }, [selected]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!isOpen) return;
      switch (e.key) {
        case 'ArrowDown':
          e.preventDefault();
          setSelected((s) => Math.min(s + 1, filtered.length - 1));
          break;
        case 'ArrowUp':
          e.preventDefault();
          setSelected((s) => Math.max(s - 1, 0));
          break;
        case 'Enter':
          e.preventDefault();
          if (filtered[selected]) handleSelect(filtered[selected]);
          break;
        case 'Escape':
          e.preventDefault();
          onClose();
          break;
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [isOpen, filtered, selected, handleSelect, onClose]);

  if (!isOpen) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center pt-[15vh]">
      <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} />
      <div role="dialog" aria-modal="true" aria-label={`${serverLabel} terminal command palette`} className="relative z-10 w-full max-w-lg overflow-hidden rounded-xl border border-[var(--color-border)] bg-[var(--color-card)] shadow-2xl">
        {/* Search */}
        <div className="flex items-center gap-3 border-b border-[var(--color-border)] px-4 py-3">
          <Search className="size-4 shrink-0 text-[var(--color-muted-foreground)]" />
          <input
            ref={inputRef}
            aria-label={`Search ${serverLabel} terminal commands`}
            value={query}
            onChange={(e) => { setQuery(e.target.value); setSelected(0); }}
            placeholder="Search commands..."
            className="flex-1 bg-transparent text-sm text-[var(--color-foreground)] placeholder:text-[var(--color-muted-foreground)] focus:outline-none"
          />
          <span className="rounded-full border border-[var(--color-border)] bg-[var(--color-muted)] px-2 py-0.5 text-[10px] font-semibold text-[var(--color-muted-foreground)]">
            {serverLabel}
          </span>
          <kbd className="hidden rounded border border-[var(--color-border)] bg-[var(--color-muted)] px-1.5 py-0.5 text-xs text-[var(--color-muted-foreground)] sm:inline">ESC</kbd>
        </div>

        {/* Results */}
        <div ref={listRef} className="max-h-80 overflow-y-auto py-1">
          {filtered.length === 0 ? (
            <p className="px-4 py-8 text-center text-sm text-[var(--color-muted-foreground)]">No commands found</p>
          ) : (
            filtered.map((cmd, idx) => (
              <button
                key={cmd.id}
                onClick={() => handleSelect(cmd)}
                className={cn(
                  'flex w-full items-center gap-3 px-4 py-2.5 text-left transition-colors',
                  idx === selected
                    ? 'bg-[var(--color-accent)]'
                    : 'hover:bg-[var(--color-accent)]/50'
                )}
              >
                <Terminal className="size-3.5 shrink-0 text-[var(--color-muted-foreground)]" />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="truncate text-sm font-medium text-[var(--color-foreground)]">
                      {cmd.label}
                    </span>
                    <span className={cn('shrink-0 text-xs', CATEGORY_COLORS[cmd.category] ?? 'text-[var(--color-muted-foreground)]')}>
                      {cmd.category}
                    </span>
                  </div>
                  <p className="truncate text-xs text-[var(--color-muted-foreground)]">
                    <code>{cmd.command}</code>
                    {cmd.description && <span> — {cmd.description}</span>}
                  </p>
                </div>
                <ChevronRight className="size-3.5 shrink-0 text-[var(--color-muted-foreground)]" />
              </button>
            ))
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center gap-4 border-t border-[var(--color-border)] px-4 py-2">
          <div className="flex items-center gap-1 text-xs text-[var(--color-muted-foreground)]">
            <kbd className="rounded border border-[var(--color-border)] bg-[var(--color-muted)] px-1 py-0.5">↑↓</kbd>
            <span>navigate</span>
          </div>
          <div className="flex items-center gap-1 text-xs text-[var(--color-muted-foreground)]">
            <kbd className="rounded border border-[var(--color-border)] bg-[var(--color-muted)] px-1 py-0.5">↵</kbd>
            <span>run</span>
          </div>
          <span className="ml-auto text-[10px] text-[var(--color-muted-foreground)]">Runs immediately on {serverLabel}</span>
        </div>
      </div>
    </div>
  );
}
