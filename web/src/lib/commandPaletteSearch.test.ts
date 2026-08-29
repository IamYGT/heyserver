import { describe, expect, it } from 'vitest'
import { filterAndRankCommandPalette } from './commandPaletteSearch'

const items = [
  { label: 'Disk', description: 'Filesystem usage and capacity', category: 'page' },
  { label: 'Files', description: 'File manager', category: 'page' },
  { label: 'SSL', description: 'Certificates', category: 'page', keywords: ['tls', 'cert'] },
]

describe('filterAndRankCommandPalette', () => {
  it('ranks an exact label above a description substring', () => {
    expect(filterAndRankCommandPalette(items, 'Files').map(item => item.label)).toEqual(['Files', 'Disk'])
  })

  it('keeps keyword discovery', () => {
    expect(filterAndRankCommandPalette(items, 'cert').map(item => item.label)).toEqual(['SSL'])
  })

  it('returns the configured leading items for an empty query', () => {
    expect(filterAndRankCommandPalette(items, '', 2).map(item => item.label)).toEqual(['Disk', 'Files'])
  })
})
