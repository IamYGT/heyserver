import { describe, expect, it } from 'vitest'
import { createSSEParser } from './sse'

describe('SSE parser', () => {
  it('reassembles split frames and ignores heartbeat comments', () => {
    const messages: string[] = []
    const parser = createSSEParser((message) => messages.push(message))

    parser.push(': ping\n\ndata: {"sta')
    parser.push('tus":"running"}\n\ndata: first\ndata: second\n\n')
    parser.finish()

    expect(messages).toEqual(['{"status":"running"}', 'first\nsecond'])
  })
})
