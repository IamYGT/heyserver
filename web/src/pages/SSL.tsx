import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Shield,
  ShieldCheck,
  ShieldAlert,
  ShieldOff,
  RefreshCw,
  Plus,
  ChevronDown,
  ChevronUp,
  Calendar,
  Key,
  FileText,
  Hash,
  Clock,
  Loader2,
  Search,
} from 'lucide-react'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { Input } from '@/components/ui/input'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import type { SSLCertificate, SSLIssueRequest, SSLOperationResult } from '@/lib/types'
import { DependencyRemediation } from '@/components/DependencyRemediation'

// ─── Helpers ──────────────────────────────────────────────────────────────────

type ExpiryStatus = 'valid' | 'warning' | 'critical' | 'expired'

function getExpiryStatus(days: number): ExpiryStatus {
  if (days < 0) return 'expired'
  if (days < 7) return 'critical'
  if (days < 30) return 'warning'
  return 'valid'
}

function formatDate(iso: string): string {
  return new Date(iso).toLocaleDateString('en-GB', {
    day: '2-digit',
    month: 'short',
    year: 'numeric',
  })
}

const statusStyles: Record<ExpiryStatus, { badge: string; border: string; icon: string }> = {
  valid: {
    badge: 'bg-green-500/10 text-green-400 border-green-500/20',
    border: 'border-zinc-800 hover:border-green-500/30',
    icon: 'text-green-400',
  },
  warning: {
    badge: 'bg-yellow-500/10 text-yellow-400 border-yellow-500/20',
    border: 'border-yellow-500/20 hover:border-yellow-500/40',
    icon: 'text-yellow-400',
  },
  critical: {
    badge: 'bg-red-500/10 text-red-400 border-red-500/20',
    border: 'border-red-500/30 hover:border-red-500/50',
    icon: 'text-red-400',
  },
  expired: {
    badge: 'bg-red-600/20 text-red-400 border-red-500/30',
    border: 'border-red-600/40 hover:border-red-500/60',
    icon: 'text-red-500',
  },
}

const StatusIcon = ({ status }: { status: ExpiryStatus }) => {
  const cls = `w-5 h-5 ${statusStyles[status].icon}`
  if (status === 'valid') return <ShieldCheck className={cls} />
  if (status === 'expired') return <ShieldOff className={cls} />
  return <ShieldAlert className={cls} />
}

// ─── Cert Card ────────────────────────────────────────────────────────────────

interface CertCardProps {
  cert: SSLCertificate
  onRenew: (domain: string) => void
  isRenewing: boolean
  renewDisabled: boolean
}

function CertCard({ cert, onRenew, isRenewing, renewDisabled }: CertCardProps) {
  const [expanded, setExpanded] = useState(false)
  const [sansExpanded, setSansExpanded] = useState(false)
  const status = getExpiryStatus(cert.daysRemaining)
  const styles = statusStyles[status]

  return (
    <Card
      className={`bg-zinc-900 transition-colors cursor-pointer ${styles.border}`}
      onClick={() => setExpanded((v) => !v)}
    >
      <CardContent className="p-5">
        {/* Header row */}
        <div className="flex items-start justify-between gap-3">
          <div className="flex items-center gap-3 min-w-0">
            <div
              className={`w-9 h-9 rounded-lg flex items-center justify-center flex-shrink-0 ${
                status === 'valid'
                  ? 'bg-green-500/10 border border-green-500/20'
                  : status === 'warning'
                  ? 'bg-yellow-500/10 border border-yellow-500/20'
                  : 'bg-red-500/10 border border-red-500/20'
              }`}
            >
              <StatusIcon status={status} />
            </div>
            <div className="min-w-0">
              <div className="flex items-center gap-2 flex-wrap">
                <span className="text-white font-semibold text-sm truncate">
                  {cert.domain}
                </span>
                {cert.isWildcard && (
                  <Badge className="bg-purple-500/10 text-purple-400 border-purple-500/20 text-xs">
                    Wildcard
                  </Badge>
                )}
                <Badge className={`text-xs ${styles.badge}`}>
                  {status === 'expired'
                    ? 'Expired'
                    : `${cert.daysRemaining}d left`}
                </Badge>
              </div>
              <p className="text-zinc-500 text-xs mt-0.5 truncate">{cert.issuer}</p>
            </div>
          </div>

          <div className="flex items-center gap-2 flex-shrink-0">
            <Button
              size="sm"
              variant="outline"
              className="border-zinc-700 text-zinc-300 hover:text-white hover:bg-zinc-800 text-xs h-7 px-2.5"
              disabled={isRenewing || renewDisabled}
              onClick={(e) => {
                e.stopPropagation()
                onRenew(cert.domain)
              }}
            >
              {isRenewing ? (
                <Loader2 className="w-3 h-3 animate-spin" />
              ) : (
                <RefreshCw className="w-3 h-3 mr-1" />
              )}
              Renew
            </Button>
            {expanded ? (
              <ChevronUp className="w-4 h-4 text-zinc-500" />
            ) : (
              <ChevronDown className="w-4 h-4 text-zinc-500" />
            )}
          </div>
        </div>

        {/* Expiry summary bar */}
        <div className="mt-3 flex items-center gap-4 text-xs text-zinc-500">
          <span className="flex items-center gap-1">
            <Calendar className="w-3 h-3" />
            <span className="text-zinc-400">{formatDate(cert.notBefore)}</span>
          </span>
          <span className="text-zinc-700">→</span>
          <span className="flex items-center gap-1">
            <Clock className="w-3 h-3" />
            <span
              className={
                status === 'valid'
                  ? 'text-green-400'
                  : status === 'warning'
                  ? 'text-yellow-400'
                  : 'text-red-400'
              }
            >
              {formatDate(cert.notAfter)}
            </span>
          </span>
        </div>

        {/* Expanded detail */}
        {expanded && (
          <div
            className="mt-4 pt-4 border-t border-zinc-800 space-y-3"
            onClick={(e) => e.stopPropagation()}
          >
            {/* Subject / Serial */}
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 text-xs">
              <div className="flex items-start gap-2">
                <Shield className="w-3.5 h-3.5 text-zinc-500 mt-0.5 flex-shrink-0" />
                <div className="min-w-0">
                  <p className="text-zinc-500 mb-0.5">Subject</p>
                  <p className="text-zinc-300 font-mono break-all">{cert.subject}</p>
                </div>
              </div>
              <div className="flex items-start gap-2">
                <Hash className="w-3.5 h-3.5 text-zinc-500 mt-0.5 flex-shrink-0" />
                <div className="min-w-0">
                  <p className="text-zinc-500 mb-0.5">Serial</p>
                  <p className="text-zinc-300 font-mono break-all">{cert.serial}</p>
                </div>
              </div>
            </div>

            {/* File paths */}
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 text-xs">
              <div className="flex items-start gap-2">
                <FileText className="w-3.5 h-3.5 text-zinc-500 mt-0.5 flex-shrink-0" />
                <div className="min-w-0">
                  <p className="text-zinc-500 mb-0.5">Certificate</p>
                  <p className="text-zinc-400 font-mono break-all">{cert.certPath}</p>
                </div>
              </div>
              <div className="flex items-start gap-2">
                <Key className="w-3.5 h-3.5 text-zinc-500 mt-0.5 flex-shrink-0" />
                <div className="min-w-0">
                  <p className="text-zinc-500 mb-0.5">Private Key</p>
                  <p className="text-zinc-400 font-mono break-all">{cert.keyPath}</p>
                </div>
              </div>
            </div>

            {/* SANs */}
            {cert.sans.length > 0 && (
              <div className="text-xs">
                <button
                  className="flex items-center gap-1 text-zinc-500 hover:text-zinc-300 transition-colors mb-2"
                  onClick={() => setSansExpanded((v) => !v)}
                >
                  <Shield className="w-3.5 h-3.5" />
                  <span>
                    SANs ({cert.sans.length})
                  </span>
                  {sansExpanded ? (
                    <ChevronUp className="w-3 h-3" />
                  ) : (
                    <ChevronDown className="w-3 h-3" />
                  )}
                </button>
                {sansExpanded && (
                  <div className="flex flex-wrap gap-1.5">
                    {cert.sans.map((san) => (
                      <span
                        key={san}
                        className="bg-zinc-800 text-zinc-300 font-mono px-2 py-0.5 rounded text-xs border border-zinc-700"
                      >
                        {san}
                      </span>
                    ))}
                  </div>
                )}
              </div>
            )}
          </div>
        )}
      </CardContent>
    </Card>
  )
}

// ─── Issue Dialog ─────────────────────────────────────────────────────────────

interface IssueDialogProps {
  open: boolean
  onClose: () => void
  onSubmit: (req: SSLIssueRequest) => void
  isPending: boolean
  httpReady: boolean
  dnsReady: boolean
  dnsCredentialsConfigured: boolean
}

function IssueDialog({ open, onClose, onSubmit, isPending, httpReady, dnsReady, dnsCredentialsConfigured }: IssueDialogProps) {
  const [domain, setDomain] = useState('')
  const [challengeType, setChallengeType] = useState<'http-01' | 'dns-01'>('http-01')
  const [email, setEmail] = useState('')
  const challengeReady = challengeType === 'http-01' ? httpReady : dnsReady

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    if (!domain.trim() || !email.trim()) return
    onSubmit({ domain: domain.trim(), challengeType, email: email.trim() })
  }

  const handleClose = () => {
    if (isPending) return
    setDomain('')
    setEmail('')
    setChallengeType('http-01')
    onClose()
  }

  return (
    <Dialog open={open} onOpenChange={handleClose}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-white">
            <Shield className="w-5 h-5 text-blue-400" />
            Issue New Certificate
          </DialogTitle>
        </DialogHeader>

        <form onSubmit={handleSubmit} className="space-y-4 mt-2">
          <div className="space-y-1.5">
            <Label className="text-zinc-300 text-sm">Domain</Label>
            <Input
              value={domain}
              onChange={(e) => setDomain(e.target.value)}
              placeholder="example.com or *.example.com"
              className="bg-zinc-800 border-zinc-700 text-white placeholder:text-zinc-600 focus:border-blue-500"
              disabled={isPending}
              required
            />
          </div>

          <div className="space-y-1.5">
            <Label className="text-zinc-300 text-sm">Email</Label>
            <Input
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="admin@example.com"
              className="bg-zinc-800 border-zinc-700 text-white placeholder:text-zinc-600 focus:border-blue-500"
              disabled={isPending}
              required
            />
          </div>

          <div className="space-y-1.5">
            <Label className="text-zinc-300 text-sm">Challenge Type</Label>
            <div className="grid grid-cols-2 gap-2">
              {(['http-01', 'dns-01'] as const).map((type) => (
                <button
                  key={type}
                  type="button"
                  disabled={isPending || (type === 'http-01' ? !httpReady : !dnsReady)}
                  onClick={() => setChallengeType(type)}
                  className={`px-3 py-2 rounded-lg text-sm font-medium border transition-colors ${
                    challengeType === type
                      ? 'bg-blue-600/20 border-blue-500/50 text-blue-400'
                      : 'bg-zinc-800 border-zinc-700 text-zinc-400 hover:text-white hover:bg-zinc-700'
                  }`}
                >
                  {type.toUpperCase()}
                </button>
              ))}
            </div>
            <p className="text-xs text-zinc-500">
              {challengeType === 'http-01'
                ? httpReady
                  ? 'Serves a token over HTTP through the installed nginx plugin — the domain must resolve to this server.'
                  : 'HTTP-01 requires the Certbot nginx authenticator plugin.'
                : dnsReady
                  ? 'Adds a TXT record through the configured Cloudflare plugin — required for wildcard certificates.'
                  : dnsCredentialsConfigured
                    ? 'DNS-01 requires a readable protected credential file and the Certbot Cloudflare plugin.'
                    : 'DNS-01 requires the Certbot Cloudflare plugin and HSERVER_CERTBOT_CLOUDFLARE_CREDENTIALS.'}
            </p>
          </div>

          <DialogFooter className="gap-2 pt-2">
            <Button
              type="button"
              variant="outline"
              className="border-zinc-700 text-zinc-300 hover:text-white hover:bg-zinc-800"
              onClick={handleClose}
              disabled={isPending}
            >
              Cancel
            </Button>
            <Button
              type="submit"
              className="bg-blue-600 hover:bg-blue-500 text-white"
              disabled={isPending || !challengeReady || !domain.trim() || !email.trim()}
            >
              {isPending ? (
                <>
                  <Loader2 className="w-4 h-4 mr-2 animate-spin" />
                  Issuing...
                </>
              ) : (
                <>
                  <Shield className="w-4 h-4 mr-2" />
                  Issue Certificate
                </>
              )}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ─── Main Page ────────────────────────────────────────────────────────────────

interface SSLReadinessStatus {
  available: boolean
  installed: boolean
  state: 'healthy' | 'certbot-missing' | 'unavailable'
  version?: string
  plugins: string[]
  pluginsAvailable: boolean
  nginxPlugin: boolean
  dnsCloudflarePlugin: boolean
  dnsCloudflareCredentialsConfigured: boolean
  dnsCloudflareCredentialsReadable: boolean
  error?: string
  pluginError?: string
}

export default function SSL() {
  const queryClient = useQueryClient()
  const [search, setSearch] = useState('')
  const [issueOpen, setIssueOpen] = useState(false)
  const [renewingDomain, setRenewingDomain] = useState<string | null>(null)

  const statusQuery = useQuery<SSLReadinessStatus>({
    queryKey: ['ssl-status'],
    queryFn: () => api.get<SSLReadinessStatus>('/ssl/status'),
  })
  const sslStatus = statusQuery.data
  const certbotReady = sslStatus?.available === true
  const httpReady = certbotReady && sslStatus.pluginsAvailable && sslStatus.nginxPlugin
  const dnsReady = certbotReady && sslStatus.pluginsAvailable && sslStatus.dnsCloudflarePlugin && sslStatus.dnsCloudflareCredentialsReadable
  const issueReady = httpReady || dnsReady

  const certificatesQuery = useQuery<SSLCertificate[]>({
    queryKey: ['ssl-certificates'],
    queryFn: () => api.get<SSLCertificate[]>('/ssl/certificates'),
  })
  const certs = certificatesQuery.data ?? []

  const renewMutation = useMutation({
    mutationFn: (domain: string) =>
      api.post<SSLOperationResult>(`/ssl/renew/${encodeURIComponent(domain)}`),
    onMutate: (domain) => setRenewingDomain(domain),
    onSuccess: (result) => {
      queryClient.invalidateQueries({ queryKey: ['ssl-certificates'] })
      toast.success(result.message || 'Certificate renewal triggered')
    },
    onError: () => toast.error('Failed to trigger renewal'),
    onSettled: () => setRenewingDomain(null),
  })

  const issueMutation = useMutation({
    mutationFn: (req: SSLIssueRequest) =>
      api.post<SSLOperationResult>('/ssl/issue', req),
    onSuccess: (result) => {
      queryClient.invalidateQueries({ queryKey: ['ssl-certificates'] })
      toast.success(result.message || 'Certificate issued successfully')
      setIssueOpen(false)
    },
    onError: () => toast.error('Failed to issue certificate'),
  })

  const filtered = certs.filter((c) =>
    c.domain.toLowerCase().includes(search.toLowerCase()),
  )

  // Summary counts
  const counts = {
    total: certs.length,
    valid: certs.filter((c) => getExpiryStatus(c.daysRemaining) === 'valid').length,
    warning: certs.filter((c) => getExpiryStatus(c.daysRemaining) === 'warning').length,
    critical: certs.filter(
      (c) => ['critical', 'expired'].includes(getExpiryStatus(c.daysRemaining)),
    ).length,
  }

  return (
    <div className="space-y-6">
      {/* Page header */}
      <div className="flex items-center justify-between gap-4 flex-wrap">
        <div>
          <h2 className="text-white text-xl font-bold">SSL Certificates</h2>
          <p className="text-zinc-500 text-sm mt-0.5">
            {certificatesQuery.isLoading
              ? 'Loading certificates...'
              : certificatesQuery.isError
                ? 'Certificate inventory unavailable'
              : `${counts.total} certificates managed`}
          </p>
        </div>
        <Button
          className="bg-blue-600 hover:bg-blue-500 text-white"
          onClick={() => setIssueOpen(true)}
          disabled={certificatesQuery.isLoading || certificatesQuery.isError || statusQuery.isLoading || statusQuery.isError || !issueReady}
        >
          <Plus className="w-4 h-4 mr-2" />
          Issue New Certificate
        </Button>
      </div>

      {statusQuery.isError && (
        <DependencyRemediation
          title="Certbot status API is unavailable"
          summary="Existing certificate files may still be visible, but issuance and renewal stay paused until Certbot readiness can be observed."
          error={statusQuery.error.message}
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
          steps={[
            'Run the packaged Heyserver doctor and inspect Heyserver service logs.',
            <>Verify <code>certbot --version</code> with the Heyserver service identity.</>,
            'Retry detection after the API and local command are ready.',
          ]}
        />
      )}

      {!statusQuery.isError && sslStatus?.state === 'certbot-missing' && (
        <DependencyRemediation
          title="Certbot is not installed"
          summary="Certificate files remain observable, but Heyserver cannot issue or renew certificates until the local Certbot client is available."
          state="not-configured"
          error={sslStatus.error}
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
          steps={[
            <>Install <code>certbot</code> and <code>python3-certbot-nginx</code> from supported Ubuntu repositories.</>,
            <>Verify <code>certbot --version</code> and <code>certbot plugins</code>.</>,
            'Retry detection; installation never issues a certificate automatically.',
          ]}
        />
      )}

      {!statusQuery.isError && sslStatus?.state === 'unavailable' && (
        <DependencyRemediation
          title="Certbot is unavailable"
          summary="The configured Certbot command could not be executed. Issuance and renewal remain paused while certificate files stay observable."
          error={sslStatus.error}
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
          steps={[
            <>Verify <code>HSERVER_CERTBOT_BIN</code> and run its <code>--version</code> command.</>,
            'Inspect Heyserver service permissions and logs without exposing credentials.',
            'Retry detection after correcting the command error.',
          ]}
        />
      )}

      {!statusQuery.isError && certbotReady && !sslStatus?.pluginsAvailable && (
        <DependencyRemediation
          title="Certbot plugin inventory is unavailable"
          summary="Renewal remains available, but new certificate issuance is paused because Heyserver cannot confirm a supported authenticator."
          error={sslStatus?.pluginError}
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
          steps={[
            <>Run <code>certbot plugins</code> with the Heyserver service identity.</>,
            'Install the nginx or Cloudflare DNS authenticator from supported repositories.',
            'Retry detection after plugin inventory succeeds.',
          ]}
        />
      )}

      {!statusQuery.isError && certbotReady && sslStatus?.pluginsAvailable && !sslStatus.nginxPlugin && !sslStatus.dnsCloudflarePlugin && (
        <DependencyRemediation
          title="No supported Certbot authenticator is installed"
          summary="Certbot can renew existing certificates, but Heyserver needs the nginx or Cloudflare DNS plugin to issue a new one."
          state="not-configured"
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
          steps={[
            <>Install <code>python3-certbot-nginx</code> for HTTP-01.</>,
            <>Or install <code>python3-certbot-dns-cloudflare</code> for DNS-01.</>,
            'Verify the selected authenticator with certbot plugins, then retry.',
          ]}
        />
      )}

      {!statusQuery.isError && certbotReady && sslStatus?.pluginsAvailable && !sslStatus.nginxPlugin && sslStatus.dnsCloudflarePlugin && !sslStatus.dnsCloudflareCredentialsConfigured && (
        <DependencyRemediation
          title="DNS-01 credentials are not configured"
          summary="The Cloudflare authenticator is installed, but no supported issuance method is ready until its installation-owned credential file is configured."
          state="not-configured"
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
          steps={[
            'Create a least-privileged Cloudflare DNS token and store it in a Certbot INI file outside Git.',
            <>Set file mode to <code>0600</code> and point <code>HSERVER_CERTBOT_CLOUDFLARE_CREDENTIALS</code> to its absolute path.</>,
            'Restart Heyserver to load the protected environment, then retry detection.',
          ]}
        />
      )}

      {!statusQuery.isError && sslStatus?.dnsCloudflarePlugin && sslStatus.dnsCloudflareCredentialsConfigured && !sslStatus.dnsCloudflareCredentialsReadable && (
        <DependencyRemediation
          title="DNS-01 credentials need attention"
          summary="HTTP-01 remains available when its plugin is ready, but Cloudflare DNS issuance is paused until the installation-owned credential file is readable and protected."
          state="not-configured"
          error={sslStatus.pluginError}
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
          steps={[
            <>Verify <code>HSERVER_CERTBOT_CLOUDFLARE_CREDENTIALS</code> references an absolute regular file.</>,
            <>Set file mode to <code>0600</code> and keep the token value out of logs and Git.</>,
            'Retry detection after the Heyserver service identity can read the file.',
          ]}
        />
      )}

      {/* Stats row */}
      {!certificatesQuery.isLoading && !certificatesQuery.isError && certs.length > 0 && (
        <div className="grid grid-cols-3 gap-3">
          <div className="bg-zinc-900 border border-zinc-800 rounded-lg px-4 py-3 flex items-center gap-3">
            <ShieldCheck className="w-5 h-5 text-green-400 flex-shrink-0" />
            <div>
              <p className="text-white font-semibold text-lg leading-none">{counts.valid}</p>
              <p className="text-zinc-500 text-xs mt-0.5">Valid</p>
            </div>
          </div>
          <div className="bg-zinc-900 border border-zinc-800 rounded-lg px-4 py-3 flex items-center gap-3">
            <ShieldAlert className="w-5 h-5 text-yellow-400 flex-shrink-0" />
            <div>
              <p className="text-white font-semibold text-lg leading-none">{counts.warning}</p>
              <p className="text-zinc-500 text-xs mt-0.5">Expiring soon</p>
            </div>
          </div>
          <div className="bg-zinc-900 border border-zinc-800 rounded-lg px-4 py-3 flex items-center gap-3">
            <ShieldOff className="w-5 h-5 text-red-400 flex-shrink-0" />
            <div>
              <p className="text-white font-semibold text-lg leading-none">{counts.critical}</p>
              <p className="text-zinc-500 text-xs mt-0.5">Critical / Expired</p>
            </div>
          </div>
        </div>
      )}

      {/* Search */}
      <div className="relative">
        <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-zinc-500" />
        <input
          type="text"
          placeholder="Search certificates..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="w-full bg-zinc-900 border border-zinc-800 text-white placeholder:text-zinc-600 rounded-lg py-2 pl-9 pr-4 text-sm focus:outline-none focus:border-blue-500 transition-colors"
        />
      </div>

      {/* Certificate grid */}
      {certificatesQuery.isLoading ? (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-28 w-full bg-zinc-900" />
          ))}
        </div>
      ) : certificatesQuery.isError ? (
        <DependencyRemediation
          title="Certificate inventory is unavailable"
          summary="Heyserver could not read the Let's Encrypt certificate inventory. Issuance and renewal controls remain paused to avoid acting on unknown state."
          error={certificatesQuery.error.message}
          retry={() => { void certificatesQuery.refetch() }}
          retrying={certificatesQuery.isFetching}
          steps={[
            <>Verify read access to <code>/etc/letsencrypt/live</code>.</>,
            'Inspect Heyserver service logs for unreadable or malformed certificate files.',
            'Retry after certificate inventory access is restored.',
          ]}
        />
      ) : filtered.length > 0 ? (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          {filtered.map((cert) => (
            <CertCard
              key={cert.domain}
              cert={cert}
              onRenew={(domain) => renewMutation.mutate(domain)}
              isRenewing={renewingDomain === cert.domain}
              renewDisabled={!certbotReady || certificatesQuery.isError}
            />
          ))}
        </div>
      ) : (
        <div className="flex flex-col items-center justify-center py-20 text-zinc-600">
          <Shield className="w-10 h-10 mb-3 opacity-50" />
          <p className="text-sm">
            {search
              ? 'No certificates match your search'
              : 'No SSL certificates found'}
          </p>
        </div>
      )}

      <IssueDialog
        open={issueOpen}
        onClose={() => setIssueOpen(false)}
        onSubmit={(req) => issueMutation.mutate(req)}
        isPending={issueMutation.isPending}
        httpReady={httpReady}
        dnsReady={dnsReady}
        dnsCredentialsConfigured={sslStatus?.dnsCloudflareCredentialsConfigured === true}
      />
    </div>
  )
}
