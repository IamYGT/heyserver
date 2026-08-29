export type TerminalOutput = string | Uint8Array

export const TERMINAL_MAX_RECONNECT_ATTEMPTS = 6

export function terminalReconnectDelay(attempt: number): number {
  const safeAttempt = Math.max(1, Math.floor(attempt))
  return Math.min(500 * 2 ** (safeAttempt - 1), 5_000)
}

export function decodeTerminalOutput(data: string, encoding?: string): TerminalOutput {
  if (encoding !== 'base64') return data

  const binary = globalThis.atob(data)
  const bytes = new Uint8Array(binary.length)
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index)
  }
  return bytes
}
