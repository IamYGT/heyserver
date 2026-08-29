export interface AgentTask {
  id: number
  node_id: string
  kind: string
  payload?: Record<string, string>
  status: 'queued' | 'running' | 'completed' | 'failed'
  result?: Record<string, string>
  error?: string
  created_at?: string
  started_at?: string
  completed_at?: string
}

interface WaitOptions {
  attempts?: number
  intervalMs?: number
  delay?: (milliseconds: number) => Promise<void>
}

const sleep = (milliseconds: number) => new Promise<void>((resolve) => window.setTimeout(resolve, milliseconds))

export async function waitForAgentTask(
  initial: AgentTask,
  readTask: (taskID: number) => Promise<AgentTask>,
  options: WaitOptions = {},
): Promise<AgentTask> {
  const attempts = options.attempts ?? 60
  const intervalMs = options.intervalMs ?? 2_000
  const delay = options.delay ?? sleep
  let task = initial

  for (let attempt = 0; ; attempt += 1) {
    if (task.status === 'completed') return task
    if (task.status === 'failed') throw new Error(task.error || `Agent task #${task.id} failed`)
    if (attempt >= attempts) break
    await delay(intervalMs)
    task = await readTask(task.id)
  }

  throw new Error(`Agent task #${task.id} is still ${task.status} after ${Math.round(attempts * intervalMs / 1000)} seconds`)
}
