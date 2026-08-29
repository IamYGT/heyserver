import { describe, expect, it } from 'vitest'
import { activeFileRoot, fileRootLabel } from './fileRoots'

describe('file manager roots', () => {
  it('selects the most specific configured root', () => {
    const roots = ['/srv/hserver/sites', '/srv/hserver/sites/private', '/etc/nginx']
    expect(activeFileRoot('/srv/hserver/sites/private/app/config', roots)).toBe('/srv/hserver/sites/private')
    expect(activeFileRoot('/etc/nginx/sites-enabled', roots)).toBe('/etc/nginx')
  })

  it('labels installation and system roots without a fixed vhost path', () => {
    expect(fileRootLabel('/srv/hserver/sites', 0)).toBe('Web Vhosts')
    expect(fileRootLabel('/etc/nginx', 1)).toBe('Nginx Config')
    expect(fileRootLabel('/srv/custom', 2)).toBe('custom')
  })
})
