export type AgentVersionState = 'current' | 'behind' | 'ahead' | 'unknown'

export interface AgentCompatibility {
  panel_version: string
  expected_protocol: string
  protocol_compatible: boolean
  agent_version_state: AgentVersionState
}

export type CompatibilityTone = 'healthy' | 'warning' | 'critical' | 'neutral'

export interface CompatibilityPresentation {
  label: string
  detail: string
  tone: CompatibilityTone
}

export function displayReleaseVersion(value?: string) {
  const normalized = value?.trim()
  if (!normalized) return 'unknown'
  return /^\d+\.\d+\.\d+$/.test(normalized) ? `v${normalized}` : normalized
}

export function compatibilityPresentation(
  compatibility: AgentCompatibility | undefined,
  agentVersion: string,
  protocolVersion: string,
): CompatibilityPresentation {
  if (!compatibility) {
    return { label: 'Compatibility unknown', detail: 'Refresh after the panel API is upgraded', tone: 'neutral' }
  }

  if (!compatibility.protocol_compatible) {
    return {
      label: 'Protocol mismatch',
      detail: `Agent ${protocolVersion || 'unknown'} · panel expects ${compatibility.expected_protocol}`,
      tone: 'critical',
    }
  }

  const versions = `Agent ${displayReleaseVersion(agentVersion)} · panel ${displayReleaseVersion(compatibility.panel_version)}`
  switch (compatibility.agent_version_state) {
    case 'current':
      return { label: 'Compatible', detail: versions, tone: 'healthy' }
    case 'behind':
      return { label: 'Agent update available', detail: versions, tone: 'warning' }
    case 'ahead':
      return { label: 'Agent ahead of panel', detail: versions, tone: 'warning' }
    default:
      return { label: 'Development build', detail: `${versions} · release ordering unavailable`, tone: 'neutral' }
  }
}

export function summarizeFleetCompatibility(nodes: Array<{ compatibility?: AgentCompatibility }>) {
  return nodes.reduce((summary, node) => {
    const compatibility = node.compatibility
    if (!compatibility) {
      summary.unknown += 1
    } else {
      if (!compatibility.protocol_compatible) summary.protocolIssues += 1
      if (compatibility.agent_version_state === 'current' && compatibility.protocol_compatible) summary.current += 1
      if (compatibility.agent_version_state === 'behind') summary.behind += 1
      if (compatibility.agent_version_state === 'ahead') summary.ahead += 1
      if (compatibility.agent_version_state === 'unknown') summary.unknown += 1
    }
    return summary
  }, { current: 0, behind: 0, ahead: 0, protocolIssues: 0, unknown: 0 })
}
