import { useState } from 'react'
import { Loader2, Plus } from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import type { NginxCreateRequest } from '@/lib/types'

type SiteType = NginxCreateRequest['type']

interface NginxCreateDialogProps {
  open: boolean
  pending: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (request: NginxCreateRequest) => void
}

interface FormState {
  domain: string
  type: SiteType
  docRoot: string
  phpVersion: string
  phpPool: string
  proxyPass: string
  redirectTo: string
  useSSL: boolean
  certPath: string
  keyPath: string
}

const emptyForm = (): FormState => ({
  domain: '',
  type: 'static',
  docRoot: '',
  phpVersion: '',
  phpPool: '',
  proxyPass: '',
  redirectTo: '',
  useSSL: false,
  certPath: '',
  keyPath: '',
})

function optional(value: string) {
  const trimmed = value.trim()
  return trimmed || undefined
}

export function NginxCreateDialog({ open, pending, onOpenChange, onSubmit }: NginxCreateDialogProps) {
  const [form, setForm] = useState<FormState>(emptyForm)
  const [error, setError] = useState('')

  function field<K extends keyof FormState>(key: K, value: FormState[K]) {
    setForm((current) => ({ ...current, [key]: value }))
    setError('')
  }

  function handleSubmit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const domain = form.domain.trim()
    if (!domain) {
      setError('Domain is required.')
      return
    }
    if (form.type === 'proxy' && !form.proxyPass.trim()) {
      setError('Upstream URL is required for a proxy site.')
      return
    }
    if (form.type === 'redirect' && !form.redirectTo.trim()) {
      setError('Redirect target is required for a redirect site.')
      return
    }
    if (Boolean(form.certPath.trim()) !== Boolean(form.keyPath.trim())) {
      setError('Custom certificate and private-key paths must be supplied together.')
      return
    }

    const request: NginxCreateRequest = {
      domain,
      type: form.type,
      useSSL: form.useSSL,
    }
    if (form.type === 'static' || form.type === 'php') {
      const docRoot = optional(form.docRoot)
      if (docRoot) request.docRoot = docRoot
    }
    if (form.type === 'php') {
      const phpVersion = optional(form.phpVersion)
      const phpPool = optional(form.phpPool)
      if (phpVersion) request.phpVersion = phpVersion
      if (phpPool) request.phpPool = phpPool
    }
    if (form.type === 'proxy') {
      request.proxyPass = form.proxyPass.trim()
    }
    if (form.type === 'redirect') {
      request.redirectTo = form.redirectTo.trim()
    }
    if (form.useSSL) {
      const certPath = optional(form.certPath)
      const keyPath = optional(form.keyPath)
      if (certPath) request.certPath = certPath
      if (keyPath) request.keyPath = keyPath
    }
    onSubmit(request)
  }

  const hasDocumentRoot = form.type === 'static' || form.type === 'php'

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[88vh] max-w-2xl overflow-y-auto border-zinc-800 bg-zinc-900 text-white">
        <DialogHeader>
          <DialogTitle className="text-white">Create Nginx Site</DialogTitle>
          <DialogDescription className="text-zinc-500">
            Generate a provider-neutral virtual host and validate the complete Nginx configuration before keeping it.
          </DialogDescription>
        </DialogHeader>

        <form className="space-y-5" onSubmit={handleSubmit}>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-1.5 sm:col-span-2">
              <Label htmlFor="nginx-create-domain" className="text-zinc-300">Domain</Label>
              <Input
                id="nginx-create-domain"
                value={form.domain}
                onChange={(event) => field('domain', event.target.value)}
                placeholder="example.com"
                autoComplete="off"
                disabled={pending}
                className="border-zinc-700 bg-zinc-800 text-white placeholder:text-zinc-600"
              />
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="nginx-create-type" className="text-zinc-300">Site type</Label>
              <select
                id="nginx-create-type"
                value={form.type}
                onChange={(event) => field('type', event.target.value as SiteType)}
                disabled={pending}
                className="h-8 w-full rounded-lg border border-zinc-700 bg-zinc-800 px-2.5 text-sm text-white outline-none focus:border-blue-500 disabled:opacity-50"
              >
                <option value="static">Static files</option>
                <option value="php">PHP-FPM</option>
                <option value="proxy">Reverse proxy</option>
                <option value="redirect">Redirect</option>
              </select>
            </div>

            {hasDocumentRoot && (
              <div className="space-y-1.5">
                <Label htmlFor="nginx-create-root" className="text-zinc-300">Document root</Label>
                <Input
                  id="nginx-create-root"
                  value={form.docRoot}
                  onChange={(event) => field('docRoot', event.target.value)}
                  placeholder="Automatic installation root"
                  autoComplete="off"
                  disabled={pending}
                  className="border-zinc-700 bg-zinc-800 font-mono text-white placeholder:text-zinc-600"
                />
                <p className="text-[11px] leading-relaxed text-zinc-600">
                  Leave empty to use the configured Heyserver vhost root.
                </p>
              </div>
            )}

            {form.type === 'php' && (
              <>
                <div className="space-y-1.5">
                  <Label htmlFor="nginx-create-php-version" className="text-zinc-300">PHP version</Label>
                  <Input
                    id="nginx-create-php-version"
                    value={form.phpVersion}
                    onChange={(event) => field('phpVersion', event.target.value)}
                    placeholder="8.4"
                    autoComplete="off"
                    disabled={pending}
                    className="border-zinc-700 bg-zinc-800 font-mono text-white placeholder:text-zinc-600"
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="nginx-create-php-pool" className="text-zinc-300">PHP pool</Label>
                  <Input
                    id="nginx-create-php-pool"
                    value={form.phpPool}
                    onChange={(event) => field('phpPool', event.target.value)}
                    placeholder="Derived from domain"
                    autoComplete="off"
                    disabled={pending}
                    className="border-zinc-700 bg-zinc-800 font-mono text-white placeholder:text-zinc-600"
                  />
                </div>
              </>
            )}

            {form.type === 'proxy' && (
              <div className="space-y-1.5 sm:col-span-2">
                <Label htmlFor="nginx-create-proxy" className="text-zinc-300">Upstream URL</Label>
                <Input
                  id="nginx-create-proxy"
                  value={form.proxyPass}
                  onChange={(event) => field('proxyPass', event.target.value)}
                  placeholder="http://127.0.0.1:3000"
                  autoComplete="off"
                  disabled={pending}
                  className="border-zinc-700 bg-zinc-800 font-mono text-white placeholder:text-zinc-600"
                />
              </div>
            )}

            {form.type === 'redirect' && (
              <div className="space-y-1.5 sm:col-span-2">
                <Label htmlFor="nginx-create-redirect" className="text-zinc-300">Redirect target</Label>
                <Input
                  id="nginx-create-redirect"
                  value={form.redirectTo}
                  onChange={(event) => field('redirectTo', event.target.value)}
                  placeholder="https://www.example.com$request_uri"
                  autoComplete="off"
                  disabled={pending}
                  className="border-zinc-700 bg-zinc-800 font-mono text-white placeholder:text-zinc-600"
                />
              </div>
            )}
          </div>

          <div className="rounded-lg border border-zinc-800 bg-zinc-950/60 p-4">
            <label className="flex cursor-pointer items-start gap-3">
              <input
                type="checkbox"
                checked={form.useSSL}
                onChange={(event) => field('useSSL', event.target.checked)}
                disabled={pending}
                className="mt-0.5 size-4 accent-blue-600"
              />
              <span>
                <span className="block text-sm font-medium text-zinc-200">Generate HTTPS listeners</span>
                <span className="mt-0.5 block text-xs leading-relaxed text-zinc-500">
                  Enable only when the certificate already exists. HTTP remains the safe default.
                </span>
              </span>
            </label>

            {form.useSSL && (
              <div className="mt-4 grid gap-4 border-t border-zinc-800 pt-4 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <Label htmlFor="nginx-create-cert" className="text-zinc-300">Custom certificate path</Label>
                  <Input
                    id="nginx-create-cert"
                    value={form.certPath}
                    onChange={(event) => field('certPath', event.target.value)}
                    placeholder="Use managed certificate"
                    autoComplete="off"
                    disabled={pending}
                    className="border-zinc-700 bg-zinc-800 font-mono text-white placeholder:text-zinc-600"
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="nginx-create-key" className="text-zinc-300">Custom private-key path</Label>
                  <Input
                    id="nginx-create-key"
                    value={form.keyPath}
                    onChange={(event) => field('keyPath', event.target.value)}
                    placeholder="Use managed private key"
                    autoComplete="off"
                    disabled={pending}
                    className="border-zinc-700 bg-zinc-800 font-mono text-white placeholder:text-zinc-600"
                  />
                </div>
              </div>
            )}
          </div>

          <div className="rounded-lg border border-blue-500/20 bg-blue-500/5 px-3 py-2.5 text-xs leading-relaxed text-blue-200/80">
            Creation runs a complete <code className="font-mono">nginx -t</code>. The new site remains disabled and Nginx is not reloaded automatically.
          </div>

          {error && (
            <p role="alert" className="rounded-lg border border-red-500/20 bg-red-500/5 px-3 py-2 text-sm text-red-300">
              {error}
            </p>
          )}

          <DialogFooter className="mt-2">
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={pending} className="border-zinc-700">
              Cancel
            </Button>
            <Button type="submit" disabled={pending} className="bg-blue-600 text-white hover:bg-blue-500">
              {pending ? <Loader2 className="mr-2 size-4 animate-spin" /> : <Plus className="mr-2 size-4" />}
              Create Site
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
