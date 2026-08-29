import { describe, expect, it } from 'vitest'
import { buildCrumbs } from './breadcrumbUtils'

describe('buildCrumbs', () => {
  it('keeps the local dashboard as Home for HServer routes', () => {
    expect(buildCrumbs('/audit', '/')).toEqual([
      { label: 'Home', href: '/', isLast: false },
      { label: 'Audit Log', href: '/audit', isLast: true },
    ])
  })

  it('uses the Contabo overview as Home for remote routes', () => {
    expect(buildCrumbs('/audit', '/servers')).toEqual([
      { label: 'Home', href: '/servers', isLast: false },
      { label: 'Audit Log', href: '/audit', isLast: true },
    ])
  })

  it('treats the Contabo overview itself as the remote root', () => {
    expect(buildCrumbs('/servers', '/servers')).toEqual([
      { label: 'Home', href: '/servers', isLast: true },
    ])
  })
})
