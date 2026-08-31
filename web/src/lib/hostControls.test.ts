import { describe, expect, it } from 'vitest'
import {
  hostActionEndpoint,
  hostActionConfirmation,
  hostActionLabel,
  hostActionStatusEndpoint,
  memoryOptimizeConfirmation,
  quickControlTargets,
  rebootControlState,
  rebootStatusDescription,
  rebootCancelEndpoint,
  rebootStatusEndpoint,
  rebootStatusQueryKey,
  swapResetAvailability,
  swapResetConfirmation,
  tempCleanConfirmation,
} from './hostControls'

describe('host quick controls', () => {
  it('routes actions and reboot state to the selected server', () => {
    expect(hostActionEndpoint('local', 'memory-optimize')).toBe('/system/actions/memory-optimize')
    expect(hostActionEndpoint('edge-eu-1', 'memory-optimize')).toBe('/nodes/edge-eu-1/actions/memory-optimize')
    expect(rebootStatusEndpoint('local')).toBe('/system/actions/reboot-status')
		expect(rebootCancelEndpoint('edge-eu-1')).toBe('/nodes/edge-eu-1/actions/reboot-cancel')
		expect(rebootStatusQueryKey('local')).toEqual(['host-controls', 'reboot-status', 'local'])
		expect(rebootStatusQueryKey('edge-eu-1')).toEqual(['host-controls', 'reboot-status', 'edge-eu-1'])
		expect(hostActionStatusEndpoint('local')).toBe('/system/actions/status')
		expect(hostActionStatusEndpoint('edge-eu-1')).toBe('/nodes/edge-eu-1/actions/status')
		expect(hostActionLabel('swap-reset')).toBe('Swap reset')
		expect(hostActionLabel('disk-cleanup')).toBe('Disk cleanup')
  })

  it('opens equivalent operational screens for both servers', () => {
    expect(quickControlTargets('local')).toEqual({
      terminal: '/terminal', services: '/monitoring',
      processes: '/monitoring?focus=processes', disk: '/disk?tab=cleanup',
    })
    expect(quickControlTargets('edge-eu-1')).toEqual({
      terminal: '/terminal?node=edge-eu-1', services: '/servers?node=edge-eu-1&tab=services',
      processes: '/servers?node=edge-eu-1&tab=processes', disk: '/servers?node=edge-eu-1&tab=disk',
    })
  })

  it('blocks swap reset unless swap is used and RAM has a 512 MiB reserve', () => {
    expect(swapResetAvailability()).toMatchObject({ eligible: false, reason: 'loading' })
    expect(swapResetAvailability({ total: 0, used: 0, available: 10 })).toMatchObject({ eligible: false, reason: 'not-configured' })
    expect(swapResetAvailability({ total: 1024, used: 0, available: 1024 })).toMatchObject({ eligible: false, reason: 'already-empty' })
    expect(swapResetAvailability({ total: 1024, used: 1024, available: 512 * 1024 ** 2 })).toMatchObject({ eligible: false, reason: 'insufficient-memory' })
    expect(swapResetAvailability({ total: 1024, used: 1024, available: 512 * 1024 ** 2 + 1024 })).toEqual({
      eligible: true,
      requiredAvailable: 512 * 1024 ** 2 + 1024,
    })
  })

  it('explains the measured swap reset safety margin before execution', () => {
    expect(swapResetConfirmation('Heyserver', {
      total: 8 * 1024 ** 3,
      used: 2 * 1024 ** 3,
      available: 4 * 1024 ** 3,
    }, 2.5 * 1024 ** 3)).toBe(
      'Reset 2.0 GB of used swap on Heyserver now? 4.0 GB RAM is currently available; the safety check requires at least 2.5 GB, including a 512 MB reserve. Running processes stay active, but memory pressure can rise briefly.',
    )
  })

  it('explains RAM optimization effects with and without a live measurement', () => {
    expect(memoryOptimizeConfirmation('Heyserver', 12.25 * 1024 ** 3)).toBe(
      'Optimize RAM on Heyserver now? 12.3 GB RAM is currently available. This syncs pending filesystem writes and releases only reclaimable caches; running processes and swap stay unchanged.',
    )
    expect(memoryOptimizeConfirmation('Contabo')).toBe(
      'Optimize RAM on Contabo now? This syncs pending filesystem writes and releases only reclaimable caches; running processes and swap stay unchanged.',
    )
  })

  it('explains the bounded temporary-file cleanup scope', () => {
    expect(tempCleanConfirmation('Contabo')).toBe(
      'Clean expired temporary files on Contabo now? This applies the host tmpfiles age policy; recent files and active application data are not targeted.',
    )
  })

  it('uses one canonical confirmation contract for every host action surface', () => {
    const memory = { total: 8 * 1024 ** 3, used: 2 * 1024 ** 3, available: 4 * 1024 ** 3 }
    expect(hostActionConfirmation('memory-optimize', 'Contabo', memory)).toContain('4.0 GB RAM is currently available')
    expect(hostActionConfirmation('swap-reset', 'Contabo', memory, 2.5 * 1024 ** 3)).toContain('requires at least 2.5 GB')
    expect(hostActionConfirmation('temp-clean', 'Contabo')).toContain('recent files and active application data are not targeted')
    expect(hostActionConfirmation('reboot', 'Contabo')).toBe('Reboot Contabo in 10 seconds? Active terminal sessions and services will disconnect.')
  })

  it('renders a persisted reboot countdown with a safe fallback', () => {
    expect(rebootStatusDescription({ pending: true, scheduled_for: '2026-08-26T04:00:10Z', remaining_seconds: 7 })).toBe('Reboot in 7s · click to cancel')
    expect(rebootStatusDescription({ pending: true })).toBe('Reboot timer is active · click to cancel')
    expect(rebootStatusDescription({ pending: false })).toBe('Schedule a reboot in 10 seconds')
  })

  it('keeps reboot recovery actionable without risking a duplicate schedule', () => {
    expect(rebootControlState(undefined, { isLoading: true, isError: false, isFetching: true })).toEqual({
      pending: false,
      blocked: true,
      retryable: false,
      description: 'Loading reboot timer state…',
    })
    expect(rebootControlState(undefined, { isLoading: false, isError: true, isFetching: true })).toEqual({
      pending: false,
      blocked: true,
      retryable: false,
      description: 'Retrying reboot timer state…',
    })
    expect(rebootControlState({ pending: false }, { isLoading: false, isError: true, isFetching: false })).toEqual({
      pending: false,
      blocked: false,
      retryable: true,
      description: 'Could not read reboot timer state · click to retry',
    })
    expect(rebootControlState({ pending: true, remaining_seconds: 7 }, { isLoading: false, isError: true, isFetching: false })).toEqual({
      pending: true,
      blocked: false,
      retryable: false,
      description: 'Reboot is scheduled · countdown refresh failed · click to cancel',
    })
  })
})
