/* eslint-disable react-refresh/only-export-components -- shared form values and validators are intentionally exported with the field component */
import type { ChangeEvent } from 'react'

export interface DeployProfile {
  allowDeployRead: boolean
  allowDeployActions: boolean
  allowDeployDomainRead: boolean
  allowDeployDomainActions: boolean
  deployPlansFile: string
  deployAcmeWebroot: string
  deployWriteRoots: string[]
}

export const emptyDeployProfile: DeployProfile = {
  allowDeployRead: false,
  allowDeployActions: false,
  allowDeployDomainRead: false,
  allowDeployDomainActions: false,
  deployPlansFile: '',
  deployAcmeWebroot: '',
  deployWriteRoots: [],
}

export type DeployPathField = 'deployPlansFile' | 'deployAcmeWebroot' | 'deployWriteRoots'

const maxDeployPathBytes = 4096
const maxDeployWriteRoots = 16
const safeDeployPathPattern = /^[A-Za-z0-9._/+:-]+$/

function hasUnsafePathCharacters(value: string): boolean {
  return value.includes('\r') || value.includes('\n') || value.includes('\u0000')
}

function byteLength(value: string): number {
  return new TextEncoder().encode(value).length
}

function isCleanAbsolutePath(value: string): boolean {
  if (!value.startsWith('/') || value === '/') return false
  const segments = value.split('/')
  return segments[0] === '' && segments.slice(1).every((segment) => segment !== '' && segment !== '.' && segment !== '..')
}

export function validateOptionalAbsoluteFilePath(raw: string): string {
  if (hasUnsafePathCharacters(raw)) return 'Path cannot contain CR, LF, or NUL.'
  if (raw === '') return ''
  if (byteLength(raw) > maxDeployPathBytes || !isCleanAbsolutePath(raw) || raw.endsWith('/') || !safeDeployPathPattern.test(raw)) {
    return 'Use a SAFE_PATH file (clean absolute, ASCII, max 4096 bytes; not / or trailing /).'
  }
  return ''
}

export function validateOptionalAbsoluteDirectory(raw: string): string {
  if (hasUnsafePathCharacters(raw)) return 'Path cannot contain CR, LF, or NUL.'
  if (raw === '') return ''
  if (byteLength(raw) > maxDeployPathBytes || !isCleanAbsolutePath(raw) || !safeDeployPathPattern.test(raw)) {
    return 'Use a SAFE_PATH directory (clean absolute ASCII, not /, max 4096 bytes).'
  }
  return ''
}

export function validateOptionalAbsoluteDirectories(roots: string[]): string {
  if (roots.length === 0) return ''
  if (roots.length > maxDeployWriteRoots) return 'Use at most 16 clean absolute directories.'
  const seen = new Set<string>()
  for (const root of roots) {
    if (hasUnsafePathCharacters(root)) return 'Paths cannot contain CR, LF, or NUL.'
    if (byteLength(root) > maxDeployPathBytes || !isCleanAbsolutePath(root) || !safeDeployPathPattern.test(root)) {
      return 'Each write root must be a SAFE_PATH directory (clean absolute ASCII, not /, max 4096 bytes).'
    }
    if (seen.has(root)) return 'Write roots must be unique.'
    seen.add(root)
  }
  return ''
}

export function deployProfilePathErrors(profile: DeployProfile): Record<DeployPathField, string> {
  return {
    deployPlansFile: validateOptionalAbsoluteFilePath(profile.deployPlansFile),
    deployAcmeWebroot: validateOptionalAbsoluteDirectory(profile.deployAcmeWebroot),
    deployWriteRoots: validateOptionalAbsoluteDirectories(profile.deployWriteRoots),
  }
}

export function isDeployProfileValid(profile: DeployProfile): boolean {
  const errors = deployProfilePathErrors(profile)
  return (!profile.allowDeployActions || profile.allowDeployRead)
    && (!profile.allowDeployDomainRead || profile.allowDeployRead)
    && (!profile.allowDeployDomainActions || profile.allowDeployDomainRead)
    && Object.values(errors).every((error) => error === '')
}

export function deployProfileEnvLines(profile: DeployProfile): string[] {
  return [
    `HSERVER_AGENT_ALLOW_DEPLOY_READ=${profile.allowDeployRead}`,
    `HSERVER_AGENT_ALLOW_DEPLOY_ACTIONS=${profile.allowDeployActions}`,
    `HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_READ=${profile.allowDeployDomainRead}`,
    `HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_ACTIONS=${profile.allowDeployDomainActions}`,
    `HSERVER_AGENT_DEPLOY_PLANS_FILE=${profile.deployPlansFile}`,
    `HSERVER_AGENT_DEPLOY_ACME_WEBROOT=${profile.deployAcmeWebroot}`,
    `HSERVER_AGENT_DEPLOY_WRITE_ROOTS=${profile.deployWriteRoots.join(',')}`,
  ]
}

export function deployProfileEnvFragment(profile: DeployProfile): string {
  return `${deployProfileEnvLines(profile).join('\n')}\n`
}

interface DeployProfileFieldsProps {
  value: DeployProfile
  onChange: (value: DeployProfile) => void
  disabled?: boolean
  title?: string
  description?: string
  idPrefix?: string
}

export function DeployProfileFields({
  value,
  onChange,
  disabled = false,
  title = 'Deploy controls',
  description = 'Optional local deploy-plan access. Permissions are closed by default; only the paths entered here are included in the generated config.',
  idPrefix = 'deploy-profile',
}: DeployProfileFieldsProps) {
  const setDeployRead = (enabled: boolean) => {
    onChange({
      ...value,
      allowDeployRead: enabled,
      allowDeployActions: enabled && value.allowDeployActions,
      allowDeployDomainRead: enabled && value.allowDeployDomainRead,
      allowDeployDomainActions: enabled && value.allowDeployDomainActions,
    })
  }

  const setDeployActions = (enabled: boolean) => {
    onChange({ ...value, allowDeployActions: enabled && value.allowDeployRead })
  }

  const setDeployDomainRead = (enabled: boolean) => {
    onChange({
      ...value,
      allowDeployDomainRead: enabled && value.allowDeployRead,
      allowDeployDomainActions: enabled && value.allowDeployDomainActions,
    })
  }

  const setDeployDomainActions = (enabled: boolean) => {
    onChange({ ...value, allowDeployDomainActions: enabled && value.allowDeployDomainRead })
  }

  const setDeployPath = (field: Exclude<DeployPathField, 'deployWriteRoots'>, event: ChangeEvent<HTMLInputElement>) => {
    onChange({ ...value, [field]: event.target.value })
  }

  const setDeployWriteRoots = (event: ChangeEvent<HTMLInputElement>) => {
    const raw = event.target.value
    onChange({
      ...value,
      deployWriteRoots: raw === '' ? [] : raw.split(',').map((root) => root.trim()),
    })
  }

  const deployPathErrors = deployProfilePathErrors(value)
  const fieldID = (field: string) => `${idPrefix}-${field}`

  return (
    <fieldset className="space-y-3 rounded-xl border border-zinc-800 bg-zinc-950/40 p-4" disabled={disabled}>
      <legend className="px-1 text-xs font-semibold text-zinc-300">{title}</legend>
      <p className="text-[11px] leading-relaxed text-zinc-500">{description}</p>
      <div className="grid gap-3 md:grid-cols-2">
        <label className="flex cursor-pointer items-start gap-2 text-xs text-zinc-300">
          <input type="checkbox" checked={value.allowDeployRead} onChange={(event) => setDeployRead(event.target.checked)} className="mt-0.5 size-4 rounded border-zinc-600 bg-zinc-800 text-violet-500" />
          <span><span className="font-medium">Read deploy targets</span><span className="mt-0.5 block text-[10px] text-zinc-500">Expose metadata from the agent's local deploy plans.</span></span>
        </label>
        <label className="flex cursor-pointer items-start gap-2 text-xs text-zinc-300">
          <input type="checkbox" checked={value.allowDeployActions} disabled={!value.allowDeployRead} onChange={(event) => setDeployActions(event.target.checked)} className="mt-0.5 size-4 rounded border-zinc-600 bg-zinc-800 text-violet-500 disabled:cursor-not-allowed disabled:opacity-40" />
          <span><span className="font-medium">Run deploy actions</span><span className="mt-0.5 block text-[10px] text-zinc-500">Requires deploy target read access.</span></span>
        </label>
        <label className="flex cursor-pointer items-start gap-2 text-xs text-zinc-300">
          <input type="checkbox" checked={value.allowDeployDomainRead} disabled={!value.allowDeployRead} onChange={(event) => setDeployDomainRead(event.target.checked)} className="mt-0.5 size-4 rounded border-zinc-600 bg-zinc-800 text-violet-500 disabled:cursor-not-allowed disabled:opacity-40" />
          <span><span className="font-medium">Read project domains</span><span className="mt-0.5 block text-[10px] text-zinc-500">Requires deploy target read access.</span></span>
        </label>
        <label className="flex cursor-pointer items-start gap-2 text-xs text-zinc-300">
          <input type="checkbox" checked={value.allowDeployDomainActions} disabled={!value.allowDeployDomainRead} onChange={(event) => setDeployDomainActions(event.target.checked)} className="mt-0.5 size-4 rounded border-zinc-600 bg-zinc-800 text-violet-500 disabled:cursor-not-allowed disabled:opacity-40" />
          <span><span className="font-medium">Manage project domains</span><span className="mt-0.5 block text-[10px] text-zinc-500">Requires project-domain read access.</span></span>
        </label>
      </div>
      <div className="grid gap-3 md:grid-cols-3">
        <label htmlFor={fieldID('plans-file')} className="space-y-1.5 text-xs text-zinc-400">
          <span className="font-medium text-zinc-300">Deploy plans file</span>
          <input id={fieldID('plans-file')} value={value.deployPlansFile} onChange={(event) => setDeployPath('deployPlansFile', event)} placeholder="Optional absolute path" autoComplete="off" aria-invalid={Boolean(deployPathErrors.deployPlansFile)} className="h-9 w-full rounded-lg border border-zinc-700 bg-zinc-900 px-3 font-mono text-[11px] text-zinc-100 outline-none focus:border-violet-500" />
          {deployPathErrors.deployPlansFile && <span className="block text-[10px] text-red-400">{deployPathErrors.deployPlansFile}</span>}
        </label>
        <label htmlFor={fieldID('acme-webroot')} className="space-y-1.5 text-xs text-zinc-400">
          <span className="font-medium text-zinc-300">ACME webroot</span>
          <input id={fieldID('acme-webroot')} value={value.deployAcmeWebroot} onChange={(event) => setDeployPath('deployAcmeWebroot', event)} placeholder="Optional absolute path" autoComplete="off" aria-invalid={Boolean(deployPathErrors.deployAcmeWebroot)} className="h-9 w-full rounded-lg border border-zinc-700 bg-zinc-900 px-3 font-mono text-[11px] text-zinc-100 outline-none focus:border-violet-500" />
          {deployPathErrors.deployAcmeWebroot && <span className="block text-[10px] text-red-400">{deployPathErrors.deployAcmeWebroot}</span>}
        </label>
        <label htmlFor={fieldID('write-roots')} className="space-y-1.5 text-xs text-zinc-400">
          <span className="font-medium text-zinc-300">Deploy write roots</span>
          <input id={fieldID('write-roots')} value={value.deployWriteRoots.join(',')} onChange={setDeployWriteRoots} placeholder="Optional comma-separated paths" autoComplete="off" aria-invalid={Boolean(deployPathErrors.deployWriteRoots)} className="h-9 w-full rounded-lg border border-zinc-700 bg-zinc-900 px-3 font-mono text-[11px] text-zinc-100 outline-none focus:border-violet-500" />
          {deployPathErrors.deployWriteRoots && <span className="block text-[10px] text-red-400">{deployPathErrors.deployWriteRoots}</span>}
        </label>
      </div>
    </fieldset>
  )
}
