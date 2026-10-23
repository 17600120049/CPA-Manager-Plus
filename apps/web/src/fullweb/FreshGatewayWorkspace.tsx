import { useMemo, useState } from 'react';
import {
  GATEWAY_SECTIONS,
  GATEWAY_OWNERS,
  COMMON_GATEWAY_PATHS,
  type GatewayOwnerMeta,
} from './gatewayCatalog';
import {
  FreshRetryConfiguration,
  type RetryDraftDiff,
} from './FreshRetryConfiguration';
import type { createPublicApiClient } from './publicApi';
import { workspaceText as w } from '../i18n/consoleWorkspace';

export type GatewayTabKey =
  | 'overview'
  | 'common'
  | 'routing'
  | 'retry'
  | 'requests'
  | 'media'
  | 'compat'
  | 'owners';

const matchesSearch = (text: string, query: string) => {
  if (!query) return true;
  return text.toLowerCase().includes(query.trim().toLowerCase());
};

export function FreshGatewayWorkspace({
  client,
  credential,
  onUnauthorized,
  onPending,
  navigationRequest = 0,
}: {
  client: ReturnType<typeof createPublicApiClient>;
  credential: string;
  onUnauthorized: () => void;
  onPending: (pending: boolean) => void;
  navigationRequest?: number;
}) {
  const [currentTab, setCurrentTab] = useState<GatewayTabKey>('overview');
  const [searchText, setSearchText] = useState('');
  const [changedOnly, setChangedOnly] = useState(false);
  const [activeOwner, setActiveOwner] = useState<GatewayOwnerMeta | null>(null);
  const [diffModalOpen, setDiffModalOpen] = useState(false);
  const [discardModalOpen, setDiscardModalOpen] = useState(false);
  const [discardEpoch, setDiscardEpoch] = useState(0);

  // 共享草稿状态跟踪
  const [draftDiffs, setDraftDiffs] = useState<RetryDraftDiff[]>([]);
  const dirtyDiffs = useMemo(
    () => draftDiffs.filter((d) => d.dirty),
    [draftDiffs]
  );
  const dirtyCount = dirtyDiffs.length;

  const handleDiscard = () => {
    setDiscardEpoch((n) => n + 1);
    setDiscardModalOpen(false);
  };

  const tabs: Array<{ key: GatewayTabKey; label: string }> = [
    { key: 'overview', label: '概览' },
    { key: 'common', label: '常用' },
    { key: 'routing', label: '路由' },
    { key: 'retry', label: '重试与故障处理' },
    { key: 'requests', label: '请求处理' },
    { key: 'media', label: '多媒体' },
    { key: 'compat', label: '兼容性' },
    { key: 'owners', label: '配置归属' },
  ];

  const filteredSections = useMemo(() => {
    return GATEWAY_SECTIONS.filter((section) => {
      if (changedOnly && !dirtyDiffs.some((d) => d.path.startsWith('routing.retry.'))) {
        return false;
      }
      if (searchText) {
        const matchesHeading =
          matchesSearch(section.title, searchText) || matchesSearch(section.description, searchText);
        const matchesField = section.fields.some(
          (f) =>
            matchesSearch(f.label, searchText) ||
            matchesSearch(f.path, searchText) ||
            matchesSearch(f.help, searchText)
        );
        return matchesHeading || matchesField;
      }
      if (currentTab === 'common') {
        return section.fields.some((f) => COMMON_GATEWAY_PATHS.has(f.path));
      }
      return section.tab === currentTab;
    });
  }, [currentTab, searchText, changedOnly, dirtyDiffs]);

  const searchKeyword = searchText.trim().toLowerCase();
  const showRetryEditor =
    (currentTab === 'retry' ||
      currentTab === 'common' ||
      (searchKeyword &&
        (searchKeyword.includes('retry') ||
          searchKeyword.includes('重试') ||
          searchKeyword.includes('cooldown') ||
          searchKeyword.includes('冷却') ||
          searchKeyword.includes('routing.retry.')))) &&
    (!changedOnly || dirtyCount > 0);

  return (
    <div className="fresh-gateway-workspace" data-testid="fresh-gateway-workspace">
      <div className="page-head">
        <div>
          <div className="eyebrow">GATEWAY CONFIGURATION / V8</div>
          <h1 id="gateway-workspace-title" data-testid="gateway-workspace-title" tabIndex={-1}>
            网关管理
          </h1>
          <p className="muted">
            按真实配置域组织，明确每一项的来源、归属与影响。
          </p>
        </div>
        <span className="badge">网关工作区 · r3</span>
      </div>

      {/* 顶部工具条 */}
      <div className="gateway-toolbar" data-testid="gateway-toolbar">
        <label className="search-box">
          <span className="sr-only">搜索字段或配置路径</span>
          <input
            id="field-search"
            data-testid="gateway-search-input"
            type="text"
            placeholder="搜索字段或路径，例如：会话、冷却、streaming…"
            value={searchText}
            onChange={(e) => setSearchText(e.target.value)}
          />
        </label>
        <label className="check-label">
          <input
            id="changed-only"
            data-testid="gateway-changed-only"
            type="checkbox"
            checked={changedOnly}
            onChange={(e) => setChangedOnly(e.target.checked)}
          />
          只看未保存
        </label>
        <div className="gateway-toolbar-status">
          <span
            className={`badge ${dirtyCount > 0 ? 'badge-amber' : 'badge-emerald'}`}
            data-testid="gateway-dirty-count"
          >
            {dirtyCount > 0 ? `${dirtyCount} 项未保存` : '草稿与服务端一致'}
          </span>
          <span className="badge">Embedded · 模拟单控制器</span>
        </div>
        <div className="gateway-toolbar-actions">
          <button
            className="btn btn-secondary btn-sm"
            data-testid="view-changes-btn"
            disabled={dirtyCount === 0}
            onClick={() => setDiffModalOpen(true)}
          >
            查看变更
          </button>
          <button
            className="btn btn-secondary btn-sm"
            data-testid="discard-drafts-btn"
            disabled={dirtyCount === 0}
            onClick={() => setDiscardModalOpen(true)}
          >
            放弃草稿
          </button>
        </div>
      </div>

      {/* Tabs 导航 */}
      <div className="tabs gateway-tabs" role="tablist" aria-label="网关配置域">
        {tabs.map((tab) => (
          <button
            key={tab.key}
            role="tab"
            aria-selected={currentTab === tab.key}
            className={`tab-btn ${currentTab === tab.key ? 'active' : ''}`}
            data-tab={tab.key}
            data-testid={`gateway-tab-${tab.key}`}
            onClick={() => {
              setCurrentTab(tab.key);
              setSearchText('');
            }}
          >
            {tab.label}
            {tab.key === 'retry' && dirtyCount > 0 && (
              <span className="dirty-dot" aria-label="有未保存修改" />
            )}
          </button>
        ))}
      </div>

      {/* 概览视图 */}
      {currentTab === 'overview' && !searchText && (
        <div className="gateway-overview-content" data-testid="gateway-overview-content">
          <div className="summary">
            <section className="card">
              <span className="muted">已映射全局字段</span>
              <strong>21 个配置字段</strong>
            </section>
            <section className="card">
              <span className="muted">独立归属说明</span>
              <strong>{GATEWAY_OWNERS.length} 组</strong>
            </section>
            <section className="card">
              <span className="muted">已开放写操作</span>
              <strong className="text-emerald">重试与冷却等待（2 项）</strong>
            </section>
          </div>

          <div className="notice">
            字段已在 CPA v8 源码及配置合同中定位。当前仅支持保存经严格隔离验证的重试与冷却等待策略；其余字段均为只读或独立归属说明。
          </div>

          <div className="overview-grid">
            {GATEWAY_SECTIONS.map((section) => (
              <button
                key={section.id}
                className="overview-card card"
                data-testid={`open-section-${section.id}`}
                onClick={() => setCurrentTab(section.tab)}
              >
                <span className="eyebrow">{section.fields.length} FIELDS</span>
                <h2>{section.title}</h2>
                <p>{section.description}</p>
                <span className="badge">
                  {section.id === 'retry' && dirtyCount > 0
                    ? `${dirtyCount} 项未保存`
                    : '查看配置 →'}
                </span>
              </button>
            ))}
          </div>

          <h2 className="section-heading" style={{ marginTop: '28px' }}>
            不在这里重复编辑的配置
          </h2>
          <div className="owner-grid">
            {GATEWAY_OWNERS.map((owner, idx) => (
              <section key={idx} className="card owner-card">
                <span className="badge">独立入口 / 只读归属</span>
                <h3>{owner.owner}</h3>
                <code>{owner.path}</code>
                <p className="muted">{owner.description}</p>
                <button
                  className="btn btn-secondary btn-sm"
                  data-testid={`view-owner-${idx}`}
                  onClick={() => setActiveOwner(owner)}
                >
                  查看配置归属
                </button>
              </section>
            ))}
          </div>
        </div>
      )}

      {/* 配置归属 Tab */}
      {currentTab === 'owners' && !searchText && (
        <div className="gateway-owners-content" data-testid="gateway-owners-content">
          <section className="card" style={{ marginBottom: '18px' }}>
            <h2>配置覆盖与归属</h2>
            <p className="muted">
              CPAMP 遵循“每个配置项拥有唯一主编辑入口”的设计原则。网关管理集中负责请求路由、重试与网络策略；各项独立资源由专属模块管理。
            </p>
            <div className="notice">
              原始 YAML 不直接发送到浏览器；未知字段、凭据和插件 opaque 数据具备专有安全脱敏策略。本批不提供全量 Raw YAML 编辑。
            </div>
          </section>

          <div className="owner-grid">
            {GATEWAY_OWNERS.map((owner, idx) => (
              <section key={idx} className="card owner-card">
                <span className="badge">独立入口 / 只读归属</span>
                <h3>{owner.owner}</h3>
                <code>{owner.path}</code>
                <p className="muted">{owner.description}</p>
                <button
                  className="btn btn-secondary btn-sm"
                  data-testid={`owners-view-${idx}`}
                  onClick={() => setActiveOwner(owner)}
                >
                  查看配置归属
                </button>
              </section>
            ))}
          </div>
        </div>
      )}

      {/* 重试与冷却等待编辑器（复用现有组件，保持真实保存、对账与 UNKNOWN 阻断机制） */}
      <div
        className="retained-retry-container"
        data-testid="fresh-managed-retry"
        style={{ display: showRetryEditor ? 'block' : 'none' }}
      >
        <div className="fresh-managed-resource-heading">
          <div>
            <h2>{w('connection')}</h2>
            <p>{w('configurationHelp')}</p>
          </div>
          <span className="badge">{w('stored')}</span>
        </div>
        <FreshRetryConfiguration
          client={client}
          credential={credential}
          onUnauthorized={onUnauthorized}
          onPending={onPending}
          navigationRequest={navigationRequest}
          discardEpoch={discardEpoch}
          onDraftChange={setDraftDiffs}
        />
      </div>

      {/* 其它分类展示（只读项与字段说明） */}
      {currentTab !== 'overview' && currentTab !== 'owners' && (
        <div className="gateway-sections-list stack" style={{ marginTop: '18px' }}>
          {filteredSections.map((section) => (
            <section
              key={section.id}
              className="card v8-section"
              data-section={section.id}
              data-testid={`section-${section.id}`}
            >
              <div className="card-head">
                <div>
                  <div className="eyebrow">{section.fields.length} 个配置字段</div>
                  <h2>{section.title}</h2>
                  <p className="muted">{section.description}</p>
                </div>
                <span className="badge">
                  {section.id === 'retry' ? '部分可编辑' : '只读展示'}
                </span>
              </div>

              {section.id !== 'retry' && (
                <div className="notice warn" style={{ marginBottom: '14px' }}>
                  只读展示：该配置域在此部署环境尚待后续获准用户操作接入，当前仅供只读参考，不做无效保存。
                </div>
              )}

              {section.fields
                .filter((f) => {
                  // 在 retry 分组中，可写的两个字段已由 FreshRetryConfiguration 承接
                  if (section.id === 'retry' && (f.path === 'routing.retry.request-retry' || f.path === 'routing.retry.max-retry-interval')) {
                    return false;
                  }
                  if (currentTab === 'common') {
                    return COMMON_GATEWAY_PATHS.has(f.path) && f.path !== 'routing.retry.request-retry' && f.path !== 'routing.retry.max-retry-interval';
                  }
                  if (searchText) {
                    return (
                      matchesSearch(f.label, searchText) ||
                      matchesSearch(f.path, searchText) ||
                      matchesSearch(f.help, searchText)
                    );
                  }
                  return true;
                })
                .map((field) => (
                  <div key={field.path} className="config-field">
                    <div>
                      <label htmlFor={`field-${field.path}`}>
                        {field.label}
                        {field.risk && (
                          <span className="badge badge-amber" style={{ marginLeft: '8px' }}>
                            需确认影响
                          </span>
                        )}
                      </label>
                      <p>{field.help}</p>
                      <code>{field.path}</code>
                      <small>来源：演示配置快照 · 待后续获准操作开放</small>
                    </div>
                    <div className="field-control">
                      <input
                        id={`field-${field.path}`}
                        type="text"
                        disabled
                        value={String(field.value)}
                        readOnly
                      />
                    </div>
                  </div>
                ))}
            </section>
          ))}
        </div>
      )}

      {/* 查看变更模态框 */}
      {diffModalOpen && (
        <div
          className="modal-backdrop"
          role="presentation"
          onClick={() => setDiffModalOpen(false)}
        >
          <div
            className="card modal-content"
            role="dialog"
            aria-modal="true"
            aria-labelledby="diff-modal-title"
            onClick={(e) => e.stopPropagation()}
            data-testid="diff-modal"
          >
            <div className="card-head">
              <h2 id="diff-modal-title">未保存变更对比</h2>
              <button
                className="btn btn-secondary btn-icon"
                onClick={() => setDiffModalOpen(false)}
                aria-label="关闭"
              >
                ×
              </button>
            </div>
            <p className="muted">
              当前草稿与服务端基线的差异。未改动字段不被提交；每个修改字段均作为独立 operation 逐项处理。
            </p>
            <div className="diff-list">
              {dirtyDiffs.map((diff) => (
                <div key={diff.path} className="diff-item">
                  <div className="diff-header">
                    <strong>{diff.label}</strong>
                    <code>{diff.path}</code>
                  </div>
                  <div className="diff-values">
                    <span className="diff-old">{diff.oldVal}</span>
                    <span className="diff-arrow">→</span>
                    <span className="diff-new">{diff.newVal}</span>
                  </div>
                </div>
              ))}
            </div>
            <div className="notice" style={{ marginTop: '14px' }}>
              注意：本批保存复用两个独立 Retry writer，逐项等待持久化结果，不伪装原子事务。遇 UNKNOWN 将停止后续写入。
            </div>
            <div className="wizard-card-footer" style={{ marginTop: '18px' }}>
              <button
                className="btn btn-primary"
                onClick={() => setDiffModalOpen(false)}
              >
                返回编辑
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 放弃草稿确认模态框 */}
      {discardModalOpen && (
        <div
          className="modal-backdrop"
          role="presentation"
          onClick={() => setDiscardModalOpen(false)}
        >
          <div
            className="card modal-content"
            role="dialog"
            aria-modal="true"
            aria-labelledby="discard-modal-title"
            onClick={(e) => e.stopPropagation()}
            data-testid="discard-modal"
          >
            <div className="card-head">
              <h2 id="discard-modal-title">放弃未保存草稿</h2>
              <button
                className="btn btn-secondary btn-icon"
                onClick={() => setDiscardModalOpen(false)}
                aria-label="关闭"
              >
                ×
              </button>
            </div>
            <p>
              你当前有 {dirtyCount} 项未保存修改。放弃后将重置为服务端当前已保存的值。已提交的操作不会受影响。
            </p>
            <div className="wizard-card-footer" style={{ marginTop: '18px' }}>
              <button
                className="btn btn-secondary"
                onClick={() => setDiscardModalOpen(false)}
              >
                继续编辑
              </button>
              <button
                className="btn btn-danger"
                data-testid="confirm-discard-btn"
                onClick={handleDiscard}
              >
                确认放弃并重置
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 归属详情模态框 */}
      {activeOwner && (
        <div
          className="modal-backdrop"
          role="presentation"
          onClick={() => setActiveOwner(null)}
        >
          <div
            className="card modal-content"
            role="dialog"
            aria-modal="true"
            aria-labelledby="owner-modal-title"
            onClick={(e) => e.stopPropagation()}
            data-testid="owner-detail-modal"
          >
            <div className="card-head">
              <h2 id="owner-modal-title">{activeOwner.owner}</h2>
              <button
                className="btn btn-secondary btn-icon"
                onClick={() => setActiveOwner(null)}
                aria-label="关闭"
              >
                ×
              </button>
            </div>
            <p>{activeOwner.description}</p>
            <div className="info-card" style={{ marginTop: '14px' }}>
              <span className="info-key">配置路径范围</span>
              <code className="info-val">{activeOwner.path}</code>
            </div>
            <div className="notice" style={{ marginTop: '14px' }}>
              此配置项归属于对应的主功能页面管理。如需调整，请前往对应的专属工作区，本页面不提供重复修改入口。
            </div>
            <div className="wizard-card-footer" style={{ marginTop: '18px' }}>
              <button
                className="btn btn-primary"
                onClick={() => setActiveOwner(null)}
              >
                关闭
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
