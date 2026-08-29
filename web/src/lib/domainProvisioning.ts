export type DomainDNSStatus = 'not_configured' | 'unavailable' | 'healthy'
export type LocalDomainType = 'php' | 'proxy' | 'static'
export type SupportedDomainPHPVersion = '7.4' | '8.0' | '8.1' | '8.2' | '8.3' | '8.4' | '8.5'

export interface DomainCreateRequest {
  domain: string
  type?: LocalDomainType
  phpVersion?: SupportedDomainPHPVersion
  proxyPort?: number
  webRoot?: string
  fpmPreset?: 'low' | 'medium' | 'high'
  spaMode?: boolean
  wwwRedirect?: boolean
  issueSSL?: boolean
  sslEmail?: string
  existingCertName?: string
  createDnsRecord?: boolean
  pm2_app?: string
  pm2_script?: string
  pm2_cwd?: string
  pm2_port?: number
  nodeEnv?: 'production' | 'development'
  isolatedLinuxUser?: boolean
}

export interface DomainDNSCapability {
  provider: string
  status: DomainDNSStatus
  origin?: string
  recordType?: 'A' | 'AAAA'
  proxied: boolean
  message: string
}

export interface DomainProvisioningCapabilities {
  vhostsRoot: string
  nginxSitesAvailable: string
  nginxSitesEnabled: string
  nginxSnippetsDir: string
  dns: DomainDNSCapability
}

// Site roots are installation-owned configuration. Keep the frontend empty
// until the API reports a detected/configured absolute path.
export const DEFAULT_VHOSTS_ROOT = ''

export function normalizeVhostsRoot(root?: string): string {
  const normalized = root?.trim().replace(/\/+$/, '')
  return normalized || DEFAULT_VHOSTS_ROOT
}

function detectParent(domain: string): string | null {
  const parts = domain.trim().split('.')
  return parts.length >= 3 ? parts.slice(1).join('.') : null
}

export function autoWebRoot(domain: string, root?: string): string {
  const base = normalizeVhostsRoot(root)
  const normalizedDomain = domain.trim().toLowerCase()
  if (!normalizedDomain) return base ? `${base}/` : ''
  if (!base) return ''
  const parent = detectParent(normalizedDomain)
  return parent
    ? `${base}/${parent}/${normalizedDomain}/public_html`
    : `${base}/${normalizedDomain}/public_html`
}

export function autoCwd(domain: string, root?: string): string {
  const base = normalizeVhostsRoot(root)
  const normalizedDomain = domain.trim().toLowerCase()
  if (!normalizedDomain) return base ? `${base}/` : ''
  if (!base) return ''
  const parent = detectParent(normalizedDomain)
  return parent
    ? `${base}/${parent}/${normalizedDomain}`
    : `${base}/${normalizedDomain}`
}

export function dnsCapabilityDescription(dns: DomainDNSCapability): string {
  if (dns.status === 'healthy') {
    const mode = dns.proxied ? 'proxied' : 'DNS only'
    return `${dns.recordType ?? 'A/AAAA'} record → ${dns.origin} (${mode})`
  }
  const label = dns.status === 'unavailable' ? 'Unavailable' : 'Not configured'
  return `${label} — ${dns.message}`
}
