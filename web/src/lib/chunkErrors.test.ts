import { describe, expect, it } from 'vitest'
import { isChunkLoadError } from './chunkErrors'

describe('isChunkLoadError', () => {
  it.each([
    'Failed to fetch dynamically imported module: https://example.test/assets/Servers.js',
    'Importing a module script failed.',
    'Loading chunk 42 failed',
  ])('recognizes recoverable lazy chunk failures: %s', (message) => {
    expect(isChunkLoadError(new Error(message))).toBe(true)
  })

  it('does not classify ordinary render failures as chunk failures', () => {
    expect(isChunkLoadError(new Error('Cannot read properties of undefined'))).toBe(false)
  })
})
