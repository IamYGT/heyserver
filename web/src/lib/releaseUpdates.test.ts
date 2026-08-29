import { describe, expect, it } from 'vitest'
import { releaseSignatureLabel, releaseStagePresentation, releaseUpdatePresentation, type ReleaseStage, type ReleaseUpdateStatus } from './releaseUpdates'

function update(overrides: Partial<ReleaseUpdateStatus>): ReleaseUpdateStatus {
  return {
    status: 'healthy',
    current_version: '1.2.3',
    latest_version: '1.2.3',
    latest_version_state: 'current',
    update_available: false,
    platform: 'linux_amd64',
    message: 'This HServer release is current.',
    checked_at: '2026-08-26T18:00:00Z',
    signature_status: 'not_configured',
    ...overrides,
  }
}

describe('release update presentation', () => {
  it('keeps optional release discovery states distinct', () => {
    expect(releaseUpdatePresentation(update({ status: 'not_configured' })))
      .toEqual({ title: 'Release discovery not configured', tone: 'neutral' })
    expect(releaseUpdatePresentation(update({ status: 'unavailable' })))
      .toEqual({ title: 'Release discovery unavailable', tone: 'warning' })
  })

  it('does not confuse an available update with a current release', () => {
    expect(releaseUpdatePresentation(update({ latest_version: '1.3.0', latest_version_state: 'ahead', update_available: true })))
      .toEqual({ title: 'Update available', tone: 'available' })
    expect(releaseUpdatePresentation(update({})))
      .toEqual({ title: 'HServer is up to date', tone: 'healthy' })
  })

  it('keeps development and feed-behind states explicit', () => {
    expect(releaseUpdatePresentation(update({ latest_version_state: 'unknown' })))
      .toEqual({ title: 'Development build', tone: 'neutral' })
    expect(releaseUpdatePresentation(update({ latest_version_state: 'behind' })))
      .toEqual({ title: 'Panel is ahead of the release feed', tone: 'warning' })
  })

  it('keeps signed and checksum-only trust states explicit', () => {
    expect(releaseSignatureLabel('verified')).toBe('Ed25519 signature verified')
    expect(releaseSignatureLabel('not_configured')).toContain('Checksum only')
    expect(releaseSignatureLabel('unavailable')).toContain('unavailable')
  })
})

describe('release stage presentation', () => {
  const stage: ReleaseStage = {
    id: 'v1.3.0-0123456789ab',
    version: 'v1.3.0',
    current_version: 'v1.2.3',
    platform: 'linux_amd64',
    sha256: '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef',
    size_bytes: 1234,
    status: 'staged',
    created_at: '2026-08-26T18:00:00Z',
    updated_at: '2026-08-26T18:00:00Z',
  }

  it('keeps verification separate from installation', () => {
    expect(releaseStagePresentation(stage)).toEqual({
      title: 'Archive verified and ready',
      detail: 'A second admin confirmation is required before installation.',
      tone: 'available',
    })
  })

  it('does not claim success for scheduled, running, or failed stages', () => {
    expect(releaseStagePresentation({ ...stage, status: 'scheduled' }).title).toBe('Upgrade scheduled')
    expect(releaseStagePresentation({ ...stage, status: 'running' }).title).toBe('Upgrade in progress')
    expect(releaseStagePresentation({ ...stage, status: 'failed' }).title).toBe('Upgrade failed')
    expect(releaseStagePresentation({ ...stage, status: 'completed' }).title).toBe('Upgrade completed')
  })
})
