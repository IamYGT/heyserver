import { useQuery } from '@tanstack/react-query'
import { ExternalLink, Mail, Server, Shield, Info, Settings2, Copy } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import { DependencyRemediation } from '@/components/DependencyRemediation'
import { api } from '@/lib/api'
import {
  isMailSettingsConfigured,
  resolveMailSettings,
  validateMailSettings,
  type MailSettings,
} from '@/lib/mailSettings'
import {
  INTEGRATION_NOT_CONFIGURED,
  INTEGRATION_UNAVAILABLE,
  integrationStateFromObservation,
  integrationStatePresentation,
  normalizeIntegrationState,
  type IntegrationState,
} from '@/lib/integrationState'
import { toast } from 'sonner'

interface ServerInfoRow {
  label: string
  value: string
  badge?: string
}

interface ClientGuide {
  name: string
  steps: string[]
}

interface MailSettingsResponse {
  [key: string]: string
}

const persistedMailSettingKeys = [
  'webmail_url',
  'mail_admin_url',
  'mail_server_host',
  'mail_imap_port',
  'mail_smtp_starttls_port',
  'mail_smtp_ssl_port',
] as const

function hasCompleteValidatedMailSettings(raw: MailSettingsResponse): boolean {
  if (!persistedMailSettingKeys.every((key) => typeof raw[key] === 'string' && raw[key].trim() !== '')) {
    return false
  }

  const persisted: MailSettings = {
    webmailUrl: raw.webmail_url.trim(),
    mailAdminUrl: raw.mail_admin_url.trim(),
    mailServerHost: raw.mail_server_host.trim().toLowerCase(),
    imapPort: raw.mail_imap_port.trim(),
    smtpStarttlsPort: raw.mail_smtp_starttls_port.trim(),
    smtpSslPort: raw.mail_smtp_ssl_port.trim(),
  }
  return !validateMailSettings(persisted)
}

function copyToClipboard(text: string) {
  navigator.clipboard.writeText(text)
    .then(() => toast.success(`Copied: ${text}`))
    .catch(() => toast.error('Could not copy to clipboard'))
}

function InfoTable({ rows, title, icon: Icon }: {
  rows: ServerInfoRow[]
  title: string
  icon: React.ComponentType<{ className?: string }>
}) {
  return (
    <Card className="bg-zinc-900 border-zinc-800">
      <CardHeader className="pb-3">
        <CardTitle className="text-white text-base flex items-center gap-2">
          <Icon className="w-4 h-4 text-blue-400" />
          {title}
        </CardTitle>
      </CardHeader>
      <CardContent className="p-0">
        <div className="divide-y divide-zinc-800">
          {rows.map((row) => (
            <div
              key={row.label}
              className="flex items-center justify-between px-6 py-3 group hover:bg-zinc-800/40 transition-colors"
            >
              <span className="text-zinc-400 text-sm">{row.label}</span>
              <div className="flex items-center gap-2">
                {row.badge && (
                  <Badge variant="secondary" className="text-xs bg-blue-600/15 text-blue-400 border-blue-600/20">
                    {row.badge}
                  </Badge>
                )}
                <span className="text-white text-sm font-mono">{row.value}</span>
                {(row.label === 'Server' || row.label === 'Port' || row.label === 'Port (STARTTLS)' || row.label === 'Port (SSL)') && (
                  <button
                    onClick={() => copyToClipboard(row.value)}
                    className="opacity-0 group-hover:opacity-100 transition-opacity text-zinc-500 hover:text-zinc-300"
                    aria-label="Copy"
                  >
                    <Copy className="w-3.5 h-3.5" />
                  </button>
                )}
              </div>
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
  )
}

function ClientGuideCard({ guide }: { guide: ClientGuide }) {
  return (
    <Card className="bg-zinc-900 border-zinc-800">
      <CardHeader className="pb-3">
        <CardTitle className="text-white text-sm flex items-center gap-2">
          <Info className="w-4 h-4 text-zinc-500" />
          {guide.name}
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-2 pb-5">
        {guide.steps.map((step, i) => (
          <div key={i} className="flex items-start gap-3">
            <span className="flex-shrink-0 w-5 h-5 rounded-full bg-zinc-800 border border-zinc-700 text-zinc-400 text-xs flex items-center justify-center mt-0.5">
              {i + 1}
            </span>
            <span className="text-zinc-400 text-sm leading-snug">{step}</span>
          </div>
        ))}
      </CardContent>
    </Card>
  )
}

export default function Webmail() {
  const settingsQuery = useQuery({
    queryKey: ['settings', 'mail'],
    queryFn: () => api.get<MailSettingsResponse>('/settings'),
    staleTime: 60_000,
  })
  const rawSettings = settingsQuery.data ?? {}
  const mail = resolveMailSettings(rawSettings)
  const reportedState = normalizeIntegrationState(rawSettings.mail_access_state)
  const configured = isMailSettingsConfigured(mail) &&
    hasCompleteValidatedMailSettings(rawSettings) &&
    reportedState !== INTEGRATION_NOT_CONFIGURED
  const settingsKnown = settingsQuery.isSuccess
  // /settings validates saved endpoints but does not probe the external
  // provider. Keep complete configuration in the readiness path without
  // promoting it to the healthy availability state.
  const mailAccessState: IntegrationState | null = settingsQuery.isError
    ? INTEGRATION_UNAVAILABLE
    : settingsQuery.isSuccess
      ? integrationStateFromObservation(configured, false)
      : null
  const statePresentation = mailAccessState ? integrationStatePresentation(mailAccessState) : null
  const webmailHost = configured ? new URL(mail.webmailUrl).host : 'Not configured'

  const imapRows: ServerInfoRow[] = [
    { label: 'Server', value: mail.mailServerHost || '—' },
    { label: 'Port', value: mail.imapPort, badge: 'SSL/TLS' },
    { label: 'Username', value: 'Full email address' },
    { label: 'Password', value: 'Your email password' },
    { label: 'Encryption', value: 'SSL / TLS' },
  ]

  const smtpRows: ServerInfoRow[] = [
    { label: 'Server', value: mail.mailServerHost || '—' },
    { label: 'Port (STARTTLS)', value: mail.smtpStarttlsPort, badge: 'STARTTLS' },
    { label: 'Port (SSL)', value: mail.smtpSslPort, badge: 'SSL/TLS' },
    { label: 'Username', value: 'Full email address' },
    { label: 'Authentication', value: 'Required' },
  ]

  const clientGuides: ClientGuide[] = [
    {
      name: 'Thunderbird',
      steps: [
        'Open Account Settings → Add Mail Account',
        'Enter your name, email address, and password',
        'Click "Configure manually" if auto-detect fails',
        `Set IMAP: ${mail.mailServerHost}:${mail.imapPort} (SSL/TLS)`,
        `Set SMTP: ${mail.mailServerHost}:${mail.smtpStarttlsPort} (STARTTLS)`,
        'Username: full email address for both',
      ],
    },
    {
      name: 'Outlook',
      steps: [
        'File → Add Account → Manual setup',
        'Choose IMAP account type',
        `Incoming: ${mail.mailServerHost}, port ${mail.imapPort}, SSL/TLS`,
        `Outgoing: ${mail.mailServerHost}, port ${mail.smtpStarttlsPort}, STARTTLS`,
        'Login with full email address as username',
      ],
    },
    {
      name: 'Apple Mail',
      steps: [
        'Mail → Add Account → Other Mail Account',
        'Enter email and password, click Sign In',
        'Set account type to IMAP',
        `Incoming: ${mail.mailServerHost}, port ${mail.imapPort}`,
        `Outgoing: ${mail.mailServerHost}, port ${mail.smtpStarttlsPort}`,
        'Authentication: Password',
      ],
    },
  ]

  return (
    <div className="space-y-6">
      {settingsQuery.isError && (
        <DependencyRemediation
          title="Mail access settings are unavailable"
          summary="Webmail links and client configuration remain hidden because Heyserver could not confirm the saved settings."
          error={settingsQuery.error.message}
          retry={() => { void settingsQuery.refetch() }}
          retrying={settingsQuery.isFetching}
          steps={[
            'Verify the Heyserver API and Settings page are reachable.',
            'Inspect the panel service log for a settings read failure.',
            'Retry detection after the settings source is readable.',
          ]}
        />
      )}
      {settingsKnown && !configured && (
        <DependencyRemediation
          state="not-configured"
          title="Mail access is not configured"
          summary="Heyserver has no complete webmail, mail-admin, and IMAP/SMTP endpoint set, so it will not invent a provider or publish a partial client guide."
          retry={() => { void settingsQuery.refetch() }}
          retrying={settingsQuery.isFetching}
          steps={[
            <a href="/settings" className="text-blue-300 hover:underline">Open Settings → Mail Access.</a>,
            'Save complete webmail and mail-admin URLs plus the IMAP/SMTP hostname and ports.',
            'Retry detection, then open webmail or copy the observed client settings.',
          ]}
        />
      )}
      {settingsKnown && configured && mailAccessState === INTEGRATION_UNAVAILABLE && (
        <DependencyRemediation
          state="unavailable"
          title="Mail access reachability is unverified"
          summary="Heyserver confirmed complete, validated mail access settings, but this endpoint does not probe the external provider. Links and client guidance are configuration-only until reachability is checked separately."
          retry={() => { void settingsQuery.refetch() }}
          retrying={settingsQuery.isFetching}
          retryLabel="Refresh settings"
          steps={[
            'Confirm that the provider is running and its DNS/TLS endpoints resolve.',
            'Test webmail plus IMAP/SMTP login from a real client or provider monitor.',
            'Refresh these settings after the external reachability check; URLs alone are not a provider health signal.',
          ]}
        />
      )}

      {/* Header */}
      <div className="flex items-start justify-between gap-4 flex-wrap">
        <div>
          <h2 className="text-white text-xl font-bold">Webmail</h2>
          <p className="text-zinc-500 text-sm mt-0.5">
            {settingsQuery.isLoading ? 'Loading mail access settings' : settingsQuery.isError ? 'Mail access settings unavailable' : <>
              Posta okuma: <span className="font-mono text-zinc-400">{webmailHost}</span>
              {' · '}
              IMAP/SMTP sunucu: <span className="font-mono text-zinc-400">{mail.mailServerHost || 'Not configured'}</span>
            </>}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <div className={`flex items-center gap-1.5 rounded-lg border px-3 py-1.5 text-xs ${settingsQuery.isLoading ? 'border-zinc-700 bg-zinc-800 text-zinc-400' : settingsQuery.isError ? 'border-red-400/20 bg-red-400/10 text-red-300' : statePresentation?.tone === 'warning' ? 'border-amber-400/20 bg-amber-400/10 text-amber-300' : 'border-zinc-700 bg-zinc-800 text-zinc-400'}`}>
            <Settings2 className="w-3.5 h-3.5" />
            <span>{settingsQuery.isLoading ? 'Checking' : statePresentation?.label ?? 'Unavailable'}</span>
          </div>
          {settingsKnown && configured && (
            <a
              href={mail.webmailUrl}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 px-3 py-1.5 rounded-lg bg-blue-600 hover:bg-blue-500 text-white text-sm font-medium transition-colors"
            >
              <Mail className="w-4 h-4" />
              Open Webmail
              <ExternalLink className="w-3.5 h-3.5 opacity-70" />
            </a>
          )}
        </div>
      </div>

      {settingsQuery.isLoading && <Skeleton className="h-24 rounded-xl bg-zinc-900" />}

      {/* Quick access banner */}
      {settingsKnown && configured && <div className="rounded-xl border border-blue-600/25 bg-blue-600/5 p-5 flex items-center justify-between gap-4 flex-wrap">
        <div className="flex items-center gap-4">
          <div className="w-10 h-10 rounded-xl bg-blue-600/20 border border-blue-600/30 flex items-center justify-center flex-shrink-0">
            <Mail className="w-5 h-5 text-blue-400" />
          </div>
          <div>
            <p className="text-white font-medium text-sm">{configured ? 'Webmail Interface' : 'Mail access is optional'}</p>
            <p className="text-zinc-500 text-xs mt-0.5 font-mono">
              {configured ? mail.webmailUrl : 'Configure your provider in Settings → Mail Access.'}
            </p>
          </div>
        </div>
        {configured && (
          <a
            href={mail.webmailUrl}
            target="_blank"
            rel="noopener noreferrer"
            className="flex items-center gap-2 px-4 py-2 text-sm text-blue-400 border border-blue-600/30 rounded-lg hover:bg-blue-600/10 transition-colors"
          >
            Open in browser
            <ExternalLink className="w-3.5 h-3.5" />
          </a>
        )}
      </div>}

      {/* Server info grid */}
      {settingsKnown && configured && (
        <>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <InfoTable rows={imapRows} title="IMAP (Incoming)" icon={Server} />
            <InfoTable rows={smtpRows} title="SMTP (Outgoing)" icon={Shield} />
          </div>

          {/* Client setup guides */}
          <div>
            <h3 className="text-white text-base font-semibold mb-3 flex items-center gap-2">
              <Info className="w-4 h-4 text-zinc-500" />
              Email Client Setup
            </h3>
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
              {clientGuides.map((guide) => (
                <ClientGuideCard key={guide.name} guide={guide} />
              ))}
            </div>
          </div>

          {/* Hostname clarification */}
          <div className="flex items-start gap-3 px-4 py-3.5 rounded-lg bg-amber-500/5 border border-amber-500/20 text-zinc-400 text-sm">
            <Info className="w-4 h-4 mt-0.5 flex-shrink-0 text-amber-400" />
            <span>
              <span className="text-white font-medium">{webmailHost}</span> — tarayıcıda posta okuma/yazma.
              {' '}
              <span className="text-white font-medium">{mail.mailServerHost}</span> — yalnızca IMAP/SMTP sunucu adı ve{' '}
              <a href={mail.mailAdminUrl} target="_blank" rel="noopener noreferrer" className="text-blue-400 hover:underline">
                posta yönetim paneli
              </a>
              ; webmail adresi değildir.
            </span>
          </div>
        </>
      )}

      {/* Note */}
      {settingsKnown && configured && <div className="flex items-start gap-3 px-4 py-3.5 rounded-lg bg-zinc-800/50 border border-zinc-700/60 text-zinc-400 text-sm">
        <Info className="w-4 h-4 mt-0.5 flex-shrink-0 text-zinc-500" />
        <span>
          Use your <span className="text-white font-medium">full email address</span> as the username when configuring any mail client.
          Self-signed certificates may require manual trust approval on first connection.
        </span>
      </div>}
    </div>
  )
}
