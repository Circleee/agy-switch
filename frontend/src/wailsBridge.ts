export interface Config {
  proxy_enabled: boolean;
  proxy_url: string;
  skip_permissions: boolean;
  default_work_dir: string;
  preferred_term: string;
  auto_start: boolean;
  warn_on_running: boolean;
}

export interface QuotaBucket {
  id: string;
  name: string;
  description?: string;
  window: string;
  remaining_fraction: number;
  reset_time: string;
}

export interface QuotaGroup {
  name: string;
  description?: string;
  buckets: QuotaBucket[];
}

export interface AccountQuota {
  groups: QuotaGroup[];
  subscription_tier?: string;
  raw_tier?: string;
  is_forbidden?: boolean;
  forbidden_reason?: string;
  last_updated: number;
  source: string;
}

export interface Account {
  id: string;
  email: string;
  alias: string;
  disabled: boolean;
  created_at: number;
  last_used: number;
  quota?: AccountQuota;
}

export interface State {
  version: number;
  current_account_id: string;
  config: Config;
  accounts: Account[];
}

export interface TargetStatus {
  name: string;
  path: string;
  exists: boolean;
  email: string;
}

export interface HealthReport {
  active_email: string;
  consistent: boolean;
  targets: TargetStatus[];
}

export interface DashboardData {
  platform: string;
  state: State;
  health: HealthReport;
  agy_binary_path: string;
  running_agy_count: number;
  auto_start: boolean;
  atool_available: boolean;
  login_running: boolean;
  quota_refreshing: boolean;
}

export interface SwitchResult {
  switched: boolean;
  need_confirm: boolean;
  running_count: number;
  message: string;
  account?: Account;
}

export interface ImportResult {
  imported_count: number;
  emails: string[];
  source_dir: string;
}

export interface ProxyCheckResult {
  reachable: boolean;
  address: string;
  latency_ms: number;
  message: string;
}

export interface LoginFlowStatus {
  running: boolean;
  stage: string;
  email?: string;
  message: string;
}

export interface QuotaProgress {
  current: number;
  total: number;
  email: string;
  stage: string;
  error?: string;
}

declare global {
  interface Window {
    go?: {
      main?: {
        App?: Record<string, (...args: any[]) => Promise<any>>;
      };
    };
    runtime?: {
      EventsOn: (eventName: string, callback: (...data: any[]) => void) => () => void;
    };
  }
}

function callApp<T>(method: string, ...args: any[]): Promise<T> {
  const fn = window.go?.main?.App?.[method];
  if (!fn) {
    return Promise.reject(new Error(`Wails backend method ${method} unavailable`));
  }
  return fn(...args);
}

export const backend = {
  getDashboard: () => callApp<DashboardData>('GetDashboard'),
  switchAccount: (idOrQuery: string, force: boolean) =>
    callApp<SwitchResult>('SwitchAccount', idOrQuery, force),
  launchAgy: (continueSession: boolean) => callApp<void>('LaunchAgy', continueSession),
  switchAndLaunchAgy: (accountID: string, continueSession: boolean, force: boolean) =>
    callApp<SwitchResult>('SwitchAndLaunchAgy', accountID, continueSession, force),
  startOfficialLogin: () => callApp<void>('StartOfficialLogin'),
  cancelOfficialLogin: () => callApp<void>('CancelOfficialLogin'),
  captureCurrentSession: () => callApp<Account>('CaptureCurrentSession'),
  importFromAntigravityTools: () => callApp<ImportResult>('ImportFromAntigravityTools'),
  refreshAccountQuota: (accountID: string) => callApp<AccountQuota>('RefreshAccountQuota', accountID),
  refreshAllQuotas: () => callApp<void>('RefreshAllQuotas'),
  updateAlias: (accountID: string, alias: string) => callApp<void>('UpdateAlias', accountID, alias),
  moveAccount: (accountID: string, direction: number) =>
    callApp<void>('MoveAccount', accountID, direction),
  deleteAccount: (accountID: string) => callApp<void>('DeleteAccount', accountID),
  saveConfig: (cfg: Config) => callApp<void>('SaveConfig', cfg),
  testProxyConnection: (proxyURL: string) =>
    callApp<ProxyCheckResult>('TestProxyConnection', proxyURL),
  hideToTray: () => callApp<void>('HideToTray'),
};

export function onBackendEvent(
  eventName: string,
  handler: (...args: any[]) => void
): () => void {
  if (window.runtime?.EventsOn) {
    return window.runtime.EventsOn(eventName, handler);
  }
  return () => {};
}
