import React, { useEffect, useState, useCallback } from 'react';
import logoUrl from './assets/logo.png';
import {
  backend,
  onBackendEvent,
  DashboardData,
  Config,
  Account,
  QuotaGroup,
  QuotaBucket,
  ProxyCheckResult,
  LoginFlowStatus,
  QuotaProgress,
} from './wailsBridge';

type ViewKey = 'accounts' | 'settings';

/* 统一 13px 线性 Web SVG 图标 */
const IconPlus = () => (
  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="square">
    <path d="M12 5v14M5 12h14" />
  </svg>
);
const IconRefresh = ({ spin = false }: { spin?: boolean }) => (
  <svg className={spin ? 'spin' : ''} width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <path d="M21 12a9 9 0 1 1-2.64-6.36" />
    <polyline points="21 3 21 9 15 9" />
  </svg>
);
const IconTerminal = () => (
  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <polyline points="4 17 10 11 4 5" />
    <line x1="12" y1="19" x2="20" y2="19" />
  </svg>
);
const IconImport = () => (
  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
    <polyline points="7 10 12 15 17 10" />
    <line x1="12" y1="15" x2="12" y2="3" />
  </svg>
);
const IconTrash = () => (
  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round">
    <line x1="18" y1="6" x2="6" y2="18" />
    <line x1="6" y1="6" x2="18" y2="18" />
  </svg>
);

function shortReset(isoStr: string): string {
  if (!isoStr || isoStr.startsWith('1970-01-01')) return '';
  const target = new Date(isoStr).getTime();
  if (isNaN(target)) return '';
  const diffMs = target - Date.now();
  if (diffMs <= 0) return '';
  const mins = Math.ceil(diffMs / 60000);
  if (mins >= 1440) return `${Math.floor(mins / 1440)}d${Math.floor((mins % 1440) / 60)}h`;
  if (mins >= 60) return `${Math.floor(mins / 60)}h${String(mins % 60).padStart(2, '0')}m`;
  return `${mins}m`;
}

function barTone(pct: number): string {
  if (pct >= 60) return 'good';
  if (pct >= 25) return 'warn';
  return 'low';
}

function shortGroupName(name: string): string {
  const lower = name.toLowerCase();
  if (lower.includes('gemini')) return 'Gemini';
  if (lower.includes('claude') || lower.includes('gpt')) return 'Claude / GPT';
  return name;
}

export default function App() {
  const [view, setView] = useState<ViewKey>('accounts');
  const [data, setData] = useState<DashboardData | null>(null);
  const [cfgDraft, setCfgDraft] = useState<Config | null>(null);
  const [toast, setToast] = useState<{ kind: string; message: string } | null>(null);
  const [loginStatus, setLoginStatus] = useState<LoginFlowStatus | null>(null);
  const [quotaProg, setQuotaProg] = useState<QuotaProgress | null>(null);
  const [localRefreshTarget, setLocalRefreshTarget] = useState<string>('');
  const [proxyTest, setProxyTest] = useState<ProxyCheckResult | null>(null);
  const [testingProxy, setTestingProxy] = useState(false);
  const [editingAliasId, setEditingAliasId] = useState<string | null>(null);
  const [aliasInput, setAliasInput] = useState('');
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null);
  const [confirmSwitch, setConfirmSwitch] = useState<{
    accountId: string;
    andLaunch: boolean;
  } | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  const showToast = useCallback((kind: string, message: string) => {
    setToast({ kind, message });
  }, []);

  useEffect(() => {
    if (!toast) return;
    const t = setTimeout(() => setToast(null), 2600);
    return () => clearTimeout(t);
  }, [toast]);

  const reload = useCallback(async () => {
    try {
      const d = await backend.getDashboard();
      setData(d);
      setCfgDraft({ ...d.state.config, auto_start: d.auto_start });
    } catch (e: any) {
      showToast('error', e?.message || String(e));
    }
  }, [showToast]);

  useEffect(() => {
    reload();
    const offState = onBackendEvent('state:updated', () => {
      backend.getDashboard().then((d) => {
        setData(d);
        setCfgDraft({ ...d.state.config, auto_start: d.auto_start });
        if (!d.quota_refreshing) {
          setLocalRefreshTarget('');
        }
      }).catch(() => {});
    });
    const offToast = onBackendEvent('app:toast', (p: any) => {
      if (p?.message) showToast(p.kind || 'info', p.message);
    });
    const offLogin = onBackendEvent('login:status', (st: LoginFlowStatus) => {
      setLoginStatus(st.running ? st : null);
      if (!st.running && st.message) {
        showToast(st.stage === 'captured' ? 'success' : 'info', st.message);
      }
      reload();
    });
    const offQuota = onBackendEvent('quota:progress', (p: QuotaProgress) => {
      if (p.stage === 'done') {
        setQuotaProg(null);
        setLocalRefreshTarget('');
      } else {
        setQuotaProg(p);
      }
    });
    const offConfirm = onBackendEvent('switch:confirm_needed', (p: any) => {
      if (p?.account_id) {
        setConfirmSwitch({ accountId: p.account_id, andLaunch: false });
      }
    });
    return () => {
      offState();
      offToast();
      offLogin();
      offQuota();
      offConfirm();
    };
  }, [reload, showToast]);

  const handleSwitch = async (accountId: string, andLaunch = false, force = false) => {
    setBusyId(accountId);
    try {
      const res = andLaunch
        ? await backend.switchAndLaunchAgy(accountId, false, force)
        : await backend.switchAccount(accountId, force);
      if (res.need_confirm) {
        setConfirmSwitch({ accountId, andLaunch });
        return;
      }
      setConfirmSwitch(null);
      await reload();
    } catch (e: any) {
      showToast('error', e?.message || String(e));
    } finally {
      setBusyId(null);
    }
  };

  const applyCfg = async (next: Config, notify = false) => {
    setCfgDraft(next);
    try {
      await backend.saveConfig(next);
      if (notify) showToast('success', '已保存');
      const d = await backend.getDashboard();
      setData(d);
    } catch (e: any) {
      showToast('error', e?.message || String(e));
    }
  };

  const handleTestProxy = async (urlToTest: string) => {
    setTestingProxy(true);
    try {
      const res = await backend.testProxyConnection(urlToTest);
      setProxyTest(res);
    } finally {
      setTestingProxy(false);
    }
  };

  const accounts = data?.state.accounts ?? [];
  const currentId = data?.state.current_account_id ?? '';
  const cfg = data?.state.config;
  const activeRefreshTarget =
    localRefreshTarget || data?.quota_refresh_target || (quotaProg ? 'all' : '');
  const refreshing = Boolean(activeRefreshTarget || data?.quota_refreshing);

  const runSingleRefresh = async (accId: string) => {
    const tag = accId || 'current';
    setLocalRefreshTarget(tag);
    try {
      await backend.refreshAccountQuota(accId);
      await reload();
    } catch (e: any) {
      showToast('error', e?.message || String(e));
    } finally {
      setLocalRefreshTarget('');
    }
  };

  const runAllRefresh = async () => {
    setLocalRefreshTarget('all');
    try {
      await backend.refreshAllQuotas();
    } catch (e: any) {
      setLocalRefreshTarget('');
      showToast('error', e?.message || String(e));
    }
  };

  return (
    <div className="shell">
      {/* 紧凑一体化 Web 顶栏 */}
      <header className="header">
        <div className="header-left">
          <img className="logo-img" src={logoUrl} alt="AGY" />
          <nav className="nav-tabs">
            <button
              className={`nav-tab ${view === 'accounts' ? 'active' : ''}`}
              onClick={() => setView('accounts')}
            >
              账号 <span className="count">{accounts.length}</span>
            </button>
            <button
              className={`nav-tab ${view === 'settings' ? 'active' : ''}`}
              onClick={() => setView('settings')}
            >
              设置
            </button>
          </nav>
        </div>

        <div className="header-right">
          <button
            className="btn btn-sm"
            onClick={() => cfgDraft && applyCfg({ ...cfgDraft, proxy_enabled: !cfgDraft.proxy_enabled })}
            title="点击切换代理开关"
          >
            <span className={`status-dot ${cfg?.proxy_enabled ? 'on' : 'off'}`} />
            <span>{cfg?.proxy_enabled ? cfg.proxy_url.replace(/^https?:\/\//, '') : '直连'}</span>
          </button>
          <button
            className="btn btn-primary btn-sm"
            onClick={() => backend.launchAgy(false).catch((e) => showToast('error', e?.message || String(e)))}
          >
            <IconTerminal />
            <span>启动 agy</span>
          </button>
        </div>
      </header>

      {/* 状态提示条 */}
      {toast && (
        <div className={`statusbar status-${toast.kind}`} onClick={() => setToast(null)}>
          <span>{toast.message}</span>
          <span>×</span>
        </div>
      )}

      {(loginStatus?.running || data?.login_running) && (
        <div className="statusbar status-info">
          <span>等待浏览器完成登录...</span>
          <button className="btn btn-xs btn-danger" onClick={() => backend.cancelOfficialLogin()}>
            取消
          </button>
        </div>
      )}

      {refreshing && (
        <div className="statusbar status-info">
          <span>
            {quotaProg
              ? quotaProg.stage === 'restoring'
                ? '正在恢复原账号...'
                : `刷新进度 ${quotaProg.current}/${quotaProg.total} · ${quotaProg.email}`
              : '正在查询余量...'}
          </span>
        </div>
      )}

      {confirmSwitch && (
        <div className="overlay">
          <div className="modal">
            <div className="modal-hd">检测到运行中的 agy 进程</div>
            <div className="modal-bd">继续切号可能导致旧窗口串号，确认强制切换？</div>
            <div className="modal-ft">
              <button className="btn btn-sm" onClick={() => setConfirmSwitch(null)}>取消</button>
              <button
                className="btn btn-primary btn-sm"
                onClick={() => handleSwitch(confirmSwitch.accountId, confirmSwitch.andLaunch, true)}
              >
                强制切换
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 账号视图 */}
      {view === 'accounts' && (
        <>
          <div className="actionbar">
            <div className="btn-group">
              <button
                className="btn btn-sm"
                disabled={Boolean(data?.login_running)}
                onClick={() => backend.startOfficialLogin().catch((e) => showToast('error', e?.message || String(e)))}
              >
                <IconPlus />
                <span>添加账号</span>
              </button>
              {data?.atool_available && (
                <button
                  className="btn btn-sm"
                  onClick={() =>
                    backend
                      .importFromAntigravityTools()
                      .then((r) => {
                        showToast('success', `已导入 ${r.imported_count} 个账号`);
                        reload();
                      })
                      .catch((e) => showToast('error', e?.message || String(e)))
                  }
                >
                  <IconImport />
                  <span>导入</span>
                </button>
              )}
            </div>

            <div className="btn-group">
              <button
                className="btn btn-sm"
                disabled={refreshing || accounts.length === 0}
                onClick={() => runSingleRefresh('')}
              >
                <IconRefresh spin={activeRefreshTarget === 'current'} />
                <span>刷当前</span>
              </button>
              <button
                className="btn btn-sm"
                disabled={refreshing || accounts.length === 0}
                onClick={runAllRefresh}
              >
                <IconRefresh spin={activeRefreshTarget === 'all'} />
                <span>刷全部</span>
              </button>
            </div>
          </div>

          <main className="scroll-body">
            {accounts.length === 0 ? (
              <div className="empty-box">
                <span>暂无已保存账号</span>
                <button
                  className="btn btn-sm"
                  onClick={() => backend.captureCurrentSession().then(() => reload()).catch(() => {})}
                >
                  读取本机当前账号
                </button>
              </div>
            ) : (
              <div className="table-list">
                {accounts.map((acc: Account, idx: number) => {
                  const isCurrent = acc.id === currentId;
                  return (
                    <div key={acc.id} className={`acc-row ${isCurrent ? 'is-current' : ''}`}>
                      <div className="acc-head">
                        <div className="acc-id">
                          <span className="acc-idx">{String(idx + 1).padStart(2, '0')}</span>
                          <span className="acc-mail" title={acc.email}>{acc.email}</span>
                          {editingAliasId === acc.id ? (
                            <input
                              className="inline-input"
                              value={aliasInput}
                              placeholder="备注"
                              autoFocus
                              onBlur={() => {
                                backend.updateAlias(acc.id, aliasInput).then(() => {
                                  setEditingAliasId(null);
                                  reload();
                                });
                              }}
                              onChange={(e) => setAliasInput(e.target.value)}
                              onKeyDown={(e) => {
                                if (e.key === 'Enter') (e.target as HTMLInputElement).blur();
                                if (e.key === 'Escape') setEditingAliasId(null);
                              }}
                            />
                          ) : (
                            <button
                              className="tag-btn"
                              onClick={() => {
                                setEditingAliasId(acc.id);
                                setAliasInput(acc.alias || '');
                              }}
                            >
                              {acc.alias || '+备注'}
                            </button>
                          )}
                        </div>

                        <div className="acc-actions">
                          {isCurrent ? (
                            <span className="badge-current">当前</span>
                          ) : (
                            <button
                              className="btn btn-xs btn-primary"
                              disabled={busyId === acc.id}
                              onClick={() => handleSwitch(acc.id, false)}
                            >
                              切换
                            </button>
                          )}
                          <button
                            className="btn btn-xs btn-icon"
                            title={isCurrent ? '启动 agy' : '切号并启动'}
                            onClick={() =>
                              isCurrent
                                ? backend.launchAgy(false).catch((e) => showToast('error', e?.message || String(e)))
                                : handleSwitch(acc.id, true)
                            }
                          >
                            <IconTerminal />
                          </button>
                          <button
                            className="btn btn-xs btn-icon"
                            title="刷新此号余量"
                            disabled={refreshing}
                            onClick={() => runSingleRefresh(acc.id)}
                          >
                            <IconRefresh spin={activeRefreshTarget === acc.id} />
                          </button>
                          {confirmDeleteId === acc.id ? (
                            <button
                              className="btn btn-xs btn-danger"
                              onMouseLeave={() => setConfirmDeleteId(null)}
                              onClick={() =>
                                backend.deleteAccount(acc.id).then(() => {
                                  setConfirmDeleteId(null);
                                  reload();
                                })
                              }
                            >
                              确认
                            </button>
                          ) : (
                            <button
                              className="btn btn-xs btn-icon btn-del"
                              title="移除"
                              onClick={() => setConfirmDeleteId(acc.id)}
                            >
                              <IconTrash />
                            </button>
                          )}
                        </div>
                      </div>

                      {acc.quota?.groups && acc.quota.groups.length > 0 && (
                        <div className="quota-grid">
                          {acc.quota.groups.map((grp: QuotaGroup, gi: number) => {
                            const b5 = grp.buckets.find((b: QuotaBucket) => b.window !== 'weekly');
                            const bw = grp.buckets.find((b: QuotaBucket) => b.window === 'weekly');
                            const p5 = b5 ? Math.round(b5.remaining_fraction * 100) : null;
                            const pw = bw ? Math.round(bw.remaining_fraction * 100) : null;
                            const rst = b5 ? shortReset(b5.reset_time) : '';
                            const mainPct = p5 ?? pw ?? 0;
                            return (
                              <div key={gi} className="metric-box">
                                <div className="metric-top">
                                  <span className="metric-name">{shortGroupName(grp.name)}</span>
                                  <span className="metric-nums">
                                    <strong>{p5 !== null ? `${p5}%` : '-'}</strong>
                                    {pw !== null && <em>周{pw}%</em>}
                                    {rst && <i>{rst}</i>}
                                  </span>
                                </div>
                                <div className="metric-track">
                                  <div
                                    className={`metric-bar ${barTone(mainPct)}`}
                                    style={{ width: `${Math.max(0, Math.min(100, mainPct))}%` }}
                                  />
                                </div>
                              </div>
                            );
                          })}
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
            )}
          </main>
        </>
      )}

      {/* 设置视图：紧凑双列 Web 表单 */}
      {view === 'settings' && cfgDraft && (
        <main className="scroll-body">
          <div className="form-table">
            <div className="form-row">
              <span className="form-label">启用代理</span>
              <button
                className={`toggle ${cfgDraft.proxy_enabled ? 'on' : ''}`}
                onClick={() => applyCfg({ ...cfgDraft, proxy_enabled: !cfgDraft.proxy_enabled })}
              />
            </div>

            <div className="form-row form-row-stack">
              <div className="form-line">
                <span className="form-label">代理地址</span>
                <div className="btn-group">
                  {['7897', '7890', '10808'].map((port) => {
                    const url = `http://127.0.0.1:${port}`;
                    const isAct = cfgDraft.proxy_url.endsWith(`:${port}`);
                    return (
                      <button
                        key={port}
                        className={`btn btn-xs ${isAct ? 'btn-active' : ''}`}
                        onClick={() => {
                          const next = { ...cfgDraft, proxy_url: url };
                          applyCfg(next);
                          handleTestProxy(url);
                        }}
                      >
                        :{port}
                      </button>
                    );
                  })}
                </div>
              </div>
              <div className="input-group">
                <input
                  className="field"
                  value={cfgDraft.proxy_url}
                  placeholder="http://127.0.0.1:7897"
                  onChange={(e) => {
                    setCfgDraft({ ...cfgDraft, proxy_url: e.target.value });
                    setProxyTest(null);
                  }}
                  onBlur={() => applyCfg(cfgDraft)}
                  onKeyDown={(e) => e.key === 'Enter' && applyCfg(cfgDraft, true)}
                />
                <button
                  className="btn btn-sm"
                  disabled={testingProxy}
                  onClick={() => handleTestProxy(cfgDraft.proxy_url)}
                >
                  {testingProxy ? '...' : proxyTest ? (proxyTest.reachable ? `${proxyTest.latency_ms}ms` : '不通') : '测速'}
                </button>
              </div>
            </div>

            <div className="form-row">
              <span className="form-label">跳过权限确认</span>
              <button
                className={`toggle ${cfgDraft.skip_permissions ? 'on' : ''}`}
                onClick={() => applyCfg({ ...cfgDraft, skip_permissions: !cfgDraft.skip_permissions })}
              />
            </div>

            <div className="form-row">
              <span className="form-label">切号防串号保护</span>
              <button
                className={`toggle ${cfgDraft.warn_on_running ? 'on' : ''}`}
                onClick={() => applyCfg({ ...cfgDraft, warn_on_running: !cfgDraft.warn_on_running })}
              />
            </div>

            <div className="form-row">
              <span className="form-label">开机自启</span>
              <button
                className={`toggle ${cfgDraft.auto_start ? 'on' : ''}`}
                onClick={() => applyCfg({ ...cfgDraft, auto_start: !cfgDraft.auto_start })}
              />
            </div>

            <div className="form-row">
              <span className="form-label">首选终端</span>
              <select
                className="field field-sm"
                value={cfgDraft.preferred_term}
                onChange={(e) => applyCfg({ ...cfgDraft, preferred_term: e.target.value })}
              >
                {data?.platform === 'darwin' ? (
                  <>
                    <option value="auto">自动</option>
                    <option value="terminal">Terminal</option>
                    <option value="iterm">iTerm2</option>
                  </>
                ) : data?.platform === 'linux' ? (
                  <option value="auto">自动</option>
                ) : (
                  <>
                    <option value="auto">自动</option>
                    <option value="wt">Windows Terminal</option>
                    <option value="pwsh">PowerShell</option>
                    <option value="cmd">CMD</option>
                  </>
                )}
              </select>
            </div>

            <div className="form-row">
              <span className="form-label">默认目录</span>
              <input
                className="field field-md"
                value={cfgDraft.default_work_dir}
                placeholder="默认用户主目录"
                onChange={(e) => setCfgDraft({ ...cfgDraft, default_work_dir: e.target.value })}
                onBlur={() => applyCfg(cfgDraft)}
                onKeyDown={(e) => e.key === 'Enter' && applyCfg(cfgDraft, true)}
              />
            </div>
          </div>
        </main>
      )}
    </div>
  );
}
