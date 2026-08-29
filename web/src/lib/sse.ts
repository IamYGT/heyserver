import { authenticatedFetch } from '@/lib/api'

export function createSSEParser(onMessage: (data: string) => void) {
  let buffer = ''

  const emitFrames = (flush = false) => {
    buffer = buffer.replace(/\r\n/g, '\n')
    let boundary = buffer.indexOf('\n\n')
    while (boundary >= 0) {
      const frame = buffer.slice(0, boundary)
      buffer = buffer.slice(boundary + 2)
      const data = frame
        .split('\n')
        .filter((line) => line.startsWith('data:'))
        .map((line) => line.slice(5).replace(/^ /, ''))
        .join('\n')
      if (data) onMessage(data)
      boundary = buffer.indexOf('\n\n')
    }

    if (flush && buffer) {
      const data = buffer
        .split('\n')
        .filter((line) => line.startsWith('data:'))
        .map((line) => line.slice(5).replace(/^ /, ''))
        .join('\n')
      if (data) onMessage(data)
      buffer = ''
    }
  }

  return {
    push(chunk: string) {
      buffer += chunk
      emitFrames()
    },
    finish() {
      emitFrames(true)
    },
  }
}

export async function consumeAuthenticatedEventStream(
  path: string,
  signal: AbortSignal,
  onOpen: () => void,
  onMessage: (data: string) => void,
) {
  const response = await authenticatedFetch(path, {
    signal,
    headers: { Accept: 'text/event-stream' },
  })
  if (!response.ok) throw new Error(`Stream request failed with HTTP ${response.status}`)
  if (!response.body) throw new Error('Streaming response body is unavailable')

  onOpen()
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  const parser = createSSEParser(onMessage)
  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    parser.push(decoder.decode(value, { stream: true }))
  }
  parser.push(decoder.decode())
  parser.finish()
}
