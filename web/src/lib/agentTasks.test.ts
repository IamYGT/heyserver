import { describe, expect, it, vi } from 'vitest'
import { waitForAgentTask, type AgentTask } from './agentTasks'

const queued: AgentTask = {
  id: 41,
  node_id: 'contabo',
  kind: 'service.action',
  status: 'queued',
}

describe('waitForAgentTask', () => {
  it('waits for the agent-reported terminal result', async () => {
    const read = vi.fn()
      .mockResolvedValueOnce({ ...queued, status: 'running' })
      .mockResolvedValueOnce({ ...queued, status: 'completed', result: { active: 'active', sub: 'running' } })

    await expect(waitForAgentTask(queued, read, { attempts: 3, intervalMs: 0, delay: async () => {} }))
      .resolves.toMatchObject({ status: 'completed', result: { active: 'active' } })
    expect(read).toHaveBeenCalledTimes(2)
  })

  it('surfaces the agent failure instead of reporting a queued action as success', async () => {
    const read = vi.fn().mockResolvedValue({ ...queued, status: 'failed', error: 'systemctl restart failed' })

    await expect(waitForAgentTask(queued, read, { attempts: 2, intervalMs: 0, delay: async () => {} }))
      .rejects.toThrow('systemctl restart failed')
  })

  it('reports a bounded timeout while leaving the server-side task untouched', async () => {
    const read = vi.fn().mockResolvedValue({ ...queued, status: 'running' })

    await expect(waitForAgentTask(queued, read, { attempts: 2, intervalMs: 1_000, delay: async () => {} }))
      .rejects.toThrow('still running after 2 seconds')
  })
})
