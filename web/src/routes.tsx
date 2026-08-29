/* eslint-disable react-refresh/only-export-components -- intentional lazy route registry and route metadata */
import { lazy, type ComponentType, type LazyExoticComponent } from 'react'

// Lazy-loaded page chunks
export const Dashboard = lazy(() => import('@/pages/Dashboard'))
export const Domains = lazy(() => import('@/pages/Domains'))
export const DomainDetail = lazy(() => import('@/pages/DomainDetail'))
export const Nginx = lazy(() => import('@/pages/Nginx'))
export const SSL = lazy(() => import('@/pages/SSL'))
export const PHP = lazy(() => import('@/pages/PHP'))
export const PM2 = lazy(() => import('@/pages/PM2'))
export const Monitoring = lazy(() => import('@/pages/Monitoring'))
export const Servers = lazy(() => import('@/pages/Servers'))
export const Mail = lazy(() => import('@/pages/Mail'))
export const DNS = lazy(() => import('@/pages/DNS'))
export const Cloudflare = lazy(() => import('@/pages/Cloudflare'))
export const Webmail = lazy(() => import('@/pages/Webmail'))
export const Firewall = lazy(() => import('@/pages/Firewall'))
export const Files = lazy(() => import('@/pages/Files'))
export const DatabasePage = lazy(() => import('@/pages/Database'))
export const Backups = lazy(() => import('@/pages/Backups'))
export const Cron = lazy(() => import('@/pages/Cron'))
export const Docker = lazy(() => import('@/pages/Docker'))
export const Deploy = lazy(() => import('@/pages/Deploy'))
export const TerminalPage = lazy(() =>
  import('@/pages/terminal').then((m) => ({ default: m.TerminalPage })),
)
export const UsersPage = lazy(() => import('@/pages/Users'))
export const AuditPage = lazy(() => import('@/pages/Audit'))
export const SettingsPage = lazy(() => import('@/pages/Settings'))
export const Security = lazy(() => import('@/pages/Security'))
export const Logs = lazy(() => import('@/pages/Logs'))
export const Notifications = lazy(() => import('@/pages/Notifications'))
export const Uptime = lazy(() => import('@/pages/Uptime'))
export const DiskManagement = lazy(() => import('@/pages/DiskManagement'))
export const AboutPage = lazy(() => import('@/pages/About'))
export const DeveloperAPI = lazy(() => import('@/pages/DeveloperAPI'))
export const Integrations = lazy(() => import('@/pages/Integrations'))
export const NotFound = lazy(() => import('@/pages/NotFound'))

/** Canonical app route paths — keep in sync with main.tsx Route declarations */
export const ROUTE_PATHS = {
  login: '/login',
  onboarding: '/onboarding',
  dashboard: '/',
  domains: '/domains',
  domainDetail: '/domains/:name',
  nginx: '/nginx',
  ssl: '/ssl',
  php: '/php',
  pm2: '/pm2',
  monitoring: '/monitoring',
  servers: '/servers',
  uptime: '/uptime',
  mail: '/mail',
  dns: '/dns',
  cloudflare: '/cloudflare',
  webmail: '/webmail',
  firewall: '/firewall',
  files: '/files',
  disk: '/disk',
  databases: '/databases',
  backups: '/backups',
  cron: '/cron',
  docker: '/docker',
  deploy: '/deploy',
  terminal: '/terminal',
  security: '/security',
  notifications: '/notifications',
  logs: '/logs',
  users: '/users',
  audit: '/audit',
  settings: '/settings',
  about: '/about',
  developerAPI: '/developer/api',
  integrations: '/integrations',
  notFound: '*',
} as const

export type RoutePath = (typeof ROUTE_PATHS)[keyof typeof ROUTE_PATHS]

/** Paths required for core panel navigation and smoke routing checks */
export const CRITICAL_ROUTE_PATHS = [
  ROUTE_PATHS.login,
  ROUTE_PATHS.dashboard,
  ROUTE_PATHS.servers,
  ROUTE_PATHS.domains,
  ROUTE_PATHS.backups,
  ROUTE_PATHS.settings,
  ROUTE_PATHS.terminal,
  ROUTE_PATHS.users,
] as const satisfies readonly RoutePath[]

/** Single source for protected layout routes — consumed by main.tsx */
export type ProtectedRouteEntry = {
  path: string
  Component: LazyExoticComponent<ComponentType>
}

export const PROTECTED_ROUTE_ENTRIES: ProtectedRouteEntry[] = [
  { path: ROUTE_PATHS.dashboard, Component: Dashboard },
  { path: ROUTE_PATHS.domains, Component: Domains },
  { path: ROUTE_PATHS.domainDetail, Component: DomainDetail },
  { path: ROUTE_PATHS.nginx, Component: Nginx },
  { path: ROUTE_PATHS.ssl, Component: SSL },
  { path: ROUTE_PATHS.php, Component: PHP },
  { path: ROUTE_PATHS.pm2, Component: PM2 },
  { path: ROUTE_PATHS.monitoring, Component: Monitoring },
  { path: ROUTE_PATHS.servers, Component: Servers },
  { path: ROUTE_PATHS.uptime, Component: Uptime },
  { path: ROUTE_PATHS.mail, Component: Mail },
  { path: ROUTE_PATHS.dns, Component: DNS },
  { path: ROUTE_PATHS.cloudflare, Component: Cloudflare },
  { path: ROUTE_PATHS.webmail, Component: Webmail },
  { path: ROUTE_PATHS.firewall, Component: Firewall },
  { path: ROUTE_PATHS.files, Component: Files },
  { path: ROUTE_PATHS.disk, Component: DiskManagement },
  { path: ROUTE_PATHS.databases, Component: DatabasePage },
  { path: ROUTE_PATHS.backups, Component: Backups },
  { path: ROUTE_PATHS.cron, Component: Cron },
  { path: ROUTE_PATHS.docker, Component: Docker },
  { path: ROUTE_PATHS.deploy, Component: Deploy },
  { path: ROUTE_PATHS.terminal, Component: TerminalPage },
  { path: ROUTE_PATHS.security, Component: Security },
  { path: ROUTE_PATHS.notifications, Component: Notifications },
  { path: ROUTE_PATHS.logs, Component: Logs },
  { path: ROUTE_PATHS.users, Component: UsersPage },
  { path: ROUTE_PATHS.audit, Component: AuditPage },
  { path: ROUTE_PATHS.settings, Component: SettingsPage },
  { path: ROUTE_PATHS.about, Component: AboutPage },
  { path: ROUTE_PATHS.developerAPI, Component: DeveloperAPI },
  { path: ROUTE_PATHS.integrations, Component: Integrations },
]
