import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Lock,
  Plus,
  Trash2,
  Loader2,
  AlertTriangle,
  ShieldCheck,
  ShieldOff,
  Shield,
} from 'lucide-react'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import { cn } from '@/lib/utils'
import { DependencyRemediation } from '@/components/DependencyRemediation'

// ─── Types ─────────────────────────────────────────────────────────────────────

interface FirewallRule {
  number: number
  to: string
  action: 'ALLOW' | 'DENY' | 'LIMIT' | 'REJECT'
  from: string
  protocol?: string
  comment?: string
}

interface FirewallStatus {
  available: boolean
  state: 'healthy' | 'ufw-missing' | 'unavailable'
  backend: 'ufw' | 'iptables' | 'none'
  error?: string
  active: boolean
  loggingLevel?: string
  defaultIncoming: string
  defaultOutgoing: string
  rules: FirewallRule[]
}

// ─── Quick Presets ─────────────────────────────────────────────────────────────

const PRESETS = [
  { label: 'SSH', port: '22', protocol: 'tcp' },
  { label: 'HTTP', port: '80', protocol: 'tcp' },
  { label: 'HTTPS', port: '443', protocol: 'tcp' },
  { label: 'PostgreSQL', port: '5432', protocol: 'tcp' },
] as const

// ─── Add Rule Dialog ───────────────────────────────────────────────────────────

interface RuleFormState {
  direction: 'in' | 'out'
  action: 'allow' | 'deny' | 'limit' | 'reject'
  protocol: 'tcp' | 'udp' | 'any'
  port: string
  from: string
  comment: string
}

const defaultRuleForm = (): RuleFormState => ({
  direction: 'in',
  action: 'allow',
  protocol: 'tcp',
  port: '',
  from: 'any',
  comment: '',
})

interface AddRuleDialogProps {
  open: boolean
  onClose: () => void
  onSave: (form: RuleFormState) => void
  isPending: boolean
  initialPreset?: Partial<RuleFormState>
}

function AddRuleDialog({ open, onClose, onSave, isPending, initialPreset }: AddRuleDialogProps) {
  const [form, setForm] = useState<RuleFormState>(() => ({
    ...defaultRuleForm(),
    ...initialPreset,
  }))

  function field<K extends keyof RuleFormState>(key: K, value: RuleFormState[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  function handleSubmit() {
    if (!form.port.trim()) {
      toast.error('Port is required')
      return
    }
    onSave(form)
  }

  const handleOpenChange = (o: boolean) => {
    if (o) setForm({ ...defaultRuleForm(), ...initialPreset })
    if (!o) onClose()
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-white">Add Firewall Rule</DialogTitle>
        </DialogHeader>

        <div className="space-y-4 py-2">
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <label className="text-zinc-400 text-xs font-medium">Direction</label>
              <select
                value={form.direction}
                onChange={(e) => field('direction', e.target.value as RuleFormState['direction'])}
                className="w-full bg-zinc-800 border border-zinc-700 text-white rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors"
              >
                <option value="in">Inbound (in)</option>
                <option value="out">Outbound (out)</option>
              </select>
            </div>
            <div className="space-y-1.5">
              <label className="text-zinc-400 text-xs font-medium">Action</label>
              <select
                value={form.action}
                onChange={(e) => field('action', e.target.value as RuleFormState['action'])}
                className="w-full bg-zinc-800 border border-zinc-700 text-white rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors"
              >
                <option value="allow">ALLOW</option>
                <option value="deny">DENY</option>
                <option value="limit">LIMIT</option>
                <option value="reject">REJECT</option>
              </select>
            </div>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <label className="text-zinc-400 text-xs font-medium">Protocol</label>
              <select
                value={form.protocol}
                onChange={(e) => field('protocol', e.target.value as RuleFormState['protocol'])}
                className="w-full bg-zinc-800 border border-zinc-700 text-white rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors"
              >
                <option value="tcp">TCP</option>
                <option value="udp">UDP</option>
                <option value="any">Any</option>
              </select>
            </div>
            <div className="space-y-1.5">
              <label className="text-zinc-400 text-xs font-medium">Port</label>
              <input
                type="text"
                value={form.port}
                onChange={(e) => field('port', e.target.value)}
                placeholder="22, 80-90, 443"
                className="w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors font-mono"
              />
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="text-zinc-400 text-xs font-medium">Source / From</label>
            <input
              type="text"
              value={form.from}
              onChange={(e) => field('from', e.target.value)}
              placeholder="any, 192.168.1.0/24, 1.2.3.4"
              className="w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors font-mono"
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-zinc-400 text-xs font-medium">Comment (optional)</label>
            <input
              type="text"
              value={form.comment}
              onChange={(e) => field('comment', e.target.value)}
              placeholder="e.g. Web server traffic"
              className="w-full bg-zinc-800 border border-zinc-700 text-white placeholder:text-zinc-600 rounded-md px-3 py-2 text-sm focus:outline-none focus:border-blue-500 transition-colors"
            />
          </div>
        </div>

        <DialogFooter className="gap-2">
          <Button
            variant="ghost"
            onClick={onClose}
            className="text-zinc-400 hover:text-white hover:bg-zinc-800"
          >
            Cancel
          </Button>
          <Button
            onClick={handleSubmit}
            disabled={isPending}
            className="bg-blue-600 hover:bg-blue-500 text-white"
          >
            {isPending && <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />}
            Add Rule
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Delete Confirmation Dialog ────────────────────────────────────────────────

interface ConfirmDeleteDialogProps {
  open: boolean
  ruleNumber: number | null
  onConfirm: () => void
  onClose: () => void
  isPending: boolean
}

function ConfirmDeleteDialog({
  open,
  ruleNumber,
  onConfirm,
  onClose,
  isPending,
}: ConfirmDeleteDialogProps) {
  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-md overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-white flex items-center gap-2">
            <AlertTriangle className="w-4 h-4 text-red-400" />
            Delete Firewall Rule
          </DialogTitle>
        </DialogHeader>

        <div className="py-2 space-y-3">
          <p className="text-zinc-400 text-sm">
            Are you sure you want to delete rule{' '}
            <span className="text-white font-mono font-semibold">#{ruleNumber}</span>?
            This cannot be undone.
          </p>
          <div className="flex items-start gap-2.5 bg-amber-500/10 border border-amber-500/20 rounded-lg px-3 py-2.5">
            <AlertTriangle className="w-4 h-4 text-amber-400 flex-shrink-0 mt-0.5" />
            <p className="text-amber-300 text-xs leading-relaxed">
              <strong>Warning:</strong> Do not delete SSH (port 22) rules while connected via SSH — you will lose access to the server.
            </p>
          </div>
        </div>

        <DialogFooter className="gap-2">
          <Button
            variant="ghost"
            onClick={onClose}
            className="text-zinc-400 hover:text-white hover:bg-zinc-800"
          >
            Cancel
          </Button>
          <Button
            onClick={onConfirm}
            disabled={isPending}
            className="bg-red-600 hover:bg-red-500 text-white"
          >
            {isPending && <Loader2 className="w-3.5 h-3.5 mr-2 animate-spin" />}
            Delete Rule
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Action Badge ──────────────────────────────────────────────────────────────

function ActionBadge({ action }: { action: string }) {
  const styles: Record<string, string> = {
    ALLOW: 'bg-green-500/10 text-green-400 border-green-500/20',
    DENY: 'bg-red-500/10 text-red-400 border-red-500/20',
    LIMIT: 'bg-amber-500/10 text-amber-400 border-amber-500/20',
    REJECT: 'bg-orange-500/10 text-orange-400 border-orange-500/20',
  }
  const style = styles[action.toUpperCase()] ?? 'bg-zinc-500/10 text-zinc-400 border-zinc-500/20'
  return (
    <Badge className={cn('text-xs font-mono border', style)}>
      {action.toUpperCase()}
    </Badge>
  )
}

// ─── Main Firewall Page ────────────────────────────────────────────────────────

export default function Firewall() {
  const queryClient = useQueryClient()
  const [addOpen, setAddOpen] = useState(false)
  const [deleteRule, setDeleteRule] = useState<number | null>(null)
  const [presetForm, setPresetForm] = useState<Partial<RuleFormState> | undefined>()

  const statusQuery = useQuery<FirewallStatus>({
    queryKey: ['firewall-status'],
    queryFn: () => api.get<FirewallStatus>('/firewall/status'),
    refetchInterval: 15_000,
  })
  const status = statusQuery.data
  const managementUnavailable = statusQuery.isError || status?.available === false

  const toggleMutation = useMutation({
    mutationFn: () => api.post('/firewall/toggle', { enable: !status?.active }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['firewall-status'] })
      toast.success(`Firewall ${status?.active ? 'disabled' : 'enabled'}`)
    },
    onError: () => toast.error('Failed to toggle firewall'),
  })

  const addRuleMutation = useMutation({
    mutationFn: (form: RuleFormState) => api.post('/firewall/rules', form),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['firewall-status'] })
      setAddOpen(false)
      setPresetForm(undefined)
      toast.success('Rule added')
    },
    onError: () => toast.error('Failed to add rule'),
  })

  const deleteRuleMutation = useMutation({
    mutationFn: (number: number) => api.delete(`/firewall/rules/${number}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['firewall-status'] })
      setDeleteRule(null)
      toast.success('Rule deleted')
    },
    onError: () => toast.error('Failed to delete rule'),
  })

  function openPreset(preset: (typeof PRESETS)[number]) {
    setPresetForm({
      direction: 'in',
      action: 'allow',
      protocol: preset.protocol,
      port: preset.port,
      from: 'any',
      comment: preset.label,
    })
    setAddOpen(true)
  }

  const rules = status?.rules ?? []

  return (
    <div className="space-y-4">
      {/* Page header */}
      <div className="flex items-center justify-between gap-4 flex-wrap">
        <div>
          <h2 className="text-white text-xl font-bold">Firewall</h2>
          <p className="text-zinc-500 text-sm mt-0.5">UFW firewall management</p>
        </div>

        <div className="flex items-center gap-2">
          {statusQuery.isLoading ? (
            <Skeleton className="h-8 w-24 bg-zinc-900 rounded-lg" />
          ) : statusQuery.isError || status?.state === 'unavailable' ? (
            <div className="flex items-center gap-1.5 rounded-lg border border-red-500/20 bg-red-500/10 px-3 py-1.5 text-xs font-medium text-red-300">
              <AlertTriangle className="size-3.5" />UFW unavailable
            </div>
          ) : status?.state === 'ufw-missing' ? (
            <div className="flex items-center gap-1.5 rounded-lg border border-amber-500/20 bg-amber-500/10 px-3 py-1.5 text-xs font-medium text-amber-300">
              <AlertTriangle className="size-3.5" />UFW not installed
            </div>
          ) : (
            <div
              className={cn(
                'flex items-center gap-1.5 px-3 py-1.5 rounded-lg border text-xs font-medium',
                status?.active
                  ? 'bg-green-500/10 border-green-500/20 text-green-400'
                  : 'bg-red-500/10 border-red-500/20 text-red-400',
              )}
            >
              {status?.active ? (
                <ShieldCheck className="w-3.5 h-3.5" />
              ) : (
                <ShieldOff className="w-3.5 h-3.5" />
              )}
              UFW {status?.active ? 'Active' : 'Inactive'}
            </div>
          )}

          <Button
            variant="outline"
            size="sm"
            onClick={() => toggleMutation.mutate()}
            disabled={toggleMutation.isPending || statusQuery.isLoading || managementUnavailable}
            className={cn(
              'border-zinc-700 text-zinc-300 hover:text-white transition-colors',
              status?.active
                ? 'hover:bg-red-500/10 hover:border-red-500/30 hover:text-red-400'
                : 'hover:bg-green-500/10 hover:border-green-500/30 hover:text-green-400',
            )}
          >
            {toggleMutation.isPending ? (
              <Loader2 className="w-3.5 h-3.5 mr-1.5 animate-spin" />
            ) : (
              <Shield className="w-3.5 h-3.5 mr-1.5" />
            )}
            {managementUnavailable ? 'Manage' : status?.active ? 'Disable' : 'Enable'} UFW
          </Button>

          <Button
            size="sm"
            onClick={() => { setPresetForm(undefined); setAddOpen(true) }}
            disabled={statusQuery.isLoading || managementUnavailable}
            className="bg-blue-600 hover:bg-blue-500 text-white"
          >
            <Plus className="w-3.5 h-3.5 mr-1.5" />
            Add Rule
          </Button>
        </div>
      </div>

      {statusQuery.isError && (
        <DependencyRemediation
          title="Firewall API is unavailable"
          summary="Heyserver could not observe firewall readiness. Rules and mutating controls remain paused until the API responds."
          error={statusQuery.error.message}
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
          steps={[
            'Run the packaged Heyserver doctor and inspect Heyserver service logs.',
            <>Verify <code>ufw status verbose</code> using the Heyserver service identity.</>,
            'Retry detection after API and local command access are ready.',
          ]}
        />
      )}

      {!statusQuery.isError && status?.state === 'ufw-missing' && (
        <DependencyRemediation
          title="UFW is not installed"
          summary={status.backend === 'iptables'
            ? 'Legacy iptables rules remain visible below for observation only. Heyserver firewall mutations require UFW.'
            : 'Heyserver firewall mutations require UFW, and no legacy iptables inventory is currently observable.'}
          state="not-configured"
          error={status.error}
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
          steps={[
            <>Install <code>ufw</code> from the supported Ubuntu repositories.</>,
            <>Verify <code>ufw version</code> and <code>ufw status verbose</code>; preserve an SSH allow rule before enabling it.</>,
            'Retry detection. Installation and activation remain explicit operator actions.',
          ]}
        />
      )}

      {!statusQuery.isError && status?.state === 'unavailable' && (
        <DependencyRemediation
          title="UFW status is unavailable"
          summary="UFW appears present, but Heyserver could not inspect it. Rules and mutating controls remain paused."
          error={status.error}
          retry={() => { void statusQuery.refetch() }}
          retrying={statusQuery.isFetching}
          steps={[
            'Run the packaged Heyserver doctor and inspect Heyserver service logs.',
            <>Run <code>ufw status verbose</code> with the Heyserver service identity and verify command permissions.</>,
            'Retry detection after the reported local error is corrected.',
          ]}
        />
      )}

      {/* Default policies + Quick presets */}
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <Card className="bg-zinc-900 border-zinc-800">
          <CardContent className="p-4">
            <p className="text-zinc-500 text-xs font-medium mb-3">Default Policies</p>
            {statusQuery.isLoading ? (
              <div className="space-y-2">
                <Skeleton className="h-5 w-40 bg-zinc-800" />
                <Skeleton className="h-5 w-40 bg-zinc-800" />
              </div>
            ) : statusQuery.isError || managementUnavailable ? (
              <p className="text-sm text-zinc-500">Policies unavailable</p>
            ) : (
              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <span className="text-zinc-400 text-sm">Incoming</span>
                  <ActionBadge action={status?.defaultIncoming ?? 'DENY'} />
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-zinc-400 text-sm">Outgoing</span>
                  <ActionBadge action={status?.defaultOutgoing ?? 'ALLOW'} />
                </div>
              </div>
            )}
          </CardContent>
        </Card>

        <Card className="bg-zinc-900 border-zinc-800">
          <CardContent className="p-4">
            <p className="text-zinc-500 text-xs font-medium mb-3">Quick Presets</p>
            <div className="flex flex-wrap gap-2">
              {PRESETS.map((preset) => (
                <button
                  key={preset.label}
                  onClick={() => openPreset(preset)}
                  disabled={statusQuery.isLoading || managementUnavailable}
                  className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-zinc-800 hover:bg-zinc-700 border border-zinc-700 hover:border-zinc-600 text-zinc-300 hover:text-white text-xs font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-40"
                >
                  <Plus className="w-3 h-3" />
                  {preset.label}
                  <span className="text-zinc-500 font-mono">{preset.port}</span>
                </button>
              ))}
            </div>
          </CardContent>
        </Card>
      </div>

      {/* Rules table */}
      <Card className="bg-zinc-900 border-zinc-800">
        <CardContent className="p-0">
          {statusQuery.isLoading ? (
            <div className="p-5 space-y-3">
              {Array.from({ length: 5 }).map((_, i) => (
                <Skeleton key={i} className="h-10 w-full bg-zinc-800" />
              ))}
            </div>
          ) : statusQuery.isError ? (
            <div className="flex flex-col items-center justify-center py-16 text-zinc-600">
              <AlertTriangle className="mb-2 size-6 text-red-400/70" />
              <p className="text-sm">Firewall rules unavailable</p>
            </div>
          ) : managementUnavailable && status?.backend !== 'iptables' ? (
            <div className="flex flex-col items-center justify-center py-16 text-zinc-600">
              <AlertTriangle className="mb-2 size-6 text-amber-400/70" />
              <p className="text-sm">Firewall rules unavailable until UFW detection succeeds</p>
            </div>
          ) : rules.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-20 text-zinc-600">
              <Lock className="w-8 h-8 mb-2 opacity-40" />
              <p className="text-sm">{status?.backend === 'iptables' ? 'No legacy iptables rules observed' : 'No rules configured'}</p>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <Table className="min-w-[480px]">
                <TableHeader>
                  <TableRow className="border-zinc-800 hover:bg-transparent">
                    <TableHead className="text-zinc-500 font-medium text-xs w-12">#</TableHead>
                    <TableHead className="text-zinc-500 font-medium text-xs">To</TableHead>
                    <TableHead className="text-zinc-500 font-medium text-xs w-28">Action</TableHead>
                    <TableHead className="text-zinc-500 font-medium text-xs">From</TableHead>
                    <TableHead className="text-zinc-500 font-medium text-xs w-20">Proto</TableHead>
                    <TableHead className="text-zinc-500 font-medium text-xs text-right w-16">Del</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rules.map((rule) => (
                    <TableRow
                      key={rule.number}
                      className="border-zinc-800 hover:bg-zinc-800/50 transition-colors"
                    >
                      <TableCell className="font-mono text-xs text-zinc-500 py-3">
                        {rule.number}
                      </TableCell>
                      <TableCell className="font-mono text-xs text-zinc-300 py-3">
                        {rule.to}
                        {rule.comment && (
                          <span className="ml-2 text-zinc-600 font-sans">({rule.comment})</span>
                        )}
                      </TableCell>
                      <TableCell className="py-3">
                        <ActionBadge action={rule.action} />
                      </TableCell>
                      <TableCell className="font-mono text-xs text-zinc-300 py-3">
                        {rule.from}
                      </TableCell>
                      <TableCell className="font-mono text-xs text-zinc-500 py-3">
                        {rule.protocol ?? '—'}
                      </TableCell>
                      <TableCell className="py-3 text-right">
                        <button
                          onClick={() => setDeleteRule(rule.number)}
                          disabled={managementUnavailable}
                          className="text-zinc-500 hover:text-red-400 p-1 rounded transition-colors disabled:cursor-not-allowed disabled:opacity-35 disabled:hover:text-zinc-500"
                          title="Delete rule"
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                        </button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>

      {/* Dialogs */}
      <AddRuleDialog
        open={addOpen}
        onClose={() => { setAddOpen(false); setPresetForm(undefined) }}
        onSave={(form) => addRuleMutation.mutate(form)}
        isPending={addRuleMutation.isPending}
        initialPreset={presetForm}
      />

      <ConfirmDeleteDialog
        open={deleteRule !== null}
        ruleNumber={deleteRule}
        onConfirm={() => deleteRule !== null && deleteRuleMutation.mutate(deleteRule)}
        onClose={() => setDeleteRule(null)}
        isPending={deleteRuleMutation.isPending}
      />
    </div>
  )
}
