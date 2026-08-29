// Go model types — matches JSON API output

import type { IntegrationState } from '@/lib/integrationState'

export interface SystemStats {
  cpu: { usage: number; cores: number; model: string }
  memory: {
    total: number; used: number; free: number; percentage: number
    buffers: number; cached: number; available: number
    swapTotal: number; swapUsed: number; swapFree: number; swapPercentage: number
  }
  disk: Array<{ mount: string; total: number; used: number; free: number; percentage: number }>
  load: [number, number, number]
  uptime: number
  hostname: string
  os: string
  network: Array<{ interface: string; bytesIn: number; bytesOut: number }>
}

export interface TimeSeriesPoint {
  createdAt: string
  timestamp?: string
  cpu: number
  memory: number
}

// ── Metrics (historical) ────────────────────────────────────────────────────

export interface MetricRaw {
  timestamp: string
  cpu_percent: number
  memory_total: number
  memory_used: number
  memory_percent: number
  memory_buffers: number
  memory_cached: number
  memory_available: number
  swap_total: number
  swap_used: number
  swap_percent: number
  load_1m: number
  load_5m: number
  load_15m: number
  net_rx_bytes: number
  net_tx_bytes: number
  disk_root_percent: number
}

export interface MetricAggregated {
  bucket: string
  sample_count: number
  cpu_avg: number
  cpu_max: number
  mem_avg: number
  mem_max: number
  swap_avg: number
  swap_max: number
  load_1m_avg: number
  net_rx_total: number
  net_tx_total: number
  disk_root_avg: number
  disk_root_max: number
}

export interface MetricsHistoryResponse {
  range: string
  resolution: 'raw' | 'minute' | 'hourly'
  data: MetricRaw[] | MetricAggregated[]
}

export interface ProcessSnapshot {
  timestamp: string
  pid: number
  start_time?: number
  username: string
  cpu_percent: number
  memory_percent: number
  rss: number
  command: string
}

export interface ProcessSnapshotResponse {
  requested_at: string
  processes: ProcessSnapshot[]
}

export interface ServiceHistoryEntry {
  timestamp: string
  name: string
  status: string
  pid: number
}

export interface MetricsSummary {
  total_samples: number
  oldest_timestamp: string
  newest_timestamp: string
  db_size_bytes: number
}

export interface ServiceStatus {
  name: string
  status: 'running' | 'degraded' | 'starting' | 'stopping' | 'stopped' | 'failed' | 'unknown'
  pid?: number
  uptime?: string
  detail?: string
}

export interface Domain {
  id: string
  name: string
  type: string
  root: string
  phpVersion?: string
  proxyPort?: number
  sslEnabled: boolean
  isActive: boolean
}

export interface NginxConfig {
  filename: string
  domain: string
  type: string
  isEnabled: boolean
  content?: string
  checksum?: string
  size?: number
  modifiedAt?: string
}

export interface NginxCreateRequest {
  domain: string
  type: 'php' | 'static' | 'proxy' | 'redirect'
  phpVersion?: string
  phpPool?: string
  docRoot?: string
  proxyPass?: string
  redirectTo?: string
  useSSL: boolean
  certPath?: string
  keyPath?: string
}

export interface NginxArchiveReceipt {
  message: string
  archive: string
  checksum: string
}

export interface NginxConfigArchive {
  archive: string
  filename: string
  checksum: string
  size: number
  archivedAt: string
  modifiedAt: string
}

export interface NginxArchiveRestoreReceipt {
  message: string
  archive: string
  filename: string
  checksum: string
  isEnabled: boolean
}

export interface NginxConfigBackup {
  backup: string
  filename: string
  checksum: string
  size: number
  createdAt: string
  modifiedAt: string
}

export interface NginxBackupRestoreReceipt {
  message: string
  backup: string
  recovery: string
  filename: string
  checksum: string
  isEnabled: boolean
}

export interface NginxSaveReceipt {
  message: string
  backup: string
  checksum: string
}

export interface NginxTestResult {
  ok: boolean
  output: string
}

export interface NginxServiceStatus {
  installed: boolean
  status: string
  statusAvailable: boolean
  version: string
  uptime: string
  configTest: NginxTestResult
}

export interface PM2Process {
  id: number
  name: string
  status: 'online' | 'stopped' | 'errored' | 'launching' | 'stopping'
  pid?: number
  cpu: number
  memory: number
  uptime?: number
  restarts: number
  mode: 'fork' | 'cluster'
  script: string
  instances?: number
}

export interface MailStatus {
  running: boolean
  version?: string
  listeners: Array<{ protocol: string; port: number; address: string }>
  storage: { used: number; total: number; path: string }
  queued: number
}

export interface MailAccount {
  email: string
  name: string
  quota: number
  usedStorage: number
  isEnabled: boolean
}

export interface MailAlias {
  id: string
  alias: string
  destination: string
}

export interface MailDNSCheck {
  domain: string
  mx: { found: boolean; valid: boolean; value: string; mxRecords?: Array<{host: string; priority: number}> }
  spf: { found: boolean; valid: boolean; value: string }
  dkim: { found: boolean; valid: boolean; value: string }
  dmarc: { found: boolean; valid: boolean; value: string }
  score: number
  suggestions: string[]
}

export interface MailQueueItem {
  id: string
  from: string
  to: string
  subject: string
  status: string
  created: string
}

export interface DNSZone {
  domain: string
  file: string
  serial: number
  recordCount?: number
  records?: DNSRecord[]
}

export interface DNSRecord {
  name: string
  ttl: number
  type: string
  value: string
  priority?: number
}

export interface DNSStatus {
  available: boolean
  installed: boolean
  state: 'healthy' | 'not-installed' | 'not-configured' | 'stopped' | 'unavailable'
  active: boolean
  serviceState: string
  version?: string
  configAvailable: boolean
  checkToolsAvailable: boolean
  reloadAvailable: boolean
  zoneManagementReady: boolean
  recoveryPending: boolean
  error?: string
}

export interface PHPVersion {
  version: string
  active: boolean
  info: string
  pool_dir: string
  pool_count: number
  // computed by frontend:
  status?: 'running' | 'stopped' | 'failed' | 'unknown'
  poolCount?: number
}

export interface PHPPool {
  name: string
  version: string
  config_file: string
  user: string
  group: string
  listen: string
  pm: 'static' | 'dynamic' | 'ondemand'
  pm_settings: {
    max_children: number
    start_servers: number
    min_spare_servers: number
    max_spare_servers: number
    process_idle_timeout: string
    max_requests: number
  }
  open_basedir?: string
  socket_exists: boolean
  raw?: Record<string, string>
  // computed/optional:
  status?: 'active' | 'inactive'
  healthScore?: number
  memoryLimit?: string
  pmMaxChildren?: number
}

export interface PHPPoolDetail extends PHPPool {
  configFile: string
  rootPath?: string
  envVars?: Record<string, string>
  phpAdminValues?: Record<string, string>
  phpValues?: Record<string, string>
  slowlogTimeout?: string
  requestTerminateTimeout?: string
}

export interface CreatePoolRequest {
  version: string
  name: string
  user?: string
  userName?: string
  domain: string
  domainRoot: string
  pm?: 'static' | 'dynamic' | 'ondemand'
  pmMaxChildren?: number
}

// ─── PHP Extended Types ────────────────────────────────────────────────────────

export interface PHPPoolConfig {
  domain: string
  version: string
  pm: 'static' | 'dynamic' | 'ondemand'
  pmMaxChildren: number
  pmStartServers: number
  pmMinSpareServers: number
  pmMaxSpareServers: number
  pmMaxRequests: number
  memoryLimit: string
  maxExecutionTime: number
  uploadMaxFilesize: string
  postMaxSize: string
  slowlogTimeout: string
  slowlogEnabled: boolean
  accessLogEnabled: boolean
  user: string
  group: string
  socketPath: string
  securityProfile: string
}

export interface PHPPoolStatus {
  domain: string
  activeProcesses: number
  idleProcesses: number
  totalProcesses: number
  listenQueue: number
  maxChildrenReached: number
  acceptedConnections: number
  slowRequests: number
  healthScore: number
  uptime: string
}

export interface OPcacheStatus {
  enabled: boolean
  hitRate: number
  memoryUsagePercent: number
  usedMemoryMB: number
  freeMemoryMB: number
  wastedMemoryMB: number
  cachedScripts: number
  maxCachedKeys: number
  jitEnabled: boolean
  jitBuffer: string
}

export interface PHPExtension {
  name: string
  enabled: boolean
  version: string
  type: 'core' | 'database' | 'caching' | 'imaging' | 'security' | 'network' | 'other'
  description?: string
  dependencies?: string[]
}

export interface PHPSecurityProfile {
  name: string
  level: 'strict' | 'moderate' | 'permissive'
  score: number
  settings: Record<string, string>
}

export interface INIDirective {
  key: string
  value: string
  defaultValue: string
  section: string
  type: 'string' | 'integer' | 'boolean' | 'bytes'
  description?: string
  modified?: boolean
}

export interface PHPPreset {
  name: string
  label: string
  description: string
  pm: 'static' | 'dynamic' | 'ondemand'
  pmMaxChildren: number
  pmStartServers: number
  pmMinSpareServers: number
  pmMaxSpareServers: number
  pmMaxRequests: number
  memoryLimit: string
}

export interface PHPProcess {
  pid: number
  state: 'Running' | 'Idle' | 'Finishing'
  requestUri: string
  durationMs: number
  memoryMB: number
  method: string
  contentLength: number
}

export interface PHPLogEntry {
  timestamp: string
  level: string
  message: string
  file?: string
  line?: number
}

export interface PHPSlowLogEntry {
  timestamp: string
  pid: number
  script: string
  durationMs: number
  backtrace: string[]
}

export interface SSLCertificate {
  domain: string
  issuer: string
  subject: string
  serial: string
  notBefore: string
  notAfter: string
  expiresAt?: string
  daysRemaining: number
  isWildcard: boolean
  sans: string[]
  certPath: string
  keyPath: string
}

export interface SSLIssueRequest {
  domain: string
  challengeType: 'http-01' | 'dns-01'
  email: string
}

export interface SSLOperationResult {
  ok: boolean
  message: string
}

export interface AuthUser {
  id: number
  email: string
  name: string
  role: 'admin' | 'manager' | 'viewer'
  createdAt: string
  updatedAt: string
}

export interface LoginCredentials {
  email: string
  password: string
}

// Docker types
export interface DockerStatus {
  installed: boolean
  running: boolean
  version?: string
  containersTotal: number
  containersRunning: number
  imageCount: number
}

export interface DockerContainer {
  id: string
  name: string
  image: string
  status: 'running' | 'stopped' | 'exited' | 'paused' | 'restarting'
  ports: string[]
  cpuPercent: number
  memoryUsage: number
  memoryLimit: number
  created: string
}

export interface DockerImage {
  id: string
  name?: string
  repoTags?: string[]
  size: number | string
  created: string
}

export interface DockerLogs {
  logs: string
  tail: number
  truncated: boolean
}

// Deploy types
export interface DeployTemplate {
  id: string
  name: string
  description: string
  branch: string
  deploymentKind: 'script' | 'compose'
  composeFile: string
  deployScript: string
}

export interface DeployTemplateIssue {
  file: string
  message: string
}

export interface DeployTemplateInventory {
  status: 'not_configured' | 'healthy' | 'unavailable'
  directory: string
  templates: DeployTemplate[]
  issues: DeployTemplateIssue[]
}

export interface DeployTarget {
  id: string
  name: string
  repoUrl: string
  branch: string
  projectDir: string
  environment: 'production' | 'staging'
  sourceTargetId?: string
  deploymentKind: 'script' | 'compose'
  composeFile: string
  deployScript: string
  webhookProvider: 'github' | 'gitlab'
  webhookStatus: 'not_configured' | 'healthy' | 'unavailable'
  webhookToken: string
  autoDeploy: boolean
  isActive: boolean
  createdAt: string
  updatedAt: string
  // computed/legacy fields from older shape
  lastStatus?: 'success' | 'failed' | 'running' | 'pending'
  lastDeployedAt?: string
}

export interface CreateDeployStagingRequest {
  name: string
  branch: string
  projectDir: string
}

export interface DeployStagingReceipt {
  target: DeployTarget
  storageBoundary: 'isolated_project_directory'
  environmentValuesCopied: false
  webhookSecretCopied: false
  domainsCopied: false
  dnsConfigured: false
}

export interface DeployPreflightCheck {
  id: string
  status: 'pass' | 'fail' | 'pending'
  message: string
}

export interface DeployPreflight {
  targetId: string
  deploymentKind: 'script' | 'compose'
  eligible: boolean
  checks: DeployPreflightCheck[]
}

export interface DeployRevisionComparison {
  targetId: string
  state: 'not_deployed' | 'ready' | 'unavailable'
  branch: string
  currentCommit?: string
  deployedCommit?: string
  rollbackCommit?: string
  trackedChanges: boolean
  matchesDeployed: boolean
  rollbackAvailable: boolean
  commitsAheadRollback: number
  commitsBehindRollback: number
  filesChanged: number
  insertions: number
  deletions: number
  message: string
  checkedAt: string
}

export interface ComposeProjectService {
  service: string
  container: string
  image: string
  state: string
  health?: string
  exitCode: number
  ports: string[]
}

export interface ComposeProjectServiceLogs {
  logs: string
  tail: number
  truncated: boolean
}

export interface DeployEnvironment {
  configured: boolean
  variables: Array<{ key: string }>
}

export interface DeployProjectDomain {
  id: string
  targetId: string
  domain: string
  service: string
  hostPort: number
  upstream: string
  tlsStatus: 'not_configured' | 'healthy' | 'expiring' | 'expired' | 'unavailable'
  tlsExpiresAt?: string
  tlsDaysRemaining?: number
  tlsMessage: string
  createdAt: string
  updatedAt: string
}

export interface DeployProjectDomainHealth {
  domain: string
  upstream: string
  status: 'healthy' | 'unhealthy' | 'unavailable'
  statusCode?: number
  latencyMs: number
  message: string
  checkedAt: string
}

export interface DeployRun {
  id: string
  targetId: string
  status: 'success' | 'failed' | 'running' | 'pending'
  commit?: string
  prevCommit?: string
  commitSha?: string  // alias kept for display compatibility
  durationMs?: number
  duration?: number   // alias kept for display compatibility
  startedAt: string
  finishedAt?: string
  logs?: string
  log?: string        // alias kept for display compatibility
}

// ─── Database types ────────────────────────────────────────────────────────────

export type DbEngine = 'postgresql' | 'mariadb'

export interface Database {
  name: string
  engine: 'postgres' | 'mariadb'
  owner: string
  size: number | string
  tableCount?: number
  tables?: number
  encoding?: string
  collation?: string
}

export interface DatabaseTable {
  name: string
  schema?: string
  rows?: number
  rowsEstimate?: number
  size: number | string
  type?: string
  tableType?: string
}

export interface TableColumn {
  name: string
  type: string
  nullable: boolean
  default?: string
  isPrimary: boolean
}

export interface TableStructure {
  name: string
  columns: TableColumn[]
  indexes?: Array<{ name: string; columns: string[]; unique: boolean }>
}

export interface QueryResult {
  columns: string[]
  rows: Record<string, unknown>[]
  rowsAffected?: number
  duration?: number
  error?: string
}

export interface DatabaseUser {
  name: string
  engine: DbEngine
  canLogin: boolean
  superuser: boolean
  databases?: string[]
}

export interface CreateDatabaseRequest {
  name: string
  owner: string
}

export interface CreateDbUserRequest {
  name: string
  password: string
  engine: DbEngine
}

export interface PGMCredential {
  id: number
  dbName: string
  dbUser: string
  dbPassword: string
  dbHost: string
  dbPort: number
  connectionString?: string
  notes?: string
  isActive: boolean
  createdAt: string
}

export interface PGMBackup {
  name: string
  path: string
  size: string
  databases: number
  createdAt: string
}

export interface Backup {
  id: string
  name?: string
  type: 'full' | 'database' | 'files'
  status: 'pending' | 'running' | 'completed' | 'failed' | 'invalid' | 'orphaned'
  compression?: 'gzip' | 'zstd' | 'none'
  size?: number
  diskSize?: number
  path?: string
  note?: string
  createdAt?: string
  created_at?: string
  completed_at?: string
  error?: string
}

export interface BackupRestoreValidation {
  id: string
  name: string
  type: Backup['type']
  artifactBytes: number
  includesDatabase: boolean
  includesFiles: boolean
  databaseEngine?: 'postgresql' | 'mariadb'
  databaseTarget?: string
  databaseRecovery: boolean
  filesRollback: boolean
}

export interface BackupStorageSummary {
  directory: string
  legacyDirectories: string[]
  totalBytes: number
  activeBytes: number
  completedBytes: number
  invalidBytes: number
  orphanedBytes: number
  legacyOrphanedBytes: number
  completedCount: number
  invalidCount: number
  orphanedCount: number
  legacyOrphanedCount: number
  rootSize: number
  rootUsed: number
  rootAvailable: number
  rootUsePercent: number
  backupVolumeSize: number
  backupVolumeUsed: number
  backupVolumeAvailable: number
  backupVolumeUsePercent: number
}

export interface CreateBackupRequest {
  type: Backup['type']
  compression?: number
  name?: string
  database?: string
  engine?: 'postgresql' | 'mariadb'
  retention?: number
  /** Observed site folder names under the installation-owned vhost root. */
  vhosts?: string[]
}

export interface BackupSchedule {
  frequency?: 'daily' | 'weekly' | 'monthly'
  time?: string
  retention_count: number
  /** @deprecated Compatibility alias; the value has always represented a backup count. */
  retention_days: number
  cron: string
  type: string
  rawLine: string
}

export interface BackupScheduleRequest {
  frequency: NonNullable<BackupSchedule['frequency']>
  time: string
  retention_count: number
  type?: 'full' | 'database' | 'files' | 'snapshot'
  database?: string
}

export interface BackupScheduleDeleteRequest {
  rawLine: string
}

export type BackupJobPhase =
  | 'preparing'
  | 'database'
  | 'files'
  | 'archive'
  | 'retention'
  | 'gdrive_upload'
  | 'gdrive_restore'
  | 'restore'
  | 'verify'
  | 'done'
  | string

export interface BackupJob {
  id: string
  jobId?: string
  status: 'pending' | 'running' | 'completed' | 'failed'
  progress: number
  phase?: BackupJobPhase
  type?: string
  source?: string
  message?: string
  error?: string
  startedAt?: string
  doneAt?: string
  etaSeconds?: number
  bytesDone?: number
  bytesTotal?: number
  sizeEstimate?: number
  outputFile?: string
  speed?: string
  command?: string
  logs?: string[]
}

export interface GDriveStorageQuota {
  limit: number
  usage: number
  usageInDrive: number
  usagePercentage: number
}

export interface GDriveSettings {
  folder: string
  autoUpload: boolean
  remoteRetentionDays: number
  notifyOnSuccess: boolean
  notifyOnFailure: boolean
  lastUploadAt?: string
  lastUploadFile?: string
  lastError?: string
}

export interface GDriveSettingsUpdateRequest {
	folder: string
	autoUpload: boolean
	remoteRetentionDays: number
	notifyOnSuccess: boolean
	notifyOnFailure: boolean
}

export interface GDriveOAuthAppUpdateRequest {
	clientId?: string
	clientSecret?: string
	gcpProjectId?: string
}

export interface GDriveOAuthCompleteRequest {
	state: string
}

export interface GDriveRestoreRequest {
	fileName: string
}

export interface GDriveOAuthSetupLinks {
  consoleCredentials: string
  enableDriveAPI: string
  createOAuthClient: string
}

export interface GDriveOAuthAppInfo {
  configured: boolean
  clientId?: string
  clientIdMasked?: string
  hasSecret: boolean
  redirectUri: string
  credentialsSource: 'env' | 'vendor' | 'panel' | 'none' | string
  gcpProjectId?: string
  setupLinks?: GDriveOAuthSetupLinks
  expressAvailable?: boolean
  consoleAutomatable?: boolean
}

export type SnapshotManifestId =
  | 'vhosts'
  | 'nginx'
  | 'letsencrypt'
  | 'postgresql-cfg'
  | 'mysql-cfg'
  | 'php'
  | 'hserver-data'
  | 'cron-d'
  | 'systemd'
  | 'root-crontab'

export interface SnapshotManifestEntry {
  id: SnapshotManifestId
  path: string
  label: string
  required?: boolean
  enabled?: boolean
  available?: boolean
}

export interface SnapshotRepoStats {
  snapshotCount: number
  totalSize: number
  totalFileSize: number
}

export type SnapshotDestination = 'gdrive' | 's3'
export type SnapshotDestinationStatus = 'not_configured' | 'unavailable' | 'healthy'

export interface SnapshotSettings {
	 destination: SnapshotDestination
  repoFolder: string
  enabledPaths?: SnapshotManifestId[] | null
  keepDaily: number
  keepWeekly: number
  keepMonthly: number
  lastRunAt?: string
  lastSnapshotId?: string
  lastError?: string
  passwordAcknowledged?: boolean
}

export interface SnapshotSettingsUpdateRequest {
	 destination: SnapshotDestination
  repoFolder: string
  enabledPaths: SnapshotManifestId[]
  keepDaily: number
  keepWeekly: number
  keepMonthly: number
  passwordAcknowledged: boolean
}

export interface SnapshotRestoreRequest {
  snapshotId: string
  manifestIds?: Exclude<SnapshotManifestId, 'root-crontab'>[]
  vhosts?: string[]
}

export interface SnapshotPurgeRepositoryRequest {
  repoFolder: string
  confirmation: 'purge-snapshot-repository'
}

export interface ResticSnapshot {
  id: string
  time: string
  hostname?: string
  tags?: string[]
  paths?: number
  size?: number
}

export interface SnapshotStatus {
  resticFound: boolean
  repoInitialized: boolean
  passwordSet: boolean
  destination: SnapshotDestination
  destinationStatus: SnapshotDestinationStatus
  destinationMessage?: string
  canPurgeRepository: boolean
  driveConnected: boolean
  settings: SnapshotSettings
  manifest: SnapshotManifestEntry[]
  repoStats?: SnapshotRepoStats
  lastSnapshots?: ResticSnapshot[]
}

export interface GDriveStatus {
  connected: boolean
  /** Canonical availability state; legacy boolean fields remain for compatibility. */
  state?: IntegrationState
  message?: string
  reconnectRequired?: boolean
  configured: boolean
  email?: string
  displayName?: string
  quota?: GDriveStorageQuota
  settings: GDriveSettings
  rcloneFound: boolean
  redirectUri?: string
  credentialsSource?: string
  oauthApp?: GDriveOAuthAppInfo
}

export interface GDriveRemoteBackup {
  name: string
  path: string
  size: number
  modTime: string
}

export interface CronJob {
  id: string
  schedule: string
  command: string
  user: string
  isActive: boolean
  description: string
  humanSchedule?: string
}

export interface CronStatus {
  available: boolean
  installed: boolean
  running: boolean
  state: 'healthy' | 'not-installed' | 'stopped' | 'unavailable'
  daemonState: string
  error?: string
}

export interface CreateCronJobRequest {
  schedule: string
  command: string
  user: string
  description: string
}

export interface UpdateCronJobRequest {
  schedule: string
  command: string
  description: string
  isActive: boolean
}

export interface SystemCronFile {
  path: string
  type: string
  entries: string[]
}

export interface SystemCronInfo {
  cron_daemon: string
  cron_status: string
  files: SystemCronFile[]
  jobs?: Array<{
    user?: string
  userName?: string
    schedule: string
    command: string
    raw: string
  }>
}

export interface FirewallRule {
  id: string
  chain: 'INPUT' | 'OUTPUT' | 'FORWARD'
  protocol: 'tcp' | 'udp' | 'icmp' | 'all'
  source?: string
  destination?: string
  sport?: string
  dport?: string
  action: 'ACCEPT' | 'DROP' | 'REJECT'
  comment?: string
  enabled: boolean
  position: number
}

export interface FirewallStatus {
  enabled: boolean
  default_policy_input: 'ACCEPT' | 'DROP' | 'REJECT'
  default_policy_output: 'ACCEPT' | 'DROP' | 'REJECT'
  default_policy_forward: 'ACCEPT' | 'DROP' | 'REJECT'
  rules: FirewallRule[]
}

export interface FileEntry {
  name: string
  path: string
  type: 'file' | 'directory' | 'symlink'
  size: number | string
  mode: string
  owner: string
  group: string
  modified_at: string
}

export interface User {
  id: string
  username: string
  email: string
  role: 'admin' | 'manager' | 'viewer'
  created_at: string
  last_login?: string
  isEnabled: boolean
}

export interface CreateUserRequest {
  username: string
  email: string
  password: string
  role: User['role']
}

export interface AuditLog {
  id: string
  user?: string
  userName?: string
  action: string
  resource: string
  resource_id?: string
  details?: Record<string, unknown>
  ip: string
  createdAt: string
  timestamp?: string
  success: boolean
}

// ─── Cloudflare types ──────────────────────────────────────────────────────────

export interface CFZone {
  id: string
  name: string
  status: string
  plan: { id: string; name: string }
  name_servers: string[]
}

export interface CFRecord {
  id: string
  type: string
  name: string
  content: string
  ttl: number
  proxied: boolean
  priority?: number
}

export interface CFEmailRoutingRule {
  tag: string
  enabled: boolean
  matchers: Array<{ type: string; field?: string; value?: string }>
  actions: Array<{ type: string; value?: string[] }>
}

export interface CFEmailRouting {
  enabled: boolean
  name?: string
  status?: string
  created?: string
  modified?: string
  rules: CFEmailRoutingRule[]
}

export interface NginxSnippet {
  name: string
  path: string
  content: string
}

export interface AppSettings {
  site_name: string
  admin_email: string
  timezone: string
  log_retention_days: number
  backup_retention_days: number
  session_timeout_minutes: number
  two_factor_enabled: boolean
  smtp_host?: string
  smtp_port?: number
  smtp_user?: string
  smtp_from?: string
}

// Uptime Monitoring
export interface UptimeMonitor {
  id: number
  name: string
  type: 'http' | 'tcp' | 'dns' | 'ping'
  url?: string
  hostname?: string
  port?: number
  method: string
  interval_secs: number
  timeout_secs: number
  retries: number
  retry_interval: number
  accepted_statuscodes: string
  keyword?: string
  keyword_invert: boolean
  req_headers?: string
  req_body?: string
  tls_check: boolean
  tls_expiry_warn_days: number
  is_active: boolean
  maintenance_mode: boolean
  description?: string
  max_redirects: number
  alert_channel_ids?: number[]
  alert_reminder_mins: number
  current_status?: number
  uptime_24h?: number
  last_check_at?: string
  avg_ping_ms?: number
  created_at: string
  updated_at: string
}

export interface UptimeHeartbeat {
  id: number
  monitor_id: number
  status: number
  msg?: string
  ping_ms?: number
  status_code?: number
  tls_expiry?: string
  created_at: string
}

export interface UptimeIncident {
  id: number
  monitor_id: number
  monitor_name?: string
  type: string
  cause?: string
  started_at: string
  resolved_at?: string
  duration_secs?: number
}

export interface UptimeSummary {
  up: number
  down: number
  paused: number
  maintenance: number
}

export interface UptimeStatusPage {
  id: number
  slug: string
  title: string
  description?: string
  theme: string
  logo_url?: string
  is_public: boolean
  history_days: number
  monitors?: { monitor_id: number; display_name?: string; sort_order: number }[]
  created_at: string
}

export interface UptimeStats {
  uptime_24h: number
  uptime_7d: number
  uptime_30d: number
  uptime_90d: number
  avg_ping_ms: number
  p95_ping_ms: number
  p99_ping_ms: number
}
