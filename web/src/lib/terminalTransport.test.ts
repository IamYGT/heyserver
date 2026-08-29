import { describe, expect, it } from 'vitest'
import {
  decodeTerminalOutput,
  TERMINAL_MAX_RECONNECT_ATTEMPTS,
  terminalReconnectDelay,
} from './terminalTransport'

describe('terminal transport', () => {
  it('preserves PTY C1 control bytes transported as base64', () => {
    expect(Array.from(decodeTerminalOutput('G1AkZnt9nA==', 'base64') as Uint8Array))
      .toEqual([0x1b, 0x50, 0x24, 0x66, 0x7b, 0x7d, 0x9c])
  })

  it('keeps legacy text output compatible', () => {
    expect(decodeTerminalOutput('root@server:~# ')).toBe('root@server:~# ')
  })

  it('backs off quickly but caps reconnect delays', () => {
    expect(TERMINAL_MAX_RECONNECT_ATTEMPTS).toBe(6)
    expect([1, 2, 3, 4, 5, 6].map(terminalReconnectDelay))
      .toEqual([500, 1000, 2000, 4000, 5000, 5000])
    expect(terminalReconnectDelay(0)).toBe(500)
  })
})
