import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Users as UsersIcon,
  Plus,
  Pencil,
  Trash2,
  KeyRound,
  Loader2,
  Eye,
  EyeOff,
  AlertTriangle,
  RefreshCw,
} from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
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
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { api } from '@/lib/api'
import { useCurrentUser } from '@/hooks/useAuth'
import { toast } from 'sonner'
import type { AuthUser } from '@/lib/types'

// ─── Types ─────────────────────────────────────────────────────────────────────

interface UserFormData {
  name: string
  email: string
  password: string
  role: 'admin' | 'manager' | 'viewer'
}

interface EditFormData {
  name: string
  email: string
  role: 'admin' | 'manager' | 'viewer'
}

interface PasswordFormData {
  password: string
  confirmPassword: string
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

function roleBadge(role: AuthUser['role']) {
  const variants: Record<AuthUser['role'], { label: string; className: string }> = {
    admin: { label: 'Admin', className: 'bg-blue-500/10 text-blue-400 border-blue-500/20' },
    manager: { label: 'Manager', className: 'bg-amber-500/10 text-amber-400 border-amber-500/20' },
    viewer: { label: 'Viewer', className: 'bg-zinc-500/10 text-zinc-400 border-zinc-500/20' },
  }
  const v = variants[role]
  return (
    <span
      className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium border ${v.className}`}
    >
      {v.label}
    </span>
  )
}

function formatDate(iso: string): string {
  return new Date(iso).toLocaleDateString('en-GB', {
    day: '2-digit',
    month: 'short',
    year: 'numeric',
  })
}

function errorStatus(error: unknown): number | undefined {
  if (!error || typeof error !== 'object' || !('status' in error)) return undefined
  return typeof error.status === 'number' ? error.status : undefined
}

// ─── Add User Dialog ──────────────────────────────────────────────────────────

interface AddUserDialogProps {
  open: boolean
  onClose: () => void
}

function AddUserDialog({ open, onClose }: AddUserDialogProps) {
  const queryClient = useQueryClient()
  const [form, setForm] = useState<UserFormData>({
    name: '',
    email: '',
    password: '',
    role: 'viewer',
  })
  const [showPassword, setShowPassword] = useState(false)

  const mutation = useMutation({
    mutationFn: (data: UserFormData) => api.post<AuthUser>('/users', data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['users'] })
      toast.success('User created successfully')
      onClose()
      setForm({ name: '', email: '', password: '', role: 'viewer' })
    },
    onError: (err: Error) => {
      toast.error(`Failed to create user: ${err.message}`)
    },
  })

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    if (!form.name || !form.email || !form.password) {
      toast.error('All fields are required')
      return
    }
    mutation.mutate(form)
  }

  return (
    <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-white">Add User</DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label className="text-zinc-300">Name</Label>
            <Input
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              placeholder="John Doe"
              className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500"
            />
          </div>
          <div className="space-y-2">
            <Label className="text-zinc-300">Email</Label>
            <Input
              type="email"
              value={form.email}
              onChange={(e) => setForm({ ...form, email: e.target.value })}
              placeholder="john@example.com"
              className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500"
            />
          </div>
          <div className="space-y-2">
            <Label className="text-zinc-300">Password</Label>
            <div className="relative">
              <Input
                type={showPassword ? 'text' : 'password'}
                value={form.password}
                onChange={(e) => setForm({ ...form, password: e.target.value })}
                placeholder="Min. 8 characters"
                className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500 pr-10"
              />
              <button
                type="button"
                onClick={() => setShowPassword(!showPassword)}
                className="absolute right-3 top-1/2 -translate-y-1/2 text-zinc-400 hover:text-white"
              >
                {showPassword ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
              </button>
            </div>
          </div>
          <div className="space-y-2">
            <Label className="text-zinc-300">Role</Label>
            <select
              value={form.role}
              onChange={(e) => setForm({ ...form, role: e.target.value as UserFormData['role'] })}
              className="w-full bg-zinc-800 border border-zinc-700 text-white rounded-md px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            >
              <option value="viewer">Viewer</option>
              <option value="manager">Manager</option>
              <option value="admin">Admin</option>
            </select>
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              onClick={onClose}
              className="text-zinc-400 hover:text-white"
            >
              Cancel
            </Button>
            <Button
              type="submit"
              disabled={mutation.isPending}
              className="bg-blue-600 hover:bg-blue-700"
            >
              {mutation.isPending && <Loader2 className="w-4 h-4 mr-2 animate-spin" />}
              Create User
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ─── Edit User Dialog ─────────────────────────────────────────────────────────

interface EditUserDialogProps {
  user: AuthUser | null
  onClose: () => void
}

function EditUserDialog({ user, onClose }: EditUserDialogProps) {
  const queryClient = useQueryClient()
  const [form, setForm] = useState<EditFormData>({
    name: user?.name ?? '',
    email: user?.email ?? '',
    role: user?.role ?? 'viewer',
  })

  const mutation = useMutation({
    mutationFn: (data: EditFormData) => api.put<AuthUser>(`/users/${user!.id}`, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['users'] })
      toast.success('User updated successfully')
      onClose()
    },
    onError: (err: Error) => {
      toast.error(`Failed to update user: ${err.message}`)
    },
  })

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    if (!form.name || !form.email) {
      toast.error('Name and email are required')
      return
    }
    mutation.mutate(form)
  }

  if (!user) return null

  return (
    <Dialog open={!!user} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-white">Edit User</DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label className="text-zinc-300">Name</Label>
            <Input
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              className="bg-zinc-800 border-zinc-700 text-white"
            />
          </div>
          <div className="space-y-2">
            <Label className="text-zinc-300">Email</Label>
            <Input
              type="email"
              value={form.email}
              onChange={(e) => setForm({ ...form, email: e.target.value })}
              className="bg-zinc-800 border-zinc-700 text-white"
            />
          </div>
          <div className="space-y-2">
            <Label className="text-zinc-300">Role</Label>
            <select
              value={form.role}
              onChange={(e) => setForm({ ...form, role: e.target.value as EditFormData['role'] })}
              className="w-full bg-zinc-800 border border-zinc-700 text-white rounded-md px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            >
              <option value="viewer">Viewer</option>
              <option value="manager">Manager</option>
              <option value="admin">Admin</option>
            </select>
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              onClick={onClose}
              className="text-zinc-400 hover:text-white"
            >
              Cancel
            </Button>
            <Button
              type="submit"
              disabled={mutation.isPending}
              className="bg-blue-600 hover:bg-blue-700"
            >
              {mutation.isPending && <Loader2 className="w-4 h-4 mr-2 animate-spin" />}
              Save Changes
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ─── Change Password Dialog ───────────────────────────────────────────────────

interface ChangePasswordDialogProps {
  user: AuthUser | null
  onClose: () => void
}

function ChangePasswordDialog({ user, onClose }: ChangePasswordDialogProps) {
  const [form, setForm] = useState<PasswordFormData>({ password: '', confirmPassword: '' })
  const [showPassword, setShowPassword] = useState(false)

  const mutation = useMutation({
    mutationFn: (data: { password: string }) =>
      api.put(`/users/${user!.id}`, { password: data.password }),
    onSuccess: () => {
      toast.success('Password changed successfully')
      onClose()
      setForm({ password: '', confirmPassword: '' })
    },
    onError: (err: Error) => {
      toast.error(`Failed to change password: ${err.message}`)
    },
  })

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    if (!form.password || form.password.length < 8) {
      toast.error('Password must be at least 8 characters')
      return
    }
    if (form.password !== form.confirmPassword) {
      toast.error('Passwords do not match')
      return
    }
    mutation.mutate({ password: form.password })
  }

  if (!user) return null

  return (
    <Dialog open={!!user} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-white">Change Password — {user.name}</DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <Label className="text-zinc-300">New Password</Label>
            <div className="relative">
              <Input
                type={showPassword ? 'text' : 'password'}
                value={form.password}
                onChange={(e) => setForm({ ...form, password: e.target.value })}
                placeholder="Min. 8 characters"
                className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500 pr-10"
              />
              <button
                type="button"
                onClick={() => setShowPassword(!showPassword)}
                className="absolute right-3 top-1/2 -translate-y-1/2 text-zinc-400 hover:text-white"
              >
                {showPassword ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
              </button>
            </div>
          </div>
          <div className="space-y-2">
            <Label className="text-zinc-300">Confirm Password</Label>
            <Input
              type="password"
              value={form.confirmPassword}
              onChange={(e) => setForm({ ...form, confirmPassword: e.target.value })}
              placeholder="Repeat password"
              className="bg-zinc-800 border-zinc-700 text-white placeholder-zinc-500"
            />
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              onClick={onClose}
              className="text-zinc-400 hover:text-white"
            >
              Cancel
            </Button>
            <Button
              type="submit"
              disabled={mutation.isPending}
              className="bg-blue-600 hover:bg-blue-700"
            >
              {mutation.isPending && <Loader2 className="w-4 h-4 mr-2 animate-spin" />}
              Change Password
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ─── Delete Confirm Dialog ────────────────────────────────────────────────────

interface DeleteConfirmDialogProps {
  user: AuthUser | null
  onClose: () => void
}

function DeleteConfirmDialog({ user, onClose }: DeleteConfirmDialogProps) {
  const queryClient = useQueryClient()

  const mutation = useMutation({
    mutationFn: () => api.delete(`/users/${user!.id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['users'] })
      toast.success('User deleted')
      onClose()
    },
    onError: (err: Error) => {
      toast.error(`Failed to delete user: ${err.message}`)
    },
  })

  if (!user) return null

  return (
    <Dialog open={!!user} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="bg-zinc-900 border-zinc-800 text-white max-w-md overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="text-white">Delete User</DialogTitle>
        </DialogHeader>
        <p className="text-zinc-400 text-sm">
          Are you sure you want to delete{' '}
          <span className="text-white font-medium">{user.name}</span>? This action cannot be
          undone.
        </p>
        <DialogFooter>
          <Button
            type="button"
            variant="ghost"
            onClick={onClose}
            className="text-zinc-400 hover:text-white"
          >
            Cancel
          </Button>
          <Button
            onClick={() => mutation.mutate()}
            disabled={mutation.isPending}
            className="bg-red-600 hover:bg-red-700"
          >
            {mutation.isPending && <Loader2 className="w-4 h-4 mr-2 animate-spin" />}
            Delete
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Main Page ────────────────────────────────────────────────────────────────

export default function UsersPage() {
  const [addOpen, setAddOpen] = useState(false)
  const [editUser, setEditUser] = useState<AuthUser | null>(null)
  const [deleteUser, setDeleteUser] = useState<AuthUser | null>(null)
  const [passwordUser, setPasswordUser] = useState<AuthUser | null>(null)

  const currentUserQuery = useCurrentUser()
  const currentUser = currentUserQuery.data
  const currentUserId = currentUser?.id ?? null
  const identityLoading = !currentUser && currentUserQuery.isLoading
  const identityError = currentUserQuery.isError ? currentUserQuery.error : null
  const canManageUsers = !identityError && currentUser?.role === 'admin'
  const permissionDenied = !identityLoading && !identityError && !!currentUser && !canManageUsers

  const { data: usersResp, isLoading, isError, error, refetch, isFetching } = useQuery<{ data: AuthUser[] }>({
    queryKey: ['users'],
    queryFn: () => api.get<{ data: AuthUser[] }>('/users'),
    enabled: canManageUsers,
  })
  const users = usersResp?.data ?? []
  const apiPermissionDenied = canManageUsers && isError && errorStatus(error) === 403
  const controlsAvailable = canManageUsers && !apiPermissionDenied

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <div className="w-8 h-8 bg-blue-600/10 rounded-lg flex items-center justify-center">
            <UsersIcon className="w-4 h-4 text-blue-400" />
          </div>
          <div>
            <h2 className="text-white font-semibold">Users</h2>
            <p className="text-zinc-500 text-xs">Manage panel access and roles</p>
          </div>
        </div>
        {controlsAvailable && (
          <Button
            onClick={() => setAddOpen(true)}
            disabled={isLoading || isError}
            className="bg-blue-600 hover:bg-blue-700 text-white text-sm"
          >
            <Plus className="w-4 h-4 mr-2" />
            Add User
          </Button>
        )}
      </div>

      {/* Table */}
      <Card className="bg-zinc-900 border-zinc-800">
        <CardHeader className="pb-3">
          <CardTitle className="text-white text-sm font-medium flex items-center gap-2">
            <UsersIcon className="w-4 h-4 text-zinc-400" />
            {identityLoading ? (
              'Checking user management permission'
            ) : identityError ? (
              'User management identity unavailable'
            ) : permissionDenied || apiPermissionDenied ? (
              'User management access denied'
            ) : isLoading ? (
              <Skeleton className="h-4 w-20 bg-zinc-800" />
            ) : isError ? (
              'Users unavailable'
            ) : (
              `${users.length ?? 0} user${(users.length ?? 0) !== 1 ? 's' : ''}`
            )}
          </CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          <Table>
            <TableHeader>
              <TableRow className="border-zinc-800 hover:bg-transparent">
                <TableHead className="text-zinc-400 font-medium">Name</TableHead>
                <TableHead className="text-zinc-400 font-medium">Email</TableHead>
                <TableHead className="text-zinc-400 font-medium">Role</TableHead>
                <TableHead className="text-zinc-400 font-medium">Created</TableHead>
                <TableHead className="text-zinc-400 font-medium text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {identityLoading ? (
                <TableRow className="border-zinc-800">
                  <TableCell colSpan={5} className="py-10 text-center">
                    <Loader2 className="mx-auto size-5 animate-spin text-amber-400" />
                    <p className="mt-2 text-sm text-zinc-300">Checking your account permission…</p>
                    <p className="mt-1 text-xs text-zinc-600">User management remains locked until your identity is verified.</p>
                  </TableCell>
                </TableRow>
              ) : identityError ? (
                <TableRow className="border-zinc-800">
                  <TableCell colSpan={5} className="py-10 text-center">
                    <AlertTriangle className="mx-auto size-5 text-red-400" />
                    <p className="mt-2 text-sm text-red-300">User management identity is unavailable.</p>
                    <p className="mt-1 text-xs text-zinc-600">{identityError.message || 'Account verification failed'}</p>
                    <Button type="button" variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={() => { void currentUserQuery.refetch() }} disabled={currentUserQuery.isFetching}>
                      <RefreshCw className={`mr-2 size-3.5 ${currentUserQuery.isFetching ? 'animate-spin' : ''}`} />Retry permission
                    </Button>
                  </TableCell>
                </TableRow>
              ) : permissionDenied ? (
                <TableRow className="border-zinc-800">
                  <TableCell colSpan={5} className="py-10 text-center">
                    <AlertTriangle className="mx-auto size-5 text-amber-400" />
                    <p className="mt-2 text-sm text-amber-300">User management access denied.</p>
                    <p className="mt-1 text-xs text-zinc-600">The <code>admin</code> role is required. Your <code>{currentUser.role}</code> account was not granted mutation controls.</p>
                  </TableCell>
                </TableRow>
              ) : isLoading ? (
                Array.from({ length: 3 }).map((_, i) => (
                  <TableRow key={i} className="border-zinc-800">
                    <TableCell><Skeleton className="h-4 w-32 bg-zinc-800" /></TableCell>
                    <TableCell><Skeleton className="h-4 w-40 bg-zinc-800" /></TableCell>
                    <TableCell><Skeleton className="h-5 w-16 bg-zinc-800 rounded" /></TableCell>
                    <TableCell><Skeleton className="h-4 w-24 bg-zinc-800" /></TableCell>
                    <TableCell><Skeleton className="h-8 w-24 bg-zinc-800 ml-auto" /></TableCell>
                  </TableRow>
                ))
              ) : apiPermissionDenied ? (
                <TableRow className="border-zinc-800">
                  <TableCell colSpan={5} className="py-10 text-center">
                    <AlertTriangle className="mx-auto size-5 text-amber-400" />
                    <p className="mt-2 text-sm text-amber-300">User management access denied.</p>
                    <p className="mt-1 text-xs text-zinc-600">The server rejected this account's user-management permission. No mutation controls were enabled.</p>
                  </TableCell>
                </TableRow>
              ) : isError ? (
                <TableRow className="border-zinc-800">
                  <TableCell colSpan={5} className="py-10 text-center">
                    <AlertTriangle className="mx-auto size-5 text-red-400" />
                    <p className="mt-2 text-sm text-red-300">Users could not be loaded.</p>
                    <p className="mt-1 text-xs text-zinc-600">{error.message}</p>
                    <Button type="button" variant="outline" size="sm" className="mt-4 border-red-500/30 text-red-200" onClick={() => { void refetch() }} disabled={isFetching}>
                      <RefreshCw className={`mr-2 size-3.5 ${isFetching ? 'animate-spin' : ''}`} />Retry
                    </Button>
                  </TableCell>
                </TableRow>
              ) : users.length === 0 ? (
                <TableRow className="border-zinc-800">
                  <TableCell colSpan={5} className="text-center text-zinc-500 py-8">
                    No users found
                  </TableCell>
                </TableRow>
              ) : (
                users.map((user) => (
                  <TableRow key={user.id} className="border-zinc-800 hover:bg-zinc-800/40">
                    <TableCell className="text-white font-medium">{user.name}</TableCell>
                    <TableCell className="text-zinc-400">{user.email}</TableCell>
                    <TableCell>{roleBadge(user.role)}</TableCell>
                    <TableCell className="text-zinc-500 text-sm">
                      {formatDate(user.createdAt)}
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex items-center justify-end gap-1">
                        <Button
                          variant="ghost"
                          size="icon"
                          title="Edit user"
                          onClick={() => setEditUser(user)}
                          className="h-7 w-7 text-zinc-400 hover:text-white hover:bg-zinc-700"
                        >
                          <Pencil className="w-3.5 h-3.5" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          title="Change password"
                          onClick={() => setPasswordUser(user)}
                          className="h-7 w-7 text-zinc-400 hover:text-blue-400 hover:bg-blue-400/10"
                        >
                          <KeyRound className="w-3.5 h-3.5" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          title={
                            user.id === currentUserId
                              ? 'Cannot delete yourself'
                              : 'Delete user'
                          }
                          onClick={() => {
                            if (user.id === currentUserId) {
                              toast.error('You cannot delete your own account')
                              return
                            }
                            setDeleteUser(user)
                          }}
                          disabled={user.id === currentUserId}
                          className="h-7 w-7 text-zinc-400 hover:text-red-400 hover:bg-red-400/10 disabled:opacity-30 disabled:cursor-not-allowed"
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      {/* Dialogs */}
      {controlsAvailable && (
        <>
          <AddUserDialog key={`add-user-${addOpen}`} open={addOpen} onClose={() => setAddOpen(false)} />
          <EditUserDialog key={editUser?.id ?? 'edit-user-closed'} user={editUser} onClose={() => setEditUser(null)} />
          <ChangePasswordDialog key={passwordUser?.id ?? 'password-user-closed'} user={passwordUser} onClose={() => setPasswordUser(null)} />
          <DeleteConfirmDialog key={deleteUser?.id ?? 'delete-user-closed'} user={deleteUser} onClose={() => setDeleteUser(null)} />
        </>
      )}
    </div>
  )
}
