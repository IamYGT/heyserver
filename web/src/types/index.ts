// PHP-FPM Types — matches Go service JSON output

export interface PMSettings {
  max_children: number;
  start_servers: number;
  min_spare_servers: number;
  max_spare_servers: number;
  process_idle_timeout: string;
  max_requests: number;
}

export interface PHPPool {
  name: string;
  version: string;
  config_file: string;
  user: string;
  group: string;
  listen: string;
  pm: string;
  pm_settings: PMSettings;
  open_basedir: string;
  socket_exists: boolean;
  is_running: boolean;
}

export interface PHPVersion {
  version: string;
  active: boolean;
  info: string;
  pool_dir: string;
  pool_count: number;
  pools?: PHPPool[];
}

export interface CreatePoolRequest {
  name: string;
  version: string;
  user: string;
  group: string;
  domain_root: string;
  pm: 'dynamic' | 'static' | 'ondemand';
  pm_settings: PMSettings;
}

export interface UpdatePoolRequest {
  user?: string;
  group?: string;
  pm?: string;
  pm_settings?: Partial<PMSettings>;
  open_basedir?: string;
}
