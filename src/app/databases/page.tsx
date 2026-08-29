'use client';

import { useState, useEffect, useTransition } from 'react';
import { useRouter } from 'next/navigation';
import {
  Database,
  Plus,
  Trash2,
  Download,
  Eye,
  RefreshCw,
  AlertTriangle,
  Users,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { listDatabases, createDatabase, dropDatabase, exportDatabase } from './actions';
import type { DatabaseInfo } from '@/lib/services/database';

type DbEngine = 'postgresql' | 'mariadb';

export default function DatabasesPage() {
  const router = useRouter();
  const [activeTab, setActiveTab] = useState<DbEngine>('postgresql');
  const [databases, setDatabases] = useState<DatabaseInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [, startTransition] = useTransition();

  // Create DB dialog
  const [createOpen, setCreateOpen] = useState(false);
  const [newDbName, setNewDbName] = useState('');
  const [newDbOwner, setNewDbOwner] = useState('postgres');
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);

  // Export state
  const [exportingDb, setExportingDb] = useState<string | null>(null);

  const fetchDatabases = (engine: DbEngine) => {
    setLoading(true);
    setError(null);
    startTransition(async () => {
      const res = await listDatabases(engine);
      if (res.success && res.data) {
        setDatabases(res.data);
      } else {
        setError(res.error ?? 'Failed to load databases');
        setDatabases([]);
      }
      setLoading(false);
    });
  };

  useEffect(() => {
    fetchDatabases(activeTab);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeTab]);

  const handleCreate = async () => {
    if (!newDbName.trim()) return;
    setCreating(true);
    setCreateError(null);
    const res = await createDatabase(activeTab, newDbName.trim(), newDbOwner.trim() || 'postgres');
    if (res.success) {
      setCreateOpen(false);
      setNewDbName('');
      setNewDbOwner('postgres');
      fetchDatabases(activeTab);
    } else {
      setCreateError(res.error ?? 'Failed to create database');
    }
    setCreating(false);
  };

  const handleDrop = async (name: string) => {
    const res = await dropDatabase(activeTab, name);
    if (res.success) {
      fetchDatabases(activeTab);
    } else {
      setError(res.error ?? 'Failed to drop database');
    }
  };

  const handleExport = async (name: string) => {
    setExportingDb(name);
    const res = await exportDatabase(activeTab, name);
    if (res.success && res.data) {
      alert(`Export saved to: ${res.data.filePath}`);
    } else {
      setError(res.error ?? 'Export failed');
    }
    setExportingDb(null);
  };

  const totalBytes = databases.reduce((s, d) => s + d.sizeBytes, 0);
  const fmtBytes = (b: number) => {
    if (!b) return '0 B';
    const u = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(b) / Math.log(1024));
    return `${(b / Math.pow(1024, i)).toFixed(1)} ${u[i]}`;
  };

  return (
    <div className="flex flex-col gap-6 p-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Databases</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Manage PostgreSQL and MariaDB databases
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={() => fetchDatabases(activeTab)} disabled={loading}>
            <RefreshCw className={`size-4 mr-1.5 ${loading ? 'animate-spin' : ''}`} />
            Refresh
          </Button>
          <Button variant="outline" size="sm" onClick={() => router.push('/databases/users')}>
            <Users className="size-4 mr-1.5" />
            Manage Users
          </Button>

          <Dialog open={createOpen} onOpenChange={setCreateOpen}>
            <DialogTrigger asChild>
              <Button size="sm">
                <Plus className="size-4 mr-1.5" />
                New Database
              </Button>
            </DialogTrigger>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>Create Database</DialogTitle>
              </DialogHeader>
              <div className="flex flex-col gap-4 py-2">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="db-name">Database name</Label>
                  <Input
                    id="db-name"
                    value={newDbName}
                    onChange={e => setNewDbName(e.target.value)}
                    placeholder="my_database"
                    autoFocus
                  />
                </div>
                {activeTab === 'postgresql' && (
                  <div className="flex flex-col gap-1.5">
                    <Label htmlFor="db-owner">Owner</Label>
                    <Input
                      id="db-owner"
                      value={newDbOwner}
                      onChange={e => setNewDbOwner(e.target.value)}
                      placeholder="postgres"
                    />
                  </div>
                )}
                {createError && <p className="text-sm text-destructive">{createError}</p>}
              </div>
              <DialogFooter>
                <Button variant="outline" onClick={() => setCreateOpen(false)}>Cancel</Button>
                <Button onClick={handleCreate} disabled={creating || !newDbName.trim()}>
                  {creating ? 'Creating...' : 'Create'}
                </Button>
              </DialogFooter>
            </DialogContent>
          </Dialog>
        </div>
      </div>

      {/* Stats */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        {[
          { label: 'Total Databases', value: databases.length },
          { label: 'Total Size', value: fmtBytes(totalBytes) },
          { label: 'Total Tables', value: databases.reduce((s, d) => s + d.tableCount, 0) },
          { label: 'Active Engine', value: activeTab === 'postgresql' ? 'PostgreSQL' : 'MariaDB' },
        ].map(({ label, value }) => (
          <Card key={label}>
            <CardContent className="pt-4">
              <div className="text-2xl font-bold">{value}</div>
              <div className="text-xs text-muted-foreground mt-0.5">{label}</div>
            </CardContent>
          </Card>
        ))}
      </div>

      {/* Error */}
      {error && (
        <div className="flex items-center gap-2 rounded-md border border-destructive/50 bg-destructive/10 p-3 text-sm text-destructive">
          <AlertTriangle className="size-4 shrink-0" />
          {error}
          <button onClick={() => setError(null)} className="ml-auto text-xs underline">Dismiss</button>
        </div>
      )}

      {/* Tabs */}
      <Tabs value={activeTab} onValueChange={v => setActiveTab(v as DbEngine)}>
        <TabsList>
          <TabsTrigger value="postgresql" className="gap-1.5">
            <Database className="size-3.5" />PostgreSQL
          </TabsTrigger>
          <TabsTrigger value="mariadb" className="gap-1.5">
            <Database className="size-3.5" />MariaDB
          </TabsTrigger>
        </TabsList>

        {(['postgresql', 'mariadb'] as const).map(engine => (
          <TabsContent key={engine} value={engine}>
            <Card>
              <CardHeader className="pb-3">
                <CardTitle className="text-base">
                  {engine === 'postgresql' ? 'PostgreSQL' : 'MariaDB'} Databases
                </CardTitle>
              </CardHeader>
              <CardContent>
                {loading ? (
                  <div className="flex items-center justify-center h-32 text-sm text-muted-foreground">
                    Loading databases...
                  </div>
                ) : databases.length === 0 ? (
                  <div className="flex flex-col items-center justify-center h-32 gap-2 text-muted-foreground">
                    <Database className="size-8 opacity-30" />
                    <span className="text-sm">No databases found</span>
                  </div>
                ) : (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Name</TableHead>
                        {engine === 'postgresql' && <TableHead>Owner</TableHead>}
                        <TableHead className="text-right">Tables</TableHead>
                        <TableHead className="text-right">Size</TableHead>
                        <TableHead className="text-right">Actions</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {databases.map(db => (
                        <TableRow key={db.name}>
                          <TableCell>
                            <div className="flex items-center gap-2">
                              <Database className="size-4 text-muted-foreground" />
                              <span className="font-mono font-medium">{db.name}</span>
                            </div>
                          </TableCell>
                          {engine === 'postgresql' && (
                            <TableCell>
                              <Badge variant="outline" className="text-xs">{db.owner}</Badge>
                            </TableCell>
                          )}
                          <TableCell className="text-right tabular-nums">{db.tableCount}</TableCell>
                          <TableCell className="text-right tabular-nums text-muted-foreground">{db.size}</TableCell>
                          <TableCell className="text-right">
                            <div className="flex items-center justify-end gap-1">
                              <Button
                                variant="ghost"
                                size="sm"
                                onClick={() => router.push(`/databases/${engine}/${db.name}`)}
                              >
                                <Eye className="size-3.5 mr-1" />Browse
                              </Button>
                              <Button
                                variant="ghost"
                                size="sm"
                                onClick={() => handleExport(db.name)}
                                disabled={exportingDb === db.name}
                              >
                                <Download className="size-3.5 mr-1" />
                                {exportingDb === db.name ? 'Exporting...' : 'Export'}
                              </Button>
                              <AlertDialog>
                                <AlertDialogTrigger asChild>
                                  <Button variant="ghost" size="sm" className="text-destructive hover:text-destructive">
                                    <Trash2 className="size-3.5" />
                                  </Button>
                                </AlertDialogTrigger>
                                <AlertDialogContent>
                                  <AlertDialogHeader>
                                    <AlertDialogTitle>Drop Database</AlertDialogTitle>
                                    <AlertDialogDescription>
                                      Are you sure you want to drop{' '}
                                      <strong className="font-mono">{db.name}</strong>?
                                      This is irreversible and all data will be lost.
                                    </AlertDialogDescription>
                                  </AlertDialogHeader>
                                  <AlertDialogFooter>
                                    <AlertDialogCancel>Cancel</AlertDialogCancel>
                                    <AlertDialogAction
                                      onClick={() => handleDrop(db.name)}
                                      className="bg-destructive hover:bg-destructive/90"
                                    >
                                      Drop Database
                                    </AlertDialogAction>
                                  </AlertDialogFooter>
                                </AlertDialogContent>
                              </AlertDialog>
                            </div>
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
