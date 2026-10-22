import { useCallback, useEffect, useMemo, useState, type FormEvent, type ReactNode } from 'react';
import { fullWebLanguage } from '../i18n/fullweb';
import { FullWebIcon } from './FullWebIcon';
import { PublicApiError, type createPublicApiClient } from './publicApi';
import './fullwebResource.css';

export type FullWebResource =
  | 'dashboard'
  | 'config'
  | 'usage'
  | 'monitoring'
  | 'model-prices'
  | 'system'
  | 'accounts'
  | 'oauth'
  | 'ai-providers'
  | 'models'
  | 'health'
  | 'policies'
  | 'prices'
  | 'runtime'
  | 'maintenance'
  | 'updates'
  | 'plugins'
  | 'logs';

type Auth = { kind: 'admin'; value: string };

type DashboardSummary = {
  today?: {
    total_calls?: number;
    success_calls?: number;
    failure_calls?: number;
    success_rate?: number;
    total_tokens?: number;
    total_cost?: number;
  };
  rolling_30m?: { rpm?: number; tpm?: number };
  top_models_today?: Array<{ model?: string; calls?: number; tokens?: number; cost?: number }>;
};

type ManagerConfigResponse = {
  config?: {
    cpaConnection?: { cpaBaseUrl?: string; managementKeyConfigured?: boolean };
    collector?: Record<string, unknown>;
    codexInspection?: Record<string, unknown>;
    externalUsageService?: Record<string, unknown>;
    updatedAtMs?: number;
  };
  source?: string;
  cpaUsage?: unknown;
};

type StatusPayload = {
  service?: string;
  events?: number;
  deadLetters?: number;
  collector?: { collector?: string; mode?: string; upstream?: string; lastError?: string };
  databaseMaintenance?: { status?: string };
};

const language = () => fullWebLanguage();
const zh = () => language().startsWith('zh');
const text = {
  dashboard: () => (zh() ? '仪表盘' : language() === 'ru' ? 'Панель' : 'Dashboard'),
  dashboardHelp: () =>
    zh() ? '沿用 CPAMP 的真实用量与请求摘要。' : 'Live CPAMP usage and request summary.',
  config: () => (zh() ? '配置管理' : language() === 'ru' ? 'Конфигурация' : 'Configuration'),
  configHelp: () =>
    zh() ? '查看并保存 Manager 的 CPA 连接与采集设置。' : 'Review and save Manager connection settings.',
  system: () => (zh() ? '系统信息' : language() === 'ru' ? 'Система' : 'System'),
  systemHelp: () =>
    zh() ? '查看当前 Manager 数据与维护状态。' : 'Inspect the current Manager data and maintenance status.',
  unavailable: () =>
    zh()
      ? '此页面已纳入统一导航，但当前 Fresh 部署尚未开放对应的真实资源。不会显示模拟数据。'
      : 'This page is in the shared navigation, but this Fresh deployment has not exposed its real resource yet. No simulated data is shown.',
  loading: () => (zh() ? '正在读取…' : 'Loading…'),
  retry: () => (zh() ? '重新读取' : 'Retry'),
  error: () => (zh() ? '真实资源读取失败' : 'The real resource could not be read'),
  todayCalls: () => (zh() ? '今日请求' : 'Today calls'),
  successRate: () => (zh() ? '成功率' : 'Success rate'),
  tokens: () => (zh() ? '今日 Token' : 'Today tokens'),
  cost: () => (zh() ? '今日成本' : 'Today cost'),
  rolling: () => (zh() ? '近 30 分钟' : 'Last 30 minutes'),
  topModels: () => (zh() ? '模型用量排行' : 'Top models'),
  model: () => (zh() ? '模型' : 'Model'),
  calls: () => (zh() ? '请求数' : 'Calls'),
  save: () => (zh() ? '保存配置' : 'Save configuration'),
  saving: () => (zh() ? '保存中…' : 'Saving…'),
  saved: () => (zh() ? '配置已保存' : 'Configuration saved'),
  cpaBase: () => (zh() ? 'CPA 地址' : 'CPA base URL'),
  cpaKey: () => (zh() ? 'CPA Management Key' : 'CPA Management Key'),
  cpaKeyHint: () =>
    zh() ? '仅在需要更新连接时填写；已保存密钥不会回显。' : 'Fill only when changing the connection; saved keys are never echoed.',
  source: () => (zh() ? '配置来源' : 'Configuration source'),
  collector: () => (zh() ? '请求采集' : 'Request monitoring'),
  enabled: () => (zh() ? '启用采集' : 'Enable monitoring'),
  events: () => (zh() ? '事件数' : 'Events'),
  deadLetters: () => (zh() ? '死信' : 'Dead letters'),
  status: () => (zh() ? '运行状态' : 'Runtime status'),
  maintenance: () => (zh() ? '数据库维护' : 'Database maintenance'),
  resourceName: (resource: FullWebResource) => {
    const names: Record<FullWebResource, string> = {
      dashboard: zh() ? '仪表盘' : 'Dashboard',
      config: zh() ? '配置管理' : 'Configuration management',
      usage: zh() ? '用量分析' : 'Usage analytics',
      monitoring: zh() ? '请求监控' : 'Monitoring',
      'model-prices': zh() ? '模型价格' : 'Model prices',
      system: zh() ? '运行状态' : 'Runtime status',
      accounts: zh() ? '账户与凭据' : 'Accounts',
      oauth: 'OAuth',
      'ai-providers': zh() ? 'AI 提供商' : 'AI providers',
      models: zh() ? '模型管理' : 'Model management',
      health: zh() ? '凭证健康' : 'Credential health',
      policies: zh() ? '访问策略' : 'Access policies',
      prices: zh() ? '模型价格' : 'Model prices',
      runtime: zh() ? '运行状态' : 'Runtime status',
      maintenance: zh() ? '用量维护' : 'Usage maintenance',
      updates: zh() ? '版本更新' : 'Version updates',
      plugins: zh() ? '插件管理' : 'Plugins',
      logs: zh() ? '系统日志' : 'System logs',
    };
    return names[resource];
  },
  resourceUnavailableHelp: () =>
    zh()
      ? '该入口保留在统一导航中，但当前部署尚未提供对应的真实资源。请使用已交付页面；不会生成模拟数据或模拟成功。'
      : 'This entry remains in the shared navigation, but the current deployment has not exposed the real resource. Use a delivered page; no simulated data or success is generated.',
};

function formatNumber(value: unknown): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '—';
  return new Intl.NumberFormat(language().startsWith('zh') ? 'zh-CN' : language()).format(value);
}

function formatPercent(value: unknown): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '—';
  return `${(value * 100).toFixed(1)}%`;
}

function ErrorPanel({ error, onRetry }: { error: string; onRetry: () => void }) {
  return (
    <section className="fullweb-resource-card fullweb-resource-error" role="alert">
      <FullWebIcon name="alert" />
      <div>
        <strong>{text.error()}</strong>
        <p>{error}</p>
        <button className="btn btn-secondary" type="button" onClick={onRetry}>
          {text.retry()}
        </button>
      </div>
    </section>
  );
}

function DashboardResource({ client, auth, onUnauthorized }: { client: ReturnType<typeof createPublicApiClient>; auth: Auth; onUnauthorized: () => void }) {
  const [data, setData] = useState<DashboardSummary | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const load = useCallback(() => {
    setLoading(true);
    setError('');
    void client
      .get<DashboardSummary>('cpamp/dashboard', auth)
      .then(setData)
      .catch((reason) => {
        if (reason instanceof PublicApiError && reason.kind === 'authentication') onUnauthorized();
        setError(reason instanceof Error ? reason.message : String(reason));
      })
      .finally(() => setLoading(false));
  }, [client, auth, onUnauthorized]);
  useEffect(() => {
    const timer = setTimeout(load, 0);
    return () => clearTimeout(timer);
  }, [load]);
  if (loading && !data) return <ResourceLoading />;
  if (error && !data) return <ErrorPanel error={error} onRetry={load} />;
  const today = data?.today;
  return (
    <div className="fullweb-resource-stack">
      {error && <ErrorPanel error={error} onRetry={load} />}
      <section className="fullweb-resource-intro">
        <div>
          <span className="fullweb-resource-eyebrow">CPAMP</span>
          <h2>{text.dashboard()}</h2>
          <p>{text.dashboardHelp()}</p>
        </div>
        <FullWebIcon name="dashboard" />
      </section>
      <div className="fullweb-resource-metrics">
        <Metric label={text.todayCalls()} value={formatNumber(today?.total_calls)} />
        <Metric label={text.successRate()} value={formatPercent(today?.success_rate)} />
        <Metric label={text.tokens()} value={formatNumber(today?.total_tokens)} />
        <Metric label={text.cost()} value={typeof today?.total_cost === 'number' ? `$${today.total_cost.toFixed(4)}` : '—'} />
      </div>
      <section className="fullweb-resource-card">
        <div className="fullweb-resource-card-heading">
          <h3>{text.rolling()}</h3>
          <span>{formatNumber(data?.rolling_30m?.rpm)} RPM · {formatNumber(data?.rolling_30m?.tpm)} TPM</span>
        </div>
        <div className="fullweb-resource-table-wrap">
          <table className="fullweb-resource-table">
            <thead><tr><th>{text.model()}</th><th>{text.calls()}</th><th>Token</th><th>Cost</th></tr></thead>
            <tbody>
              {(data?.top_models_today || []).map((item) => (
                <tr key={item.model || 'unknown'}><td>{item.model || '—'}</td><td>{formatNumber(item.calls)}</td><td>{formatNumber(item.tokens)}</td><td>{typeof item.cost === 'number' ? `$${item.cost.toFixed(4)}` : '—'}</td></tr>
              ))}
              {!data?.top_models_today?.length && <tr><td colSpan={4} className="fullweb-resource-empty">{zh() ? '暂无请求数据' : 'No request data yet'}</td></tr>}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}

function Metric({ label, value }: { label: string; value: string }) {
  return <section className="fullweb-resource-card fullweb-resource-metric"><span>{label}</span><strong>{value}</strong></section>;
}

function ConfigResource({ client, auth, onUnauthorized }: { client: ReturnType<typeof createPublicApiClient>; auth: Auth; onUnauthorized: () => void }) {
  const [response, setResponse] = useState<ManagerConfigResponse | null>(null);
  const [baseUrl, setBaseUrl] = useState('');
  const [managementKey, setManagementKey] = useState('');
  const [monitoring, setMonitoring] = useState(true);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const load = useCallback(() => {
    setLoading(true);
    setError('');
    void client.get<ManagerConfigResponse>('cpamp/config', auth).then((next) => {
      setResponse(next);
      setBaseUrl(next.config?.cpaConnection?.cpaBaseUrl || '');
      setMonitoring(next.config?.collector?.enabled !== false);
    }).catch((reason) => {
      if (reason instanceof PublicApiError && reason.kind === 'authentication') onUnauthorized();
      setError(reason instanceof Error ? reason.message : String(reason));
    }).finally(() => setLoading(false));
  }, [client, auth, onUnauthorized]);
  useEffect(() => {
    const timer = setTimeout(load, 0);
    return () => clearTimeout(timer);
  }, [load]);
  async function save(event: FormEvent) {
    event.preventDefault();
    setSaving(true); setError(''); setNotice('');
    const current = response?.config || {};
    try {
      const next = await client.put<ManagerConfigResponse>('cpamp/config', {
        config: {
          cpaConnection: { cpaBaseUrl: baseUrl, ...(managementKey.trim() ? { managementKey: managementKey.trim() } : {}) },
          collector: { ...(current.collector || {}), enabled: monitoring },
          codexInspection: current.codexInspection || {},
          externalUsageService: current.externalUsageService || { enabled: false },
        },
      }, auth);
      setResponse(next); setManagementKey(''); setNotice(text.saved());
    } catch (reason) {
      if (reason instanceof PublicApiError && reason.kind === 'authentication') { onUnauthorized(); setError('Authentication required'); }
      else setError(reason instanceof Error ? reason.message : String(reason));
    } finally { setSaving(false); }
  }
  if (loading && !response) return <ResourceLoading />;
  return (
    <div className="fullweb-resource-stack">
      {error && <ErrorPanel error={error} onRetry={load} />}
      {notice && <p className="alert alert-success" role="status">{notice}</p>}
      <section className="fullweb-resource-intro"><div><span className="fullweb-resource-eyebrow">CPAMP</span><h2>{text.config()}</h2><p>{text.configHelp()}</p></div><FullWebIcon name="settings" /></section>
      <form className="fullweb-resource-card fullweb-resource-form" onSubmit={(event) => void save(event)}>
        <label className="form-label" htmlFor="fullweb-cpa-base">{text.cpaBase()}</label>
        <input id="fullweb-cpa-base" className="form-input" value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)} placeholder="http://cpa:8317" />
        <label className="form-label" htmlFor="fullweb-cpa-key">{text.cpaKey()}</label>
        <input id="fullweb-cpa-key" className="form-input" type="password" value={managementKey} onChange={(event) => setManagementKey(event.target.value)} placeholder={response?.config?.cpaConnection?.managementKeyConfigured ? '••••••••' : ''} autoComplete="new-password" />
        <p className="fullweb-resource-hint">{text.cpaKeyHint()}</p>
        <label className="fullweb-resource-check"><input type="checkbox" checked={monitoring} onChange={(event) => setMonitoring(event.target.checked)} /> {text.enabled()}</label>
        <div className="fullweb-resource-form-footer"><span>{text.source()}: {response?.source || '—'}</span><button className="btn btn-primary" type="submit" disabled={saving}>{saving ? text.saving() : text.save()}</button></div>
      </form>
    </div>
  );
}

function StatusResource({ client, auth, onUnauthorized }: { client: ReturnType<typeof createPublicApiClient>; auth: Auth; onUnauthorized: () => void }) {
  const [data, setData] = useState<StatusPayload | null>(null);
  const [error, setError] = useState('');
  const load = useCallback(() => { setError(''); void client.get<StatusPayload>('cpamp/status', auth).then(setData).catch((reason) => { if (reason instanceof PublicApiError && reason.kind === 'authentication') onUnauthorized(); setError(reason instanceof Error ? reason.message : String(reason)); }); }, [client, auth, onUnauthorized]);
  useEffect(() => {
    const timer = setTimeout(load, 0);
    return () => clearTimeout(timer);
  }, [load]);
  if (!data && !error) return <ResourceLoading />;
  if (error && !data) return <ErrorPanel error={error} onRetry={load} />;
  return <div className="fullweb-resource-stack"><section className="fullweb-resource-intro"><div><span className="fullweb-resource-eyebrow">CPAMP</span><h2>{text.system()}</h2><p>{text.systemHelp()}</p></div><FullWebIcon name="settings" /></section><div className="fullweb-resource-metrics"><Metric label={text.events()} value={formatNumber(data?.events)} /><Metric label={text.deadLetters()} value={formatNumber(data?.deadLetters)} /><Metric label={text.status()} value={data?.collector?.collector || '—'} /><Metric label={text.maintenance()} value={data?.databaseMaintenance?.status || '—'} /></div><section className="fullweb-resource-card"><dl className="fullweb-resource-facts"><div><dt>Service</dt><dd>{data?.service || '—'}</dd></div><div><dt>Collector mode</dt><dd>{data?.collector?.mode || '—'}</dd></div><div><dt>Upstream</dt><dd>{data?.collector?.upstream || '—'}</dd></div></dl></section></div>;
}

function ResourceLoading() { return <section className="fullweb-resource-card fullweb-resource-loading" role="status">{text.loading()}</section>; }

function UnavailableResource({ resource }: { resource: FullWebResource }) {
  return (
    <section className="fullweb-resource-card fullweb-resource-unavailable">
      <FullWebIcon name="info" />
      <div>
        <span className="fullweb-resource-eyebrow">CPAMP</span>
        <h2>{text.resourceName(resource)}</h2>
        <p>{text.resourceUnavailableHelp()}</p>
      </div>
    </section>
  );
}

export function FullWebResourcePage({ resource, client, credential, onUnauthorized }: { resource: FullWebResource; client: ReturnType<typeof createPublicApiClient>; credential: string; onUnauthorized: () => void }) {
  const auth = useMemo(() => ({ kind: 'admin' as const, value: credential }), [credential]);
  const guarded = (element: ReactNode) => <ResourceBoundary>{element}</ResourceBoundary>;
  switch (resource) {
    case 'dashboard': return guarded(<DashboardResource client={client} auth={auth} onUnauthorized={onUnauthorized} />);
    case 'config': return guarded(<ConfigResource client={client} auth={auth} onUnauthorized={onUnauthorized} />);
    case 'system': return guarded(<StatusResource client={client} auth={auth} onUnauthorized={onUnauthorized} />);
    default: return <UnavailableResource resource={resource} />;
  }
}

function ResourceBoundary({ children }: { children: ReactNode }) {
  return children;
}
