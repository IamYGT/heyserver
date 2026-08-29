import { describe, expect, it } from 'vitest'
import {
  DEFAULT_MAIL_SETTINGS,
  isMailSettingsConfigured,
  resolveMailSettings,
  validateMailSettings,
} from './mailSettings'

describe('resolveMailSettings', () => {
  it('uses persisted values when every field is valid', () => {
    expect(resolveMailSettings({
      webmail_url: 'https://inbox.example.com/mail',
      mail_admin_url: 'https://admin.example.com',
      mail_server_host: 'MX.EXAMPLE.COM',
      mail_imap_port: '1993',
      mail_smtp_starttls_port: '1587',
      mail_smtp_ssl_port: '1465',
    })).toEqual({
      webmailUrl: 'https://inbox.example.com/mail',
      mailAdminUrl: 'https://admin.example.com',
      mailServerHost: 'mx.example.com',
      imapPort: '1993',
      smtpStarttlsPort: '1587',
      smtpSslPort: '1465',
    })
  })

  it('falls back instead of exposing unsafe or malformed values', () => {
    expect(resolveMailSettings({
      webmail_url: 'javascript:alert(1)',
      mail_admin_url: 'not a URL',
      mail_server_host: 'https://mail.example.com:993/path',
      mail_imap_port: '0',
      mail_smtp_starttls_port: '70000',
      mail_smtp_ssl_port: 'abc',
    })).toEqual(DEFAULT_MAIL_SETTINGS)
  })
})

describe('validateMailSettings', () => {
  const configuredSettings = {
    ...DEFAULT_MAIL_SETTINGS,
    webmailUrl: 'https://webmail.example.com',
    mailAdminUrl: 'https://mail-admin.example.com',
    mailServerHost: 'mail.example.com',
  }

  it('accepts an unconfigured installation', () => {
    expect(validateMailSettings(DEFAULT_MAIL_SETTINGS)).toBeUndefined()
    expect(isMailSettingsConfigured(DEFAULT_MAIL_SETTINGS)).toBe(false)
  })

  it('returns a precise error for an invalid field', () => {
    expect(validateMailSettings({ ...configuredSettings, mailServerHost: 'mail.example.com:993' }))
      .toBe('IMAP/SMTP server must be a hostname without a protocol, path, or port.')
    expect(validateMailSettings({ ...configuredSettings, imapPort: '65536' }))
      .toBe('IMAP port must be between 1 and 65535.')
  })

  it('requires every endpoint after mail access is enabled', () => {
    expect(validateMailSettings({ ...DEFAULT_MAIL_SETTINGS, webmailUrl: 'https://webmail.example.com' }))
      .toBe('Mail admin URL must be a complete http:// or https:// address.')
    expect(isMailSettingsConfigured(configuredSettings)).toBe(true)
  })
})
