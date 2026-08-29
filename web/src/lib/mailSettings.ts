export interface MailSettings {
  webmailUrl: string
  mailAdminUrl: string
  mailServerHost: string
  imapPort: string
  smtpStarttlsPort: string
  smtpSslPort: string
}

export const DEFAULT_MAIL_SETTINGS: MailSettings = {
  webmailUrl: '',
  mailAdminUrl: '',
  mailServerHost: '',
  imapPort: '993',
  smtpStarttlsPort: '587',
  smtpSslPort: '465',
}

function validHttpUrl(value: string): boolean {
  try {
    const url = new URL(value)
    return (url.protocol === 'http:' || url.protocol === 'https:') && Boolean(url.hostname)
  } catch {
    return false
  }
}

function validHostname(value: string): boolean {
  if (!value || value.length > 253 || value.includes('..')) return false
  if (!/^[a-z0-9.-]+$/i.test(value)) return false
  return value.split('.').every((label) =>
    label.length > 0 &&
    label.length <= 63 &&
    !label.startsWith('-') &&
    !label.endsWith('-'),
  )
}

function validPort(value: string): boolean {
  if (!/^\d{1,5}$/.test(value)) return false
  const port = Number(value)
  return port >= 1 && port <= 65535
}

function safeUrl(value: string | undefined, fallback: string): string {
  const candidate = value?.trim() ?? ''
  return validHttpUrl(candidate) ? candidate : fallback
}

function safeHostname(value: string | undefined, fallback: string): string {
  const candidate = value?.trim().toLowerCase() ?? ''
  return validHostname(candidate) ? candidate : fallback
}

function safePort(value: string | undefined, fallback: string): string {
  const candidate = value?.trim() ?? ''
  return validPort(candidate) ? candidate : fallback
}

export function resolveMailSettings(raw: Record<string, string>): MailSettings {
  return {
    webmailUrl: safeUrl(raw.webmail_url, DEFAULT_MAIL_SETTINGS.webmailUrl),
    mailAdminUrl: safeUrl(raw.mail_admin_url, DEFAULT_MAIL_SETTINGS.mailAdminUrl),
    mailServerHost: safeHostname(raw.mail_server_host, DEFAULT_MAIL_SETTINGS.mailServerHost),
    imapPort: safePort(raw.mail_imap_port, DEFAULT_MAIL_SETTINGS.imapPort),
    smtpStarttlsPort: safePort(raw.mail_smtp_starttls_port, DEFAULT_MAIL_SETTINGS.smtpStarttlsPort),
    smtpSslPort: safePort(raw.mail_smtp_ssl_port, DEFAULT_MAIL_SETTINGS.smtpSslPort),
  }
}

export function validateMailSettings(settings: MailSettings): string | undefined {
  const hasMailEndpoint = Boolean(
    settings.webmailUrl.trim() || settings.mailAdminUrl.trim() || settings.mailServerHost.trim(),
  )
  if (!hasMailEndpoint) return undefined

  if (!validHttpUrl(settings.webmailUrl.trim())) {
    return 'Webmail URL must be a complete http:// or https:// address.'
  }
  if (!validHttpUrl(settings.mailAdminUrl.trim())) {
    return 'Mail admin URL must be a complete http:// or https:// address.'
  }
  if (!validHostname(settings.mailServerHost.trim())) {
    return 'IMAP/SMTP server must be a hostname without a protocol, path, or port.'
  }
  if (!validPort(settings.imapPort.trim())) return 'IMAP port must be between 1 and 65535.'
  if (!validPort(settings.smtpStarttlsPort.trim())) return 'SMTP STARTTLS port must be between 1 and 65535.'
  if (!validPort(settings.smtpSslPort.trim())) return 'SMTP SSL port must be between 1 and 65535.'
  return undefined
}

export function isMailSettingsConfigured(settings: MailSettings): boolean {
  return Boolean(
    settings.webmailUrl &&
    settings.mailAdminUrl &&
    settings.mailServerHost &&
    !validateMailSettings(settings),
  )
}
