import { describe, expect, it } from 'vitest'
import {
  CRITICAL_ROUTE_PATHS,
  PROTECTED_ROUTE_ENTRIES,
  ROUTE_PATHS,
} from './routes'

describe('ROUTE_PATHS', () => {
  it('defines expected critical route strings', () => {
    expect(ROUTE_PATHS.login).toBe('/login')
    expect(ROUTE_PATHS.dashboard).toBe('/')
    expect(ROUTE_PATHS.backups).toBe('/backups')
    expect(ROUTE_PATHS.domains).toBe('/domains')
    expect(ROUTE_PATHS.settings).toBe('/settings')
    expect(ROUTE_PATHS.terminal).toBe('/terminal')
    expect(ROUTE_PATHS.users).toBe('/users')
    expect(ROUTE_PATHS.integrations).toBe('/integrations')
  })

  it('lists all protected panel sections', () => {
    const protectedPaths = PROTECTED_ROUTE_ENTRIES.map((entry) => entry.path)

    for (const path of protectedPaths) {
      expect(path).toMatch(/^\//)
    }
    expect(new Set(protectedPaths).size).toBe(protectedPaths.length)
  })

  it('exposes CRITICAL_ROUTE_PATHS as a non-empty subset', () => {
    expect(CRITICAL_ROUTE_PATHS.length).toBeGreaterThan(0)
    for (const path of CRITICAL_ROUTE_PATHS) {
      expect(Object.values(ROUTE_PATHS)).toContain(path)
    }
  })
})

describe('PROTECTED_ROUTE_ENTRIES', () => {
  it('covers every protected ROUTE_PATHS value', () => {
    const publicOrSpecial = new Set<string>([
      ROUTE_PATHS.login,
      ROUTE_PATHS.onboarding,
      ROUTE_PATHS.notFound,
    ])
    const protectedPaths = Object.values(ROUTE_PATHS).filter(
      (path) => !publicOrSpecial.has(path),
    )
    const entryPaths = PROTECTED_ROUTE_ENTRIES.map((entry) => entry.path)

    expect(entryPaths).toEqual(expect.arrayContaining(protectedPaths))
    expect(entryPaths.length).toBe(protectedPaths.length)
  })

  it('assigns a lazy component to each entry', () => {
    for (const entry of PROTECTED_ROUTE_ENTRIES) {
      expect(entry.path.length).toBeGreaterThan(0)
      expect(entry.Component).toBeTruthy()
    }
  })

  it('includes all critical navigation paths', () => {
    const entryPaths = new Set(
      PROTECTED_ROUTE_ENTRIES.map((entry) => entry.path),
    )
    for (const path of CRITICAL_ROUTE_PATHS) {
      if (path === ROUTE_PATHS.login) {
        continue
      }
      expect(entryPaths.has(path)).toBe(true)
    }
  })
})
