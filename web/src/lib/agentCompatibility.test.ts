import { describe, expect, it } from 'vitest'
import {
  compatibilityPresentation,
  displayReleaseVersion,
  summarizeFleetCompatibility,
  type AgentCompatibility,
} from './agentCompatibility'

function compatibility(overrides: Partial<AgentCompatibility> = {}): AgentCompatibility {
  return {
    panel_version: '1.4.0',
    expected_protocol: 'v1',
    protocol_compatible: true,
    agent_version_state: 'current',
    ...overrides,
  }
}

describe('agent compatibility presentation', () => {
  it('formats release tags without adding a duplicate prefix', () => {
    expect(displayReleaseVersion('1.4.0')).toBe('v1.4.0')
    expect(displayReleaseVersion('v1.4.0')).toBe('v1.4.0')
    expect(displayReleaseVersion('dev')).toBe('dev')
  })

  it('prioritizes a protocol mismatch over release ordering', () => {
    expect(compatibilityPresentation(
      compatibility({ protocol_compatible: false, agent_version_state: 'behind' }),
      '1.3.0',
      'v0',
    )).toEqual({
      label: 'Protocol mismatch',
      detail: 'Agent v0 · panel expects v1',
      tone: 'critical',
    })
  })

  it('turns release drift into actionable fleet states', () => {
    expect(compatibilityPresentation(compatibility({ agent_version_state: 'behind' }), '1.3.0', 'v1'))
      .toMatchObject({ label: 'Agent update available', tone: 'warning' })
    expect(compatibilityPresentation(compatibility({ agent_version_state: 'ahead' }), '1.5.0', 'v1'))
      .toMatchObject({ label: 'Agent ahead of panel', tone: 'warning' })
  })

  it('summarizes every managed node without hiding unknown builds', () => {
    expect(summarizeFleetCompatibility([
      { compatibility: compatibility() },
      { compatibility: compatibility({ agent_version_state: 'behind' }) },
      { compatibility: compatibility({ agent_version_state: 'ahead' }) },
      { compatibility: compatibility({ protocol_compatible: false }) },
      { compatibility: compatibility({ agent_version_state: 'unknown' }) },
      {},
    ])).toEqual({ current: 1, behind: 1, ahead: 1, protocolIssues: 1, unknown: 2 })
  })
})
