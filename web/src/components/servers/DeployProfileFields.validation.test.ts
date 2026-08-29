import { describe, expect, it } from 'vitest'
import {
  validateOptionalAbsoluteDirectories,
  validateOptionalAbsoluteDirectory,
  validateOptionalAbsoluteFilePath,
} from './DeployProfileFields'

describe('DeployProfileFields SAFE_PATH validation', () => {
  it('accepts the inclusive 4096-byte boundary and rejects 4097 bytes', () => {
    expect(validateOptionalAbsoluteDirectory(`/${'a'.repeat(4095)}`)).toBe('')
    expect(validateOptionalAbsoluteDirectory(`/${'a'.repeat(4096)}`)).toContain('max 4096 bytes')
  })

  it('rejects every non-ASCII or non-SAFE_PATH character', () => {
    for (const path of ['/srv/deploy plans.json', '/srv/dağıtım.json', '/srv/deploy%20plans.json']) {
      expect(validateOptionalAbsoluteFilePath(path)).toContain('SAFE_PATH')
      expect(validateOptionalAbsoluteDirectory(path)).toContain('SAFE_PATH')
      expect(validateOptionalAbsoluteDirectories([path])).toContain('SAFE_PATH')
    }
  })

  it('keeps file, root, clean, and trailing-slash rules closed', () => {
    for (const path of ['/', '/srv/../etc', '/srv//apps', '/srv/apps/']) {
      expect(validateOptionalAbsoluteFilePath(path)).not.toBe('')
      expect(validateOptionalAbsoluteDirectory(path)).not.toBe('')
    }
  })
})
