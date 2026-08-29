import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { Copy, Download, Loader2, Server, ShieldCheck, X } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  DeployProfileFields,
  emptyDeployProfile,
  deployProfileEnvLines,
  isDeployProfileValid,
  type DeployProfile,
} from './DeployProfileFields'

interface EnrollmentResponse {
  node: { id: string; name: string }
  token: string
}

interface AgentEnrollmentProps {
  onClose: () => void
  onRegistered: (nodeID: string) => Promise<void> | void
}

const nodeIDPattern = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/

function agentEnvironment(nodeID: string, deployProfile: DeployProfile): string {
  return [
    `HSERVER_AGENT_HUB_URL=${window.location.origin}`,
    `HSERVER_AGENT_NODE_ID=${nodeID}`,
    'HSERVER_AGENT_TOKEN_FILE=/etc/hserver-agent.token',
    'HSERVER_AGENT_INTERVAL=30s',
    'HSERVER_AGENT_OBSERVED_SERVICES=',
    'HSERVER_AGENT_ALLOWED_SERVICES=',
    'HSERVER_AGENT_ALLOWED_HOST_ACTIONS=memory-optimize,swap-reset,temp-clean,reboot,reboot-cancel',
    'HSERVER_AGENT_ALLOW_PROCESS_SIGNALS=true',
    'HSERVER_AGENT_ALLOW_TERMINAL=true',
    'HSERVER_AGENT_ALLOWED_DISK_CLEANUP=apt-cache,journal,tmp-old,rotated-logs',
    'HSERVER_AGENT_ALLOWED_LOG_SOURCES=system,nginx,php,mariadb,postgresql,pm2,docker',
    'HSERVER_AGENT_ALLOW_CONTAINER_READ=true',
    'HSERVER_AGENT_ALLOWED_CONTAINER_ACTIONS=start,restart,stop',
    'HSERVER_AGENT_ALLOWED_NGINX_ACTIONS=test,reload',
    'HSERVER_AGENT_ALLOW_NGINX_CONFIG_READ=true',
    'HSERVER_AGENT_ALLOW_NGINX_CONFIG_WRITE=true',
    'HSERVER_AGENT_NGINX_SITES_AVAILABLE=/etc/nginx/sites-available',
    'HSERVER_AGENT_NGINX_SITES_ENABLED=/etc/nginx/sites-enabled',
		'HSERVER_AGENT_ALLOW_DOMAIN_READ=true',
		'HSERVER_AGENT_ALLOW_DOMAIN_ACTIONS=true',
    ...deployProfileEnvLines(deployProfile),
    'HSERVER_AGENT_ALLOWED_PHP_ACTIONS=test,reload,restart',
    'HSERVER_AGENT_ALLOW_PHP_CONFIG_READ=true',
    'HSERVER_AGENT_ALLOW_PHP_CONFIG_WRITE=true',
    'HSERVER_AGENT_PHP_CONFIG_ROOT=/etc/php',
    'HSERVER_AGENT_PHP_BINARY_ROOT=/usr/sbin',
    'HSERVER_AGENT_ALLOW_PM2_READ=false',
    'HSERVER_AGENT_ALLOWED_PM2_ACTIONS=',
    'HSERVER_AGENT_PM2_BINARY=',
    'HSERVER_AGENT_PM2_HOME=',
    'HSERVER_AGENT_PM2_USER=',
    'HSERVER_AGENT_ALLOW_CRON_READ=true',
    'HSERVER_AGENT_ALLOW_CRON_WRITE=true',
    'HSERVER_AGENT_ALLOW_CRON_RUN=true',
    'HSERVER_AGENT_CRON_STATE_PATH=/etc/hserver/cron-jobs.json',
    'HSERVER_AGENT_CRON_FILE_PATH=/etc/cron.d/hserver-managed',
    'HSERVER_AGENT_CRON_LOCK_PATH=/run/lock/hserver-cron.lock',
    'HSERVER_AGENT_CRONTAB_BINARY=/usr/bin/crontab',
    'HSERVER_AGENT_RUNUSER_BINARY=/usr/sbin/runuser',
    'HSERVER_AGENT_CRON_SHELL=/bin/bash',
    'HSERVER_AGENT_CRON_SERVICE=cron.service',
		'HSERVER_AGENT_ALLOW_FIREWALL_READ=true',
		'HSERVER_AGENT_ALLOW_FIREWALL_WRITE=true',
		'HSERVER_AGENT_IPTABLES_BINARY=/usr/sbin/iptables',
		'HSERVER_AGENT_FIREWALL_SAVE_BINARY=/usr/sbin/netfilter-persistent',
		'HSERVER_AGENT_FIREWALL_LOCK_PATH=/run/lock/hserver-firewall.lock',
		'HSERVER_AGENT_FIREWALL_PERSISTENCE_SERVICE=netfilter-persistent.service',
		'HSERVER_AGENT_FIREWALL_PERSISTENCE_PATH=/etc/iptables',
		'HSERVER_AGENT_FIREWALL_PROTECTED_SOURCES=',
		'HSERVER_AGENT_FIREWALL_PROTECTED_PORTS=22',
    '',
  ].join('\n')
}

export function AgentEnrollment({ onClose, onRegistered }: AgentEnrollmentProps) {
  const [nodeID, setNodeID] = useState('')
  const [name, setName] = useState('')
  const [deployProfile, setDeployProfile] = useState<DeployProfile>(emptyDeployProfile)
  const [enrollment, setEnrollment] = useState<EnrollmentResponse | null>(null)

  const valid = nodeIDPattern.test(nodeID) && name.trim().length > 0 && isDeployProfileValid(deployProfile)

  const register = useMutation<EnrollmentResponse, Error>({
    mutationFn: () => api.post('/nodes', { id: nodeID, name: name.trim() }),
    onSuccess: async (result) => {
      setEnrollment(result)
      await onRegistered(result.node.id)
      toast.success(`${result.node.name} enrollment created`)
    },
    onError: (error) => toast.error(error.message || 'Could not create agent enrollment'),
  })

  const copyToken = async () => {
    if (!enrollment) return
    try {
      await navigator.clipboard.writeText(enrollment.token)
      toast.success('Enrollment token copied')
    } catch {
      toast.error('Could not copy the enrollment token')
    }
  }

  const downloadEnvironment = () => {
    if (!enrollment) return
    const url = URL.createObjectURL(new Blob([agentEnvironment(enrollment.node.id, deployProfile)], { type: 'text/plain;charset=utf-8' }))
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = `hserver-agent-${enrollment.node.id}.env`
    document.body.appendChild(anchor)
    anchor.click()
    anchor.remove()
    URL.revokeObjectURL(url)
  }

  const close = () => {
    setEnrollment(null)
    onClose()
  }

  return (
    <Card className="border-violet-500/25 bg-violet-500/[0.04]">
      <CardHeader className="flex-row items-start justify-between gap-4">
        <div>
          <CardTitle className="flex items-center gap-2 text-sm text-zinc-100"><Server className="size-4 text-violet-400" /> Enroll a managed server</CardTitle>
          <p className="mt-1 text-xs text-zinc-500">The agent connects outbound to this HServer installation; no provider-specific network or inbound SSH is required.</p>
        </div>
        <Button type="button" variant="ghost" size="icon-xs" onClick={close} aria-label="Close server enrollment"><X className="size-3.5" /></Button>
      </CardHeader>
      <CardContent>
        {!enrollment ? (
          <form className="space-y-4" onSubmit={(event) => { event.preventDefault(); if (valid) register.mutate() }}>
            <div className="grid gap-3 md:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)_auto] md:items-end">
              <label className="space-y-1.5 text-xs text-zinc-400">
                <span className="font-medium text-zinc-300">Node ID</span>
                <input value={nodeID} onChange={(event) => setNodeID(event.target.value)} placeholder="production-1" autoComplete="off" className="h-9 w-full rounded-lg border border-zinc-700 bg-zinc-950 px-3 font-mono text-xs text-zinc-100 outline-none focus:border-violet-500" />
                {nodeID && !nodeIDPattern.test(nodeID) && <span className="block text-[10px] text-red-400">Use letters, digits, dot, underscore, or hyphen.</span>}
              </label>
              <label className="space-y-1.5 text-xs text-zinc-400">
                <span className="font-medium text-zinc-300">Display name</span>
                <input value={name} onChange={(event) => setName(event.target.value)} placeholder="Production server" autoComplete="off" className="h-9 w-full rounded-lg border border-zinc-700 bg-zinc-950 px-3 text-xs text-zinc-100 outline-none focus:border-violet-500" />
              </label>
              <Button type="submit" disabled={!valid || register.isPending}>{register.isPending && <Loader2 className="size-4 animate-spin" />} Create enrollment</Button>
            </div>

            <DeployProfileFields
              value={deployProfile}
              onChange={setDeployProfile}
              title="Deploy controls"
              description="Optional local deploy-plan access. Permissions are closed by default; the generated config uses only paths you enter here."
              idPrefix="enrollment-deploy-profile"
            />
          </form>
        ) : (
          <div className="space-y-4">
            <div className="rounded-xl border border-amber-500/25 bg-amber-500/[0.06] p-3">
              <p className="flex items-center gap-2 text-xs font-semibold text-amber-300"><ShieldCheck className="size-4" /> Save this enrollment token now</p>
              <p className="mt-1 text-[10px] text-amber-300/70">It is returned once and is never stored by HServer in recoverable form.</p>
              <code className="mt-3 block select-all overflow-x-auto rounded-lg bg-zinc-950 p-3 text-xs text-zinc-200">{enrollment.token}</code>
            </div>
            <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-center">
              <div className="text-[11px] leading-relaxed text-zinc-500">
                Install the release package's <code>hserver-agent</code> binary and systemd unit on the target server. Save the token as <code>/etc/hserver-agent.token</code>, then install the generated environment file as <code>/etc/hserver-agent.env</code>.
              </div>
              <div className="flex flex-wrap gap-2">
                <Button type="button" variant="outline" size="sm" onClick={copyToken}><Copy className="size-3.5" /> Copy token</Button>
                <Button type="button" variant="outline" size="sm" onClick={downloadEnvironment}><Download className="size-3.5" /> Download config</Button>
                <Button type="button" size="sm" onClick={close}>Done</Button>
              </div>
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  )
}
