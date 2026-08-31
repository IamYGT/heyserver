export type ReleaseVersionState = 'current' | 'behind' | 'ahead' | 'unknown'
export type ReleaseDiscoveryStatus = 'not_configured' | 'unavailable' | 'healthy'
export type ReleaseSignatureStatus = 'not_configured' | 'unavailable' | 'verified'

export interface ReleaseArtifact {
  url: string
  sha256: string
  size_bytes?: number
}

export interface ReleaseUpdateStatus {
  status: ReleaseDiscoveryStatus
  current_version: string
  latest_version?: string
  latest_version_state?: ReleaseVersionState
  update_available: boolean
  platform: string
  artifact?: ReleaseArtifact
  published_at?: string
  release_notes_url?: string
  message: string
  checked_at: string
  signature_status: ReleaseSignatureStatus
}

export function releaseSignatureLabel(status: ReleaseSignatureStatus): string {
  switch (status) {
    case 'verified': return 'Ed25519 signature verified'
    case 'unavailable': return 'Signature verification unavailable'
    case 'not_configured': return 'Checksum only (signing key not configured)'
  }
}

export type ReleaseStageStatus = 'staged' | 'scheduled' | 'running' | 'completed' | 'failed'

export interface ReleaseStage {
  id: string
  version: string
  current_version: string
  platform: string
  sha256: string
  size_bytes: number
  status: ReleaseStageStatus
  status_detail?: string
  created_at: string
  updated_at: string
}

export interface ReleaseStageResponse {
  stage: ReleaseStage | null
}

export type ReleaseUpdateTone = 'available' | 'healthy' | 'warning' | 'neutral'

export function releaseUpdatePresentation(update: ReleaseUpdateStatus) {
  if (update.status === 'not_configured') {
    return { title: 'Release discovery not configured', tone: 'neutral' as const }
  }
  if (update.status === 'unavailable') {
    return { title: 'Release discovery unavailable', tone: 'warning' as const }
  }
  if (update.update_available) {
    return { title: 'Update available', tone: 'available' as const }
  }
  if (update.latest_version_state === 'unknown') {
    return { title: 'Development build', tone: 'neutral' as const }
  }
  if (update.latest_version_state === 'behind') {
    return { title: 'Panel is ahead of the release feed', tone: 'warning' as const }
  }
  return { title: 'Heyserver is up to date', tone: 'healthy' as const }
}

export function releaseStagePresentation(stage: ReleaseStage) {
  switch (stage.status) {
    case 'staged':
      return { title: 'Archive verified and ready', detail: 'A second admin confirmation is required before installation.', tone: 'available' as const }
    case 'scheduled':
      return { title: 'Upgrade scheduled', detail: 'The detached installer will start in a few seconds and restart Heyserver.', tone: 'warning' as const }
    case 'running':
      return { title: 'Upgrade in progress', detail: 'Heyserver may be briefly unavailable while the new release is health-checked.', tone: 'warning' as const }
    case 'completed':
      return { title: 'Upgrade completed', detail: 'The new release passed its health check.', tone: 'healthy' as const }
    case 'failed':
      return { title: 'Upgrade failed', detail: 'The packaged installer reported failure; review the service journal before retrying.', tone: 'warning' as const }
  }
}
