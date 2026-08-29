'use client';

import { useState, useEffect, useTransition } from 'react';
import { useRouter } from 'next/navigation';
import {
  ArrowLeft, Plus, Trash2, RefreshCw, Shield, ShieldCheck, AlertTriangle, User,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from '@/components/ui/table';
import {
  Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, DialogTrigger,
} from '@/components/ui/dialog';
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent,
  AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger,
} from '@/components/ui/alert-dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { listUsers, createUser, dropUser } from '../actions';
import type { DbUser } from '@/lib/services/database';

type DbEngine = 'postgresql' | 'mariadb';

const defaultForm = { username: '', password: '', canCreateDb: false, isSuper: false, host: 'localhost' };

export default function DatabaseUsersPage() {
  const router = useRouter();
  const [activeTab, setActiveTab] = useState<DbEngine>('postgresql');
  const [users, setUsers] = useState<DbUser[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [, startTransition] = useTransition();

  const [createOpen, setCreateOpen] = useState(false);
  const [form, setForm] = useState(defaultForm);
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);

  const fetchUsers = (engine: DbEngine) => {
    setLoading(true);
    setError(null);
    startTransition(async () => {
      const res = await listUsers(engine);
      if (res.success && res.data) setUsers(res.data);
      else { setError(res.error ?? 'Failed to load users'); setUsers([]); }
      setLoading(false);
    });
  };

  useEffect(() => { fetchUsers(activeTab); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, [activeTab]);

  const handleCreate = async () => {
    if (!form.username.trim() || !form.password.trim()) return;
    setCreating(true);
    setCreateError(null);
    const res = await createUser(activeTab, { username: form.username.trim(), password: form.password, canCreateDb: form.canCreateDb, isSuper: form.isSuper, host: form.host || 'localhost' });
    if (res.success) { setCreateOpen(false); setForm(defaultForm); fetchUsers(activeTab); }
    else setCreateError(res.error ?? 'Failed to create user');
    setCreating(false);
  };

  const handleDrop = async (username: string) => {
    const res = await dropUser(activeTab, username);
    if (res.success) fetchUsers(activeTab);
    else setError(res.error ?? 'Failed to drop user');
  };

  const superCount = users.filter(u => u.isSuper).length;

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <Button variant="ghost" size="sm" onClick={() => router.push('/databases')}>
            <ArrowLeft className="size-4" />
          </Button>
          <div>
            <h1 className="text-2xl font-semibold tracking-tight">Database Users</h1>
            <p className="text-sm text-muted-foreground mt-1">Manage database users and roles</p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={() => fetchUsers(activeTab)} disabled={loading}>
            <RefreshCw className={`size-4 mr-1.5 ${loading ? 'animate-spin' : ''}`} />Refresh
          </Button>

          <Dialog open={createOpen} onOpenChange={setCreateOpen}>
            <DialogTrigger asChild>
              <Button size="sm"><Plus className="size-4 mr-1.5" />New User</Button>
            </DialogTrigger>
            <DialogContent>
              <DialogHeader><DialogTitle>Create Database User</DialogTitle></DialogHeader>
              <div className="flex flex-col gap-4 py-2">
                <div className="flex flex-col gap-1.5">
                  <Label>Username</Label>
                  <Input value={form.username} onChange={e => setForm(f => ({ ...f, username: e.target.value }))} placeholder="dbuser" autoFocus />
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label>Password</Label>
                  <Input type="password" value={form.password} onChange={e => setForm(f => ({ ...f, password: e.target.value }))} placeholder="Strong password" />
                </div>
                {activeTab === 'mariadb' && (
                  <div className="flex flex-col gap-1.5">
                    <Label>Host</Label>
                    <Input value={form.host} onChange={e => setForm(f => ({ ...f, host: e.target.value }))} placeholder="localhost" />
                  </div>
                )}
                {activeTab === 'postgresql' && (
                  <div className="flex flex-col gap-3">
                    <label className="flex items-center gap-2 cursor-pointer text-sm">
                      <input type="checkbox" checked={form.canCreateDb} onChange={e => setForm(f => ({ ...f, canCreateDb: e.target.checked }))} className="rounded" />
                      Can create databases
                    </label>
                    <label className="flex items-center gap-2 cursor-pointer text-sm text-destructive">
                      <input type="checkbox" checked={form.isSuper} onChange={e => setForm(f => ({ ...f, isSuper: e.target.checked }))} className="rounded" />
                      Superuser (dangerous)
                    </label>
                  </div>
                )}
                {createError && <p className="text-sm text-destructive">{createError}</p>}
              </div>
              <DialogFooter>
                <Button variant="outline" onClick={() => setCreateOpen(false)}>Cancel</Button>
                <Button onClick={handleCreate} disabled={creating || !form.username.trim() || !form.password.trim()}>
                  {creating ? 'Creating...' : 'Create User'}
                </Button>
              </DialogFooter>
            </DialogContent>
          </Dialog>
        </div>
      </div>

      {/* Stats */}
      <div className="grid grid-cols-3 gap-4">
        <Card><CardContent className="pt-4"><div className="text-2xl font-bold">{users.length}</div><div className="text-xs text-muted-foreground mt-0.5">Total Users</div></CardContent></Card>
        <Card><CardContent className="pt-4"><div className="text-2xl font-bold text-amber-500">{superCount}</div><div className="text-xs text-muted-foreground mt-0.5">Superusers</div></CardContent></Card>
        <Card><CardContent className="pt-4"><div className="text-2xl font-bold">{users.length - superCount}</div><div className="text-xs text-muted-foreground mt-0.5">Regular Users</div></CardContent></Card>
      </div>

      {error && (
        <div className="flex items-center gap-2 rounded-md border border-destructive/50 bg-destructive/10 p-3 text-sm text-destructive">
          <AlertTriangle className="size-4 shrink-0" />{error}
          <button onClick={() => setError(null)} className="ml-auto text-xs underline">Dismiss</button>
        </div>
      )}

      <Tabs value={activeTab} onValueChange={v => setActiveTab(v as DbEngine)}>
        <TabsList>
          <TabsTrigger value="postgresql">PostgreSQL</TabsTrigger>
          <TabsTrigger value="mariadb">MariaDB</TabsTrigger>
        </TabsList>

        {(['postgresql', 'mariadb'] as const).map(engine => (
          <TabsContent key={engine} value={engine}>
            <Card>
              <CardHeader className="pb-3">
                <CardTitle className="text-base">{engine === 'postgresql' ? 'PostgreSQL' : 'MariaDB'} Users</CardTitle>
              </CardHeader>
              <CardContent>
                {loading ? (
                  <div className="flex items-center justify-center h-32 text-sm text-muted-foreground">Loading users...</div>
                ) : users.length === 0 ? (
                  <div className="flex flex-col items-center justify-center h-32 gap-2 text-muted-foreground">
                    <User className="size-8 opacity-30" /><span className="text-sm">No users found</span>
                  </div>
                ) : (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Username</TableHead>
                        <TableHead>Role</TableHead>
                        <TableHead>Privileges</TableHead>
                        {engine === 'postgresql' && <TableHead className="text-right">Conn Limit</TableHead>}
                        <TableHead className="text-right">Actions</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {users.map(user => (
                        <TableRow key={user.username}>
                          <TableCell>
                            <div className="flex items-center gap-2">
                              <User className="size-4 text-muted-foreground" />
                              <span className="font-mono font-medium">{user.username}</span>
                            </div>
                          </TableCell>
                          <TableCell>
                            {user.isSuper ? (
                              <Badge className="text-xs bg-amber-500/20 text-amber-700 dark:text-amber-400 border border-amber-500/30 gap-1">
                                <ShieldCheck className="size-3" />Superuser
                              </Badge>
                            ) : (
                              <Badge variant="outline" className="text-xs gap-1">
                                <Shield className="size-3" />User
                              </Badge>
                            )}
                          </TableCell>
                          <TableCell>
                            <div className="flex flex-wrap gap-1">
                              {user.canCreateDb && <Badge variant="secondary" className="text-xs">CREATEDB</Badge>}
                              {user.canCreateRole && <Badge variant="secondary" className="text-xs">CREATEROLE</Badge>}
                              {!user.canCreateDb && !user.canCreateRole && !user.isSuper && (
                                <span className="text-xs text-muted-foreground">—</span>
                              )}
                            </div>
                          </TableCell>
                          {engine === 'postgresql' && (
                            <TableCell className="text-right text-sm text-muted-foreground tabular-nums">
                              {user.connectionLimit === -1 ? '∞' : user.connectionLimit}
                            </TableCell>
                          )}
                          <TableCell className="text-right">
                            <AlertDialog>
                              <AlertDialogTrigger asChild>
                                <Button
                                  variant="ghost"
                                  size="sm"
                                  className="text-destructive hover:text-destructive"
                                  disabled={user.username === 'postgres'}
                                >
                                  <Trash2 className="size-3.5" />
                                </Button>
                              </AlertDialogTrigger>
                              <AlertDialogContent>
                                <AlertDialogHeader>
                                  <AlertDialogTitle>Drop User</AlertDialogTitle>
                                  <AlertDialogDescription>
                                    Are you sure you want to drop user{' '}
                                    <strong className="font-mono">{user.username}</strong>?
                                  </AlertDialogDescription>
                                </AlertDialogHeader>
                                <AlertDialogFooter>
                                  <AlertDialogCancel>Cancel</AlertDialogCancel>
                                  <AlertDialogAction onClick={() => handleDrop(user.username)} className="bg-destructive hover:bg-destructive/90">
                                    Drop User
                                  </AlertDialogAction>
                                </AlertDialogFooter>
                              </AlertDialogContent>
                            </AlertDialog>
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                )}
              </CardContent>
            </Card>
          </TabsContent>
        ))}
      </Tabs>
    </div>
  );
}
