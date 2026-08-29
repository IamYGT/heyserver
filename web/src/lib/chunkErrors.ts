const CHUNK_ERROR_PATTERNS = [
  /failed to fetch dynamically imported module/i,
  /importing a module script failed/i,
  /loading chunk [\d-]+ failed/i,
  /chunkloaderror/i,
]

export function isChunkLoadError(error: Error | null | undefined): boolean {
  if (!error) return false
  return CHUNK_ERROR_PATTERNS.some(pattern => pattern.test(`${error.name}: ${error.message}`))
}
