import { useState } from 'react';
import { NavLink, Outlet } from 'react-router-dom';
import {
  LayoutDashboard,
  Globe,
  Server,
  Code,
  Shield,
  Play,
  Database,
  Folder,
  Lock,
  Archive,
  Terminal,
  Activity,
  Users,
  Settings,
  ScrollText,
  ChevronLeft,
  ChevronRight,
} from 'lucide-react';
import { cn } from '@/lib/utils';

const navItems = [
  { to: '/dashboard', label: 'Dashboard', icon: LayoutDashboard },
  { to: '/domains', label: 'Domains', icon: Globe },
  { to: '/nginx', label: 'Nginx', icon: Server },
  { to: '/php-fpm', label: 'PHP-FPM', icon: Code },
  { to: '/ssl', label: 'SSL Certificates', icon: Shield },
  { to: '/pm2', label: 'PM2 Processes', icon: Play },
  { to: '/databases', label: 'Databases', icon: Database },
  { to: '/files', label: 'File Manager', icon: Folder },
  { to: '/firewall', label: 'Firewall', icon: Lock },
  { to: '/backups', label: 'Backups', icon: Archive },
  { to: '/terminal', label: 'Terminal', icon: Terminal },
  { to: '/monitoring', label: 'Monitoring', icon: Activity },
  { to: '/users', label: 'Users', icon: Users },
  { to: '/settings', label: 'Settings', icon: Settings },
  { to: '/audit-log', label: 'Audit Log', icon: ScrollText },
];

export function MainLayout() {
  const [collapsed, setCollapsed] = useState(false);

  return (
    <div className="flex h-full overflow-hidden bg-[var(--color-background)]">
      {/* Sidebar */}
      <aside
        className={cn(
          'flex h-full flex-col border-r border-[var(--color-sidebar-border)] bg-[var(--color-sidebar)] transition-all duration-300',
          collapsed ? 'w-16' : 'w-60'
        )}
      >
        {/* Logo */}
        <div
          className={cn(
            'flex h-14 shrink-0 items-center border-b border-[var(--color-sidebar-border)] px-4',
            collapsed ? 'justify-center' : 'gap-2'
          )}
        >
          <div className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-blue-600 text-white">
            <Server className="size-4" />
          </div>
          {!collapsed && (
            <span className="text-sm font-semibold text-[var(--color-sidebar-foreground)]">
              HServer Panel
            </span>
          )}
        </div>

        {/* Nav items */}
        <nav className="flex-1 overflow-y-auto py-2 scrollbar-hide">
          {navItems.map(({ to, label, icon: Icon }) => (
            <NavLink
              key={to}
              to={to}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-3 px-4 py-2 text-sm transition-colors',
                  collapsed ? 'justify-center' : '',
                  isActive
                    ? 'bg-[var(--color-accent)] text-[var(--color-accent-foreground)] font-medium'
                    : 'text-[var(--color-muted-foreground)] hover:bg-[var(--color-accent)]/50 hover:text-[var(--color-sidebar-foreground)]'
                )
              }
              title={collapsed ? label : undefined}
            >
              <Icon className="size-4 shrink-0" />
              {!collapsed && <span className="truncate">{label}</span>}
            </NavLink>
          ))}
        </nav>

        {/* Collapse toggle */}
        <button
          onClick={() => setCollapsed((c) => !c)}
          className={cn(
            'flex h-10 items-center border-t border-[var(--color-sidebar-border)] px-4 text-xs text-[var(--color-muted-foreground)] transition-colors hover:bg-[var(--color-accent)]/50',
            collapsed ? 'justify-center' : 'gap-2'
          )}
        >
          {collapsed ? (
            <ChevronRight className="size-4" />
          ) : (
            <>
              <ChevronLeft className="size-4" />
              <span>Collapse</span>
            </>
          )}
        </button>
      </aside>

      {/* Main content */}
      <div className="flex flex-1 flex-col overflow-hidden">
        {/* Header */}
        <header className="flex h-14 shrink-0 items-center border-b border-[var(--color-border)] bg-[var(--color-card)] px-4">
          <span className="text-sm font-medium text-[var(--color-foreground)]">HServer Panel</span>
        </header>

        {/* Page content */}
        <main className="flex flex-1 overflow-hidden">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
