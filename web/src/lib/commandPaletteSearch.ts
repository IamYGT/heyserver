export interface CommandPaletteSearchItem {
  label: string
  description?: string
  category: string
  keywords?: string[]
}

function matchScore(item: CommandPaletteSearchItem, query: string): number {
  const label = item.label.toLowerCase()
  const keywords = item.keywords?.map(keyword => keyword.toLowerCase()) ?? []
  const description = item.description?.toLowerCase() ?? ''
  const category = item.category.toLowerCase()

  if (label === query) return 0
  if (label.startsWith(query)) return 10
  if (label.includes(query)) return 20
  if (keywords.some(keyword => keyword === query)) return 30
  if (keywords.some(keyword => keyword.startsWith(query))) return 40
  if (keywords.some(keyword => keyword.includes(query))) return 50
  if (category === query || category.startsWith(query)) return 60
  if (description.startsWith(query)) return 70
  if (description.includes(query) || category.includes(query)) return 80
  return Number.POSITIVE_INFINITY
}

export function filterAndRankCommandPalette<T extends CommandPaletteSearchItem>(
  items: T[],
  rawQuery: string,
  emptyLimit = 8,
): T[] {
  const query = rawQuery.trim().toLowerCase()
  if (!query) return items.slice(0, emptyLimit)

  return items
    .map((item, index) => ({ item, index, score: matchScore(item, query) }))
    .filter(result => Number.isFinite(result.score))
    .sort((left, right) => left.score - right.score || left.index - right.index)
    .map(result => result.item)
}
