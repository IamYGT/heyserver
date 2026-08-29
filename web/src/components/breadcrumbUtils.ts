const ROUTE_LABELS: Record<string, string> = {
  '': 'Dashboard', domains: 'Domains', nginx: 'Nginx', ssl: 'SSL', php: 'PHP', pm2: 'PM2',
  monitoring: 'Monitoring', servers: 'Servers', uptime: 'Uptime', logs: 'Logs', mail: 'Mail',
  dns: 'DNS', cloudflare: 'Cloudflare', webmail: 'Webmail', firewall: 'Firewall', files: 'Files',
  disk: 'Disk', databases: 'Databases', backups: 'Backups', cron: 'Cron', docker: 'Docker',
  deploy: 'Deploy', terminal: 'Terminal', security: 'Security', notifications: 'Notifications',
  users: 'Users', audit: 'Audit Log', settings: 'Settings', about: 'About',
}

export interface Crumb { label: string; href: string; isLast: boolean }

export function buildCrumbs(pathname: string, homeHref = '/'): Crumb[] {
  const crumbs: Crumb[] = [{ label: 'Home', href: homeHref, isLast: false }]
  if (pathname === homeHref) {
    crumbs[0].isLast = true
    return crumbs
  }

  const segments = pathname.split('/').filter(Boolean)
  segments.forEach((segment, index) => {
    crumbs.push({
      label: ROUTE_LABELS[segment] ?? capitalize(segment),
      href: `/${segments.slice(0, index + 1).join('/')}`,
      isLast: index === segments.length - 1,
    })
  })
  return crumbs
}

function capitalize(value: string): string {
  return value.charAt(0).toUpperCase() + value.slice(1).replace(/-/g, ' ')
}
