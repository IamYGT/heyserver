import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, AlertTriangle, ExternalLink, Globe2, Loader2, LockKeyhole, RefreshCw, Trash2 } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { api } from '@/lib/api'
import type { ComposeProjectService, DeployProjectDomain, DeployProjectDomainHealth } from '@/lib/types'
import { toast } from 'sonner'

interface ProjectDomainsDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  targetId: string | null
  targetName: string
  canManage: boolean
}

export function ProjectDomainsDialog({ open, onOpenChange, targetId, targetName, canManage }: ProjectDomainsDialogProps) {
  const queryClient = useQueryClient()
  const [domain, setDomain] = useState('')
  const [service, setService] = useState('')
  const [hostPort, setHostPort] = useState('')
  const [pendingDelete, setPendingDelete] = useState<string | null>(null)
  const [health, setHealth] = useState<Record<string, DeployProjectDomainHealth>>({})
  const [healthLoading, setHealthLoading] = useState<string | null>(null)
  const [tlsTarget, setTLSTarget] = useState<string | null>(null)
  const [tlsEmail, setTLSEmail] = useState('')
  const [pendingTLSDisable, setPendingTLSDisable] = useState<string | null>(null)

  const domainsQuery = useQuery<DeployProjectDomain[]>({
    queryKey: ['deploy', 'domains', targetId],
    queryFn: () => api.get<DeployProjectDomain[]>(`/deploy/targets/${targetId}/domains`),
    enabled: open && targetId !== null,
    retry: false,
  })
  const servicesQuery = useQuery<ComposeProjectService[]>({
    queryKey: ['deploy', 'domain-services', targetId],
    queryFn: () => api.get<ComposeProjectService[]>(`/deploy/targets/${targetId}/services`),
    enabled: open && targetId !== null && canManage,
    retry: false,
  })
  const serviceNames = useMemo(
    () => Array.from(new Set((servicesQuery.data ?? []).map((item) => item.service))).sort(),
    [servicesQuery.data],
  )

  const createDomain = useMutation({
    mutationFn: (request: { targetId: string; domain: string; service: string; hostPort: number }) =>
      api.post<DeployProjectDomain>(`/deploy/targets/${request.targetId}/domains`, {
        domain: request.domain,
        service: request.service,
        hostPort: request.hostPort,
      }),
    onSuccess: (created, request) => {
      queryClient.setQueryData<DeployProjectDomain[]>(['deploy', 'domains', request.targetId], (current = []) =>
        [...current, created].sort((left, right) => left.domain.localeCompare(right.domain)),
      )
      setDomain('')
      setService('')
      setHostPort('')
      toast.success('Project domain activated')
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : 'Failed to activate project domain'),
  })

  const deleteDomain = useMutation({
    mutationFn: (request: { targetId: string; domainId: string }) =>
      api.delete(`/deploy/targets/${request.targetId}/domains/${request.domainId}`),
    onSuccess: (_response, request) => {
      queryClient.setQueryData<DeployProjectDomain[]>(['deploy', 'domains', request.targetId], (current = []) =>
        current.filter((item) => item.id !== request.domainId),
      )
      setHealth((current) => {
        const next = { ...current }
        delete next[request.domainId]
        return next
      })
      setPendingDelete(null)
      toast.success('Project domain removed')
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : 'Failed to remove project domain'),
  })

  const updateDomain = (target: string, updated: DeployProjectDomain) => {
    queryClient.setQueryData<DeployProjectDomain[]>(['deploy', 'domains', target], (current = []) =>
      current.map((item) => item.id === updated.id ? updated : item),
    )
  }

  const enableTLS = useMutation({
    mutationFn: (request: { targetId: string; domainId: string; email: string }) =>
      api.post<DeployProjectDomain>(`/deploy/targets/${request.targetId}/domains/${request.domainId}/tls`, { email: request.email }),
    onSuccess: (updated, request) => {
      updateDomain(request.targetId, updated)
      setTLSTarget(null)
      setTLSEmail('')
      toast.success('Managed TLS certificate activated')
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : 'Failed to activate managed TLS'),
  })

  const disableTLS = useMutation({
    mutationFn: (request: { targetId: string; domainId: string }) =>
      api.delete<DeployProjectDomain>(`/deploy/targets/${request.targetId}/domains/${request.domainId}/tls`),
    onSuccess: (updated, request) => {
      updateDomain(request.targetId, updated)
      setPendingTLSDisable(null)
      toast.success('Managed TLS disabled; certificate files were preserved')
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : 'Failed to disable managed TLS'),
  })

  const probe = async (domainId: string) => {
    if (!targetId) return
    setHealthLoading(domainId)
    try {
      const result = await api.get<DeployProjectDomainHealth>(`/deploy/targets/${targetId}/domains/${domainId}/health`)
      setHealth((current) => ({ ...current, [domainId]: result }))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Upstream health probe failed')
    } finally {
      setHealthLoading(null)
    }
  }

  const close = (nextOpen: boolean) => {
    if (!nextOpen) {
      setDomain('')
      setService('')
      setHostPort('')
      setPendingDelete(null)
      setHealth({})
      setTLSTarget(null)
      setTLSEmail('')
      setPendingTLSDisable(null)
    }
    onOpenChange(nextOpen)
  }

  const parsedPort = Number(hostPort)
  const canSubmit = Boolean(targetId && domain.trim() && service.trim() && Number.isInteger(parsedPort) && parsedPort >= 1 && parsedPort <= 65535)

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="max-h-[85vh] max-w-2xl overflow-y-auto border-zinc-800 bg-zinc-900 text-white">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-white">
            <Globe2 className="size-4 text-sky-400" />Project Domains · {targetName}
          </DialogTitle>
        </DialogHeader>

        <div className="space-y-5 py-1">
          <div className="rounded-lg border border-sky-500/20 bg-sky-500/[0.05] p-3">
            <p className="text-xs font-medium text-sky-200">Nginx → loopback published port</p>
            <p className="mt-1 text-[11px] leading-5 text-zinc-500">
              Heyserver creates an HTTP virtual host that proxies only to 127.0.0.1. TLS is reported separately and is not implied by a running container.
            </p>
          </div>

          <div className="space-y-2">
            <div className="flex items-center gap-2">
              <Label className="text-sm text-zinc-300">Active mappings</Label>
              <Button aria-label="Refresh project domains" type="button" variant="ghost" size="icon" className="ml-auto size-7 text-zinc-500 hover:text-sky-300" onClick={() => { void domainsQuery.refetch() }} disabled={domainsQuery.isFetching}>
                <RefreshCw className={`size-3.5 ${domainsQuery.isFetching ? 'animate-spin' : ''}`} />
              </Button>
            </div>
            {domainsQuery.isLoading ? (
              <div className="space-y-2"><Skeleton className="h-24 bg-zinc-800" /><Skeleton className="h-24 bg-zinc-800" /></div>
            ) : domainsQuery.isError ? (
              <div className="rounded-lg border border-red-500/20 bg-red-500/[0.05] p-3">
                <p className="flex items-center gap-2 text-xs text-red-300"><AlertTriangle className="size-3.5" />Project domains could not be loaded.</p>
                <p className="mt-1 text-[11px] text-zinc-600">{domainsQuery.error.message}</p>
              </div>
            ) : domainsQuery.data && domainsQuery.data.length > 0 ? (
              <div className="space-y-2">
                {domainsQuery.data.map((item) => {
                  const result = health[item.id]
                  const tlsConfigured = item.tlsStatus !== 'not_configured'
                  const tlsStyle = item.tlsStatus === 'healthy'
                    ? 'border-emerald-500/20 bg-emerald-500/10 text-emerald-300'
                    : item.tlsStatus === 'expiring'
                      ? 'border-amber-500/20 bg-amber-500/10 text-amber-300'
                      : item.tlsStatus === 'expired' || item.tlsStatus === 'unavailable'
                        ? 'border-red-500/20 bg-red-500/10 text-red-300'
                        : 'border-zinc-700 bg-zinc-800 text-zinc-500'
                  const healthStyle = result?.status === 'healthy'
                    ? 'border-emerald-500/20 bg-emerald-500/10 text-emerald-300'
                    : result?.status === 'unhealthy'
                      ? 'border-amber-500/20 bg-amber-500/10 text-amber-300'
                      : 'border-zinc-700 bg-zinc-800 text-zinc-400'
                  return (
                    <div key={item.id} className="rounded-lg border border-zinc-800 bg-zinc-950/60 p-3">
                      <div className="flex flex-wrap items-start gap-3">
                        <div className="min-w-0 flex-1">
                          <div className="flex flex-wrap items-center gap-2">
                            <a href={`${tlsConfigured ? 'https' : 'http'}://${item.domain}`} target="_blank" rel="noreferrer" className="flex items-center gap-1 text-sm font-medium text-white hover:text-sky-300">
                              {item.domain}<ExternalLink className="size-3" />
                            </a>
                            <Badge className={`border text-[10px] ${tlsStyle}`}>TLS {item.tlsStatus.replace('_', ' ')}</Badge>
                            {result && <Badge className={`border text-[10px] ${healthStyle}`}>{result.status}</Badge>}
                          </div>
                          <p className="mt-1 font-mono text-[11px] text-zinc-500">{item.service} · {item.upstream}</p>
                          <p className="mt-1 text-[11px] text-zinc-500">{item.tlsMessage}{item.tlsExpiresAt ? ` Expires ${new Date(item.tlsExpiresAt).toLocaleDateString()} (${item.tlsDaysRemaining ?? 0} days).` : ''}</p>
                          {result && <p className="mt-1 text-[11px] text-zinc-500">{result.message} {result.statusCode ? `HTTP ${result.statusCode} · ` : ''}{result.latencyMs} ms</p>}
                        </div>
                        <div className="flex items-center gap-1">
                          <Button aria-label={`Probe ${item.domain}`} type="button" variant="ghost" size="icon" className="size-7 text-zinc-500 hover:text-emerald-300" onClick={() => { void probe(item.id) }} disabled={healthLoading === item.id}>
                            {healthLoading === item.id ? <Loader2 className="size-3.5 animate-spin" /> : <Activity className="size-3.5" />}
                          </Button>
                          {canManage && !tlsConfigured && (
                            <Button aria-label={`Configure TLS for ${item.domain}`} type="button" variant="ghost" size="icon" className="size-7 text-zinc-500 hover:text-emerald-300" onClick={() => { setTLSTarget(item.id); setTLSEmail('') }}><LockKeyhole className="size-3.5" /></Button>
                          )}
                          {canManage && tlsConfigured && (pendingTLSDisable === item.id ? (
                            <div className="flex items-center gap-1">
                              <Button type="button" variant="ghost" size="sm" className="h-7 text-xs text-zinc-400" onClick={() => setPendingTLSDisable(null)}>Cancel</Button>
                              <Button type="button" variant="destructive" size="sm" className="h-7 text-xs" onClick={() => { if (targetId) disableTLS.mutate({ targetId, domainId: item.id }) }} disabled={disableTLS.isPending}>Disable</Button>
                            </div>
                          ) : (
                            <Button aria-label={`Disable TLS for ${item.domain}`} type="button" variant="ghost" size="icon" className="size-7 text-emerald-400 hover:text-amber-300" onClick={() => setPendingTLSDisable(item.id)}><LockKeyhole className="size-3.5" /></Button>
                          ))}
                          {canManage && (pendingDelete === item.id ? (
                            <div className="flex items-center gap-1">
                              <Button type="button" variant="ghost" size="sm" className="h-7 text-xs text-zinc-400" onClick={() => setPendingDelete(null)}>Cancel</Button>
                              <Button type="button" variant="destructive" size="sm" className="h-7 text-xs" onClick={() => { if (targetId) deleteDomain.mutate({ targetId, domainId: item.id }) }} disabled={deleteDomain.isPending}>Remove</Button>
                            </div>
                          ) : (
                            <Button aria-label={`Remove ${item.domain}`} type="button" variant="ghost" size="icon" className="size-7 text-zinc-600 hover:text-red-300" onClick={() => setPendingDelete(item.id)}><Trash2 className="size-3.5" /></Button>
                          ))}
                        </div>
                      </div>
                      {canManage && tlsTarget === item.id && (
                        <div className="mt-3 flex flex-col gap-2 border-t border-zinc-800 pt-3 sm:flex-row sm:flex-wrap sm:items-end">
                          <div className="min-w-0 flex-1 space-y-1.5">
                            <Label htmlFor={`project-domain-tls-email-${item.id}`} className="text-xs text-zinc-400">ACME account email (optional)</Label>
                            <Input id={`project-domain-tls-email-${item.id}`} type="email" autoComplete="email" placeholder="admin@example.com" value={tlsEmail} onChange={(event) => setTLSEmail(event.target.value)} className="border-zinc-700 bg-zinc-800 text-white" />
                          </div>
                          <div className="flex items-center gap-2">
                            <Button type="button" variant="ghost" size="sm" className="text-zinc-400" onClick={() => { setTLSTarget(null); setTLSEmail('') }}>Cancel</Button>
                            <Button type="button" size="sm" className="bg-emerald-600 text-white hover:bg-emerald-500" onClick={() => { if (targetId) enableTLS.mutate({ targetId, domainId: item.id, email: tlsEmail.trim() }) }} disabled={enableTLS.isPending}>
                              {enableTLS.isPending && <Loader2 className="mr-2 size-3.5 animate-spin" />}Issue certificate
                            </Button>
                          </div>
                          <p className="text-[11px] leading-5 text-zinc-600 sm:basis-full">HTTP-01 must reach this host on port 80. Heyserver keeps the ACME challenge route available and redirects other HTTP traffic after issuance.</p>
                        </div>
                      )}
                    </div>
                  )
                })}
              </div>
            ) : (
              <p className="rounded-lg border border-zinc-800 bg-zinc-950/50 px-3 py-6 text-center text-xs text-zinc-600">No project domains configured.</p>
            )}
          </div>

          {canManage && (
            <div className="grid gap-3 rounded-lg border border-zinc-800 bg-zinc-950/40 p-3 sm:grid-cols-[1.4fr_1fr_0.7fr]">
              <div className="space-y-1.5">
                <Label htmlFor="project-domain-name" className="text-xs text-zinc-400">Domain</Label>
                <Input id="project-domain-name" autoComplete="off" placeholder="app.example.com" value={domain} onChange={(event) => setDomain(event.target.value.toLowerCase())} className="border-zinc-700 bg-zinc-800 text-white" />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="project-domain-service" className="text-xs text-zinc-400">Compose service</Label>
                <Input id="project-domain-service" list="project-domain-services" autoComplete="off" placeholder="web" value={service} onChange={(event) => setService(event.target.value)} className="border-zinc-700 bg-zinc-800 font-mono text-white" />
                <datalist id="project-domain-services">{serviceNames.map((name) => <option key={name} value={name} />)}</datalist>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="project-domain-port" className="text-xs text-zinc-400">Host port</Label>
                <Input id="project-domain-port" type="number" min={1} max={65535} placeholder="8080" value={hostPort} onChange={(event) => setHostPort(event.target.value)} className="border-zinc-700 bg-zinc-800 font-mono text-white" />
              </div>
              <p className="text-[11px] leading-5 text-zinc-600 sm:col-span-3">Use the service&apos;s published host port, not its container-only port. The mapping remains HTTP-only until TLS is configured.</p>
            </div>
          )}
        </div>

        <DialogFooter>
          <Button type="button" variant="ghost" className="text-zinc-400" onClick={() => close(false)}>Close</Button>
          {canManage && (
            <Button type="button" className="bg-sky-600 text-white hover:bg-sky-500" onClick={() => { if (targetId) createDomain.mutate({ targetId, domain: domain.trim(), service: service.trim(), hostPort: parsedPort }) }} disabled={!canSubmit || createDomain.isPending}>
              {createDomain.isPending && <Loader2 className="mr-2 size-3.5 animate-spin" />}Activate domain
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
