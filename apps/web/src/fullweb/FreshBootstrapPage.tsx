import React, { useCallback, useEffect, useRef, useState } from 'react';
import { fullWebText as t } from '../i18n/fullweb';
import { validControlBasePath } from './routingContext';
import {
  BootstrapTransportClient,
  BootstrapTransportError,
  FreshOperation,
  Handoff,
  validateFreshAdminKey,
} from './bootstrapTransport';

export interface FreshBootstrapPageProps {
  handoff: Handoff | null;
  transport: BootstrapTransportClient;
}

type BootPhase = 'no_handoff' | 'connecting' | 'ready' | 'init_error';

interface ActionFeedback {
  tone: 'danger' | 'warn' | 'info' | 'success';
  title: string;
  detail: string;
}

// Public discovery never confers write authority. Non-Fresh statuses only
// provide a Host-side preflight next step; they cannot enter the Fresh claim.
type DiscoveryGuidance = ActionFeedback & { flow: 'fresh' | 'host' };
function publicDiscoveryGuidance(payload: unknown): DiscoveryGuidance | null {
  if (!payload || typeof payload !== 'object') return null;
  const value = payload as Record<string, unknown>;
  if (value.contract_version !== 'cpamp.phase4-01.discovery.v1') return null;
  if (
    value.status === 'fresh_claim_required' &&
    value.mode === 'fresh' &&
    value.auth_entry === 'fresh_claim' &&
    value.reason === 'fresh_claim_required' &&
    value.authentication_required === true
  ) {
    return {
      tone: 'info',
      flow: 'fresh',
      title: '检测到需要宿主机认领的 Fresh 初始化状态',
      detail:
        '此状态仅为只读识别结果。请从受信宿主机获取一次性 Handoff 链接；本页面不会自行创建或认领安装。',
    };
  }
  if (
    value.status === 'unsupported_upgrade' &&
    value.mode === 'unsupported_upgrade' &&
    value.auth_entry === 'admin_session' &&
    value.reason === 'qualification_unavailable' &&
    value.authentication_required === true
  ) {
    return {
      tone: 'warn',
      flow: 'host',
      title: '检测到尚未获得升级资格的现有来源',
      detail:
        '只能在受信宿主机检查原始来源。当前无法进入 Fresh 初始化或 Upgrade 切换；预检成功也不代表获得导入或覆盖授权。',
    };
  }
  if (
    value.status === 'recovery_required' &&
    value.mode === 'recovery' &&
    value.auth_entry === 'host_intervention' &&
    value.reason === 'recovery_host_intervention' &&
    value.authentication_required === false
  ) {
    return {
      tone: 'warn',
      flow: 'host',
      title: '检测到需要宿主机介入的恢复状态',
      detail:
        '来源不具备直接初始化资格。请先在受信宿主机进行只读预检，保护数据库、data.key 和已有备份；不得从此页面尝试 Fresh/Adopt/Upgrade 写入。',
    };
  }
  if (
    value.status === 'unavailable' &&
    (value.mode === undefined || value.mode === null) &&
    value.auth_entry === 'unavailable' &&
    value.reason === 'observation_unavailable' &&
    value.authentication_required === false
  ) {
    return {
      tone: 'warn',
      flow: 'host',
      title: '来源状态暂无法确认',
      detail:
        '来源只读检查未获得可信分类。请在受信宿主机排查；不能根据此状态认定为 Fresh，也不能据此执行初始化或迁移。',
    };
  }
  return null;
}

let commitSeq = 0;

function createCommitId(): string {
  commitSeq += 1;
  if (typeof globalThis.crypto?.randomUUID === 'function') {
    return `commit_${globalThis.crypto.randomUUID().replace(/-/g, '')}`;
  }
  return `commit_${Date.now().toString(36)}_${commitSeq.toString(36)}`;
}

function describeBootstrapError(
  err: unknown,
  stage: 'start' | 'generate' | 'commit' | 'refresh'
): ActionFeedback {
  if (err instanceof BootstrapTransportError) {
    if (err.kind === 'transport') {
      return {
        tone: 'warn',
        title: '网络连接异常（Bootstrap Transport Unavailable）',
        detail:
          stage === 'commit'
            ? '提交结果尚未确认，当前密钥与幂等提交标识（commitId）已保留在内存中。请检查网络或服务状态后点击“同步服务端状态”；确认服务端仍未初始化后才可重试。请勿刷新页面或更换密钥。'
            : stage === 'start'
              ? '无法连接至 Manager Bootstrap 服务端。您可尝试重新连接一次；若服务端已消费本次一次性 Claim Token 并返回 HTTP 401，请在宿主机重新执行 cpa-manager-plus initialize 获取新的 Handoff 链接。'
              : '无法连接至 Manager Bootstrap 服务端，请确认本地受控容器或服务进程正在运行后重试。',
      };
    }
    if (err.kind === 'rejected' || err.kind === 'authority') {
      return {
        tone: 'danger',
        title: 'Bootstrap 授权已失效或被拒绝（HTTP 401）',
        detail:
          stage === 'start'
            ? '一次性认领令牌（Claim Token）已被消费、过期，或被更新的初始化代际（Claim Generation）取代。请在宿主机重新执行 cpa-manager-plus initialize 获取最新 Handoff 链接。'
            : '当前 Bootstrap 会话授权已失效，或该操作在当前阶段已关闭。请点击“同步服务端状态”检查最新进度，或在宿主机重新执行初始化命令。',
      };
    }
    if (err.kind === 'mismatch') {
      return {
        tone: 'danger',
        title: '安装实例身份不匹配（Bootstrap Authority Mismatch）',
        detail:
          '服务端返回的 FreshOperation 身份（admissionId / installationId / generation / operationId）与 Handoff 凭证不一致，已按 Fail-Closed 策略中止操作。',
      };
    }
    if (err.kind === 'bad_request') {
      return {
        tone: 'danger',
        title: '请求参数未通过服务端校验（HTTP 400）',
        detail:
          '管理员密钥格式、字节长度或字符复杂度不符合服务端安全要求，请检查密钥规则或使用“一键生成”重新生成。',
      };
    }
    if (err.kind === 'conflict') {
      return {
        tone: 'warn',
        title: '操作状态冲突（HTTP 409）',
        detail:
          '服务端 FreshOperation 状态已发生变更（例如管理员已完成初始化或正在使用不同幂等标识提交）。请点击“同步服务端状态”获取最新状态。',
      };
    }
    return {
      tone: 'danger',
      title: `服务端暂时不可用${err.status ? `（HTTP ${err.status}）` : ''}`,
      detail: 'Bootstrap 服务端未能完成当前请求，请稍后重试或同步服务端状态。',
    };
  }
  return {
    tone: 'danger',
    title: '操作未能完成',
    detail: '发生未预期的客户端异常，请刷新服务端状态或重新获取 Handoff 链接。',
  };
}

export function FreshBootstrapPage({ handoff, transport }: FreshBootstrapPageProps) {
  const [lightTheme, setLightTheme] = useState(false);
  const [phase, setPhase] = useState<BootPhase>(handoff ? 'connecting' : 'no_handoff');
  const [operation, setOperation] = useState<FreshOperation | null>(null);
  const [initError, setInitError] = useState<ActionFeedback | null>(null);
  const [initErrorKind, setInitErrorKind] = useState<string | null>(null);
  const [discoveryNote, setDiscoveryNote] = useState<DiscoveryGuidance | null>(null);
  const [discoveryChecked, setDiscoveryChecked] = useState(false);
  const [discoveryAttempt, setDiscoveryAttempt] = useState(0);

  const [adminKey, setAdminKey] = useState('');
  const [commitId, setCommitId] = useState<string>(() => createCommitId());
  const [showKey, setShowKey] = useState(false);
  const [confirmedSaved, setConfirmedSaved] = useState(false);

  const [generating, setGenerating] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [commitUnresolved, setCommitUnresolved] = useState(false);
  const [candidateSubmitted, setCandidateSubmitted] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [feedback, setFeedback] = useState<ActionFeedback | null>(null);
  const [commitCompletedInSession, setCommitCompletedInSession] = useState(false);
  const [routeTarget, setRouteTarget] = useState('/console');
  const [attemptedTarget, setAttemptedTarget] = useState('');
  const [routingBusy, setRoutingBusy] = useState(false);
  const [verifiedRoute, setVerifiedRoute] = useState('');
  const [routingError, setRoutingError] = useState('');

  const keyInputRef = useRef<HTMLInputElement | null>(null);
  const startedRef = useRef(false);

  const preparedTarget = operation?.routing.target || attemptedTarget;

  async function handleEnableConsole(event: React.FormEvent) {
    event.preventDefault();
    if (routingBusy || refreshing || !operation?.adminInitialized) return;
    const target = preparedTarget || routeTarget;
    if (!validControlBasePath(target)) {
      setRoutingError(t('invalidPath'));
      return;
    }
    setRoutingBusy(true);
    setRoutingError('');
    setAttemptedTarget(target);
    try {
      // Always reconcile the original intent; never create a new route owner.
      if (!operation.routing.target) {
        const next = await transport.prepareRouting(target);
        setOperation(next);
      }
      setVerifiedRoute(await transport.activateRouting());
      transport.clear();
    } catch (err) {
      const kind = err instanceof BootstrapTransportError ? err.kind : 'unknown';
      setRoutingError(
        t(
          kind === 'conflict'
            ? 'routeConflict'
            : kind === 'unavailable'
              ? 'routeUnavailable'
              : kind === 'rejected' || kind === 'authority'
                ? 'routeRejected'
                : kind === 'mismatch'
                  ? 'routeMismatch'
                  : 'routeFailure'
        )
      );
      // Prepare may have persisted before the connection was lost. Failure to
      // read it keeps the attempted target locked rather than inventing state.
      try {
        const current = await transport.fetchOperation();
        setOperation(current);
        if (!current.routing.target) setAttemptedTarget('');
      } catch {
        /* Retry stays on the original intent. */
      }
    } finally {
      setRoutingBusy(false);
    }
  }

  useEffect(() => {
    document.documentElement.classList.toggle('light', lightTheme);
  }, [lightTheme]);

  const executeStart = useCallback(
    async (targetHandoff: Handoff) => {
      setPhase('connecting');
      setInitError(null);
      setInitErrorKind(null);
      try {
        const op = await transport.start(targetHandoff);
        setOperation(op);
        setPhase('ready');
        if (op.adminInitialized) {
          setAdminKey('');
        }
      } catch (err) {
        const kind = err instanceof BootstrapTransportError ? err.kind : 'unknown';
        setInitErrorKind(kind);
        setInitError(describeBootstrapError(err, 'start'));
        setPhase('init_error');
      }
    },
    [transport]
  );

  useEffect(() => {
    if (handoff) return;
    let active = true;
    void fetch('/.well-known/cpamp-bootstrap', {
      method: 'GET',
      mode: 'same-origin',
      credentials: 'omit',
      redirect: 'error',
      cache: 'no-store',
      headers: { Accept: 'application/json' },
    })
      .then(async (response) => {
        if (response.status !== 200 && response.status !== 503) return null;
        const payload: unknown = await response.json();
        if (!payload || typeof payload !== 'object') return null;
        // Public discovery uses 200 for classified sources and 503 only for
        // unavailable. A mismatched HTTP status must never suggest Fresh setup.
        const unavailable = (payload as Record<string, unknown>).status === 'unavailable';
        return unavailable === (response.status === 503) ? payload : null;
      })
      .then((payload: unknown) => {
        if (!active) return;
        setDiscoveryNote(publicDiscoveryGuidance(payload));
        setDiscoveryChecked(true);
      })
      .catch(() => {
        // An unverified source must not receive Fresh setup guidance.
        if (active) setDiscoveryChecked(true);
      });
    return () => {
      active = false;
    };
  }, [handoff, discoveryAttempt]);

  useEffect(() => {
    if (!handoff) {
      setPhase('no_handoff');
      return;
    }
    if (startedRef.current) {
      return;
    }
    startedRef.current = true;
    void executeStart(handoff);
  }, [handoff, executeStart]);

  function handleKeyInputChange(nextValue: string) {
    if (submitting || refreshing || commitUnresolved || candidateSubmitted) return;
    setAdminKey(nextValue);
    setCommitId(createCommitId());
    setConfirmedSaved(false);
    setFeedback(null);
  }

  async function handleGenerateKey() {
    if (
      generating ||
      submitting ||
      refreshing ||
      commitUnresolved ||
      candidateSubmitted ||
      !operation ||
      operation.adminInitialized
    )
      return;
    setGenerating(true);
    setFeedback(null);
    try {
      const generated = await transport.generateAdminKey();
      setAdminKey(generated);
      setCommitId(createCommitId());
      setShowKey(true);
      setConfirmedSaved(false);
      setFeedback({
        tone: 'info',
        title: '已由 Manager 服务端生成高强度管理员密钥',
        detail: '请立即复制并妥善保存该密钥，勾选下方保存确认框后即可提交初始化。',
      });
    } catch (err) {
      setFeedback(describeBootstrapError(err, 'generate'));
    } finally {
      setGenerating(false);
    }
  }

  async function handleCopyKey() {
    if (!adminKey) return;
    try {
      if (!navigator.clipboard?.writeText) {
        throw new Error('Clipboard unavailable');
      }
      await navigator.clipboard.writeText(adminKey);
      setFeedback({
        tone: 'success',
        title: '管理员密钥已复制到剪贴板',
        detail: '请将其存入密码管理器或受控凭据库，确认无误后勾选下方确认框并提交。',
      });
    } catch {
      setShowKey(true);
      setTimeout(() => {
        keyInputRef.current?.focus();
        keyInputRef.current?.select();
      }, 0);
      setFeedback({
        tone: 'warn',
        title: '浏览器剪贴板访问受限，已为您展开并选中密钥',
        detail: '请直接使用键盘快捷键（macOS 按 Cmd+C，Windows/Linux 按 Ctrl+C）手动复制。',
      });
    }
  }

  async function handleRefreshOperation() {
    if (refreshing || submitting || generating || routingBusy || verifiedRoute) return;
    setRefreshing(true);
    setFeedback(null);
    try {
      const next = await transport.fetchOperation();
      setOperation(next);
      setCommitUnresolved(false);
      if (next.adminInitialized) {
        setAdminKey('');
        setShowKey(false);
      }
      setFeedback({
        tone: 'info',
        title: '已同步最新服务端 Operation 状态',
        detail: `当前状态版本 Revision #${next.revision}，管理员初始化状态：${
          next.adminInitialized ? '已完成' : '待初始化'
        }。`,
      });
    } catch (err) {
      setFeedback(describeBootstrapError(err, 'refresh'));
    } finally {
      setRefreshing(false);
    }
  }

  async function handleCommitAdmin(event: React.FormEvent) {
    event.preventDefault();
    if (
      submitting ||
      generating ||
      refreshing ||
      commitUnresolved ||
      !operation ||
      operation.adminInitialized
    )
      return;

    const validation = validateFreshAdminKey(adminKey);
    if (!validation.valid) {
      setFeedback({
        tone: 'danger',
        title: '管理员密钥未满足安全校验要求',
        detail: validation.reason,
      });
      return;
    }
    if (!confirmedSaved) {
      setFeedback({
        tone: 'warn',
        title: '请先确认已妥善保存管理员密钥',
        detail: '勾选确认框后方可提交，提交成功后服务端仅保存不可逆密钥校验值，不保存明文。',
      });
      return;
    }

    setSubmitting(true);
    setCommitUnresolved(true);
    setCandidateSubmitted(true);
    setFeedback(null);
    try {
      const updated = await transport.commitAdminKey(commitId, adminKey);
      setOperation(updated);
      setCommitUnresolved(false);
      setAdminKey('');
      setShowKey(false);
      setCommitCompletedInSession(true);
      setFeedback({
        tone: 'success',
        title: '管理员密钥初始化已提交生效',
        detail: `已通过幂等提交标识 ${commitId} 完成初始化，服务端状态已推进至 Revision #${updated.revision}。`,
      });
    } catch (err) {
      if (err instanceof BootstrapTransportError && err.kind === 'bad_request') {
        setCommitUnresolved(false);
        setCandidateSubmitted(false);
      } else if (
        !(err instanceof BootstrapTransportError) ||
        !['mismatch', 'authority'].includes(err.kind)
      ) {
        try {
          const latest = await transport.fetchOperation();
          setOperation(latest);
          setCommitUnresolved(false);
          if (latest.adminInitialized) {
            setAdminKey('');
            setShowKey(false);
            setFeedback({
              tone: 'info',
              title: '服务端已完成管理员初始化',
              detail: `经同步确认，当前 Operation（Revision #${latest.revision}）已处于管理员已初始化状态。`,
            });
            return;
          }
        } catch {
          // Keep original commit error feedback if status refresh also fails.
        }
      }
      setFeedback(describeBootstrapError(err, 'commit'));
    } finally {
      setSubmitting(false);
    }
  }

  const keyValidation = validateFreshAdminKey(adminKey);
  const strengthScore = !adminKey
    ? 0
    : keyValidation.valid
      ? 4
      : Math.max(
          1,
          (keyValidation.byteLength >= 24 ? 1 : 0) +
            (keyValidation.classCount >= 3 ? 1 : 0) +
            (keyValidation.distinctCount >= 12 ? 1 : 0)
        );

  const isAdminDone = Boolean(operation?.adminInitialized);

  return (
    <div className="bootstrap-shell" data-testid="fresh-bootstrap-shell">
      <header className="bootstrap-header">
        <div className="brand-group">
          <div className="brand-logo" aria-hidden="true">
            C+
          </div>
          <div className="brand-text">
            <span className="brand-title">CPA Manager Plus</span>
            <span className="brand-sub">
              {phase === 'no_handoff' && discoveryNote?.flow !== 'fresh'
                ? 'v2 Source Preflight · 只读来源检查'
                : 'v2 Fresh Bootstrap · 受控安装初始化入口'}
            </span>
          </div>
          <span className="badge badge-indigo">
            {phase === 'no_handoff' && discoveryNote?.flow !== 'fresh'
              ? 'Source Preflight · Read Only'
              : 'Fresh New Preview'}
          </span>
          {operation && (
            <span
              className={`badge ${operation.gatewayPending ? 'badge-amber' : 'badge-emerald'}`}
              data-testid="gateway-status-badge"
            >
              {operation.gatewayPending
                ? 'CPA Gateway: Pending (Fail-Closed)'
                : 'CPA Gateway: Ready'}
            </span>
          )}
        </div>
        <div className="header-actions">
          <span className="badge badge-neutral">无浏览器持久化密钥存储</span>
          <button
            type="button"
            className="btn-icon"
            onClick={() => setLightTheme((prev) => !prev)}
            aria-label="切换明暗主题"
            title="切换明暗主题"
          >
            {lightTheme ? '☾' : '☀'}
          </button>
        </div>
      </header>

      <main className="bootstrap-main">
        <div className="wizard-container">
          {!(phase === 'no_handoff' && discoveryNote?.flow !== 'fresh') && (
            <div className="stepper" aria-label="Fresh Bootstrap 进度">
              <div
                className={`step-item ${
                  phase === 'ready' ? 'completed' : phase === 'connecting' ? 'active' : ''
                }`}
              >
                <span className="step-dot">{phase === 'ready' ? '✓' : '1'}</span>
                <span>Handoff 认领与会话绑定</span>
              </div>
              <div className={`step-line ${phase === 'ready' ? 'completed' : ''}`} />
              <div
                className={`step-item ${
                  phase === 'ready' && !isAdminDone ? 'active' : isAdminDone ? 'completed' : ''
                }`}
              >
                <span className="step-dot">{isAdminDone ? '✓' : '2'}</span>
                <span>管理员密钥初始化</span>
              </div>
              <div className={`step-line ${isAdminDone ? 'completed' : ''}`} />
              <div className={`step-item ${isAdminDone ? 'active' : ''}`}>
                <span className="step-dot">3</span>
                <span>Operation 状态确认（Gateway Fail-Closed）</span>
              </div>
            </div>
          )}

          {phase === 'no_handoff' && (
            <section className="wizard-card" data-testid="bootstrap-no-handoff">
              <div className="wizard-card-header">
                <div className="wizard-step-tag">
                  <span>
                    {discoveryNote?.flow === 'fresh'
                       ? 'Bootstrap Gate · 需要安装主机授权'
                       : 'Source Preflight · 只读来源检查'}
                  </span>
                </div>
                <h1 className="wizard-title">
                  {discoveryNote?.flow === 'host'
                     ? '当前来源需要宿主机预检，不能直接 Fresh 初始化'
                     : discoveryNote?.flow === 'fresh'
                       ? '未检测到有效的 Fresh Bootstrap Handoff 凭证'
                       : discoveryChecked
                         ? '来源状态无法确认，禁止直接初始化'
                         : '正在确认来源状态，禁止直接初始化'}
                </h1>
                <p className="wizard-desc">
                  {discoveryNote?.flow === 'host'
                     ? '公共 Discovery 仅提供来源类型提示，不读取或修改现有数据；后续操作必须在受信宿主机执行。'
                     : discoveryNote?.flow === 'fresh'
                       ? '一次性认领令牌在进入页面时已从地址栏剥离，且不会写入浏览器本地存储；请从受信宿主机重新获取 Handoff。'
                       : '尚未取得可信的 Fresh 来源分类；请检查宿主机上的来源与服务状态，不能依据当前页面执行初始化或迁移。'}
                </p>
              </div>
              <div className="wizard-card-body">
                {discoveryNote && (
                  <div
                    className={`alert ${discoveryNote.tone === 'warn' ? 'alert-warn' : 'alert-info'}`}
                    data-testid="bootstrap-discovery-guidance"
                    role="status"
                  >
                    <div className="alert-content">
                      <div className="alert-title">{discoveryNote.title}</div>
                      <div className="alert-desc">{discoveryNote.detail}</div>
                    </div>
                  </div>
                )}

                {discoveryNote?.flow === 'host' ? (
                  <div className="alert alert-warn" data-testid="host-preflight-instructions">
                    <div className="alert-content">
                      <div className="alert-title">下一步：在宿主机执行只读来源预检</div>
                      <div className="alert-desc">
                        把路径替换为真实来源数据库路径。此命令不会授权迁移，且不要在来源写入仍运行时声称已停止。
                      </div>
                      <div className="code-block" data-testid="host-preflight-command">
                        cpa-manager-plus legacy-preflight --db /path/to/usage.sqlite --summary
                      </div>
                    </div>
                  </div>
                ) : discoveryNote?.flow === 'fresh' ? (
                  <>
                    <div className="alert alert-warn">
                      <span className="alert-icon" aria-hidden="true">!</span>
                      <div className="alert-content">
                        <div className="alert-title">如何获取或恢复 Bootstrap 授权链接？</div>
                        <div className="alert-desc">
                          请在受信宿主机运行初始化命令，并使用输出的带{' '}
                          <code>#handoff=...</code> 片段的完整 URL 重新打开本页面：
                        </div>
                      </div>
                    </div>
                    <div className="code-block" data-testid="cli-handoff-command">
                      cpa-manager-plus initialize
                    </div>
                  </>
                ) : (
                  <div className="alert alert-warn" data-testid="bootstrap-discovery-unconfirmed" role="status">
                    <div className="alert-content">
                      <div className="alert-title">
                        {discoveryChecked ? '来源状态无法确认' : '正在读取来源状态'}
                      </div>
                      <div className="alert-desc">
                        不提供 Fresh 初始化命令；请在受信宿主机确认来源、日志与服务状态。未知结果不会授予任何写入权限。
                      </div>
                    </div>
                  </div>
                )}
                {discoveryChecked && !discoveryNote && (
                  <button
                    className="btn btn-secondary"
                    type="button"
                    data-testid="bootstrap-recheck-discovery"
                    onClick={() => {
                      // Reissue only the public read-only discovery GET.
                      // An unverified result never grants a claim or write authority.
                      setDiscoveryChecked(false);
                      setDiscoveryNote(null);
                      setDiscoveryAttempt((attempt) => attempt + 1);
                    }}
                  >
                    重新检查来源状态
                  </button>
                )}
              </div>
            </section>
          )}

          {phase === 'connecting' && (
            <section className="wizard-card" data-testid="bootstrap-connecting">
              <div className="wizard-card-header">
                <div className="wizard-step-tag">
                  <span>Step 1 · Bootstrap Authority Exchange</span>
                </div>
                <h1 className="wizard-title">
                  {handoff?.resume
                    ? '正在恢复 Fresh Bootstrap 受控会话…'
                    : '正在验证一次性 Handoff 并建立受控会话…'}
                </h1>
                <p className="wizard-desc">
                  正在与 Manager Bootstrap 后端核验安装身份（Admission / Installation / Generation /
                  Operation）并同步最新初始化状态。
                </p>
              </div>
              <div className="wizard-card-body">
                <div className="info-card">
                  <div className="info-row">
                    <span className="info-key">授权模式</span>
                    <span className="info-val">
                      {handoff?.resume
                        ? 'Resume Session (GET operation)'
                        : 'Claim Exchange (POST claim/exchange)'}
                    </span>
                  </div>
                  {handoff && (
                    <>
                      <div className="info-row">
                        <span className="info-key">Operation ID</span>
                        <span className="info-val">{handoff.identity.operationId}</span>
                      </div>
                      <div className="info-row">
                        <span className="info-key">Installation / Gen</span>
                        <span className="info-val">
                          {handoff.identity.installationId} · {handoff.identity.generation}
                        </span>
                      </div>
                    </>
                  )}
                </div>
              </div>
            </section>
          )}

          {phase === 'init_error' && initError && (
            <section className="wizard-card" data-testid="bootstrap-authority-error">
              <div className="wizard-card-header">
                <div className="wizard-step-tag">
                  <span>Bootstrap Authority Failed</span>
                </div>
                <h1 className="wizard-title">无法建立 Fresh Bootstrap 受控会话</h1>
                <p className="wizard-desc">
                  客户端已完成地址栏敏感片段清理，但与 Manager Bootstrap
                  后端交换或恢复授权时未通过验证。
                </p>
              </div>
              <div className="wizard-card-body">
                <div
                  className={`alert ${initError.tone === 'warn' ? 'alert-warn' : 'alert-danger'}`}
                  role="alert"
                >
                  <span className="alert-icon" aria-hidden="true">
                    ✕
                  </span>
                  <div className="alert-content">
                    <div className="alert-title" data-testid="init-error-title">
                      {initError.title}
                    </div>
                    <div className="alert-desc" data-testid="init-error-detail">
                      {initError.detail}
                    </div>
                  </div>
                </div>

                {handoff && (
                  <div className="info-card">
                    <div className="info-row">
                      <span className="info-key">Operation ID</span>
                      <span className="info-val">{handoff.identity.operationId}</span>
                    </div>
                    <div className="info-row">
                      <span className="info-key">Claim Generation</span>
                      <span className="info-val">#{handoff.claimGeneration}</span>
                    </div>
                    <div className="info-row">
                      <span className="info-key">Handoff 模式</span>
                      <span className="info-val">{handoff.resume ? 'Resume' : 'Fresh Claim'}</span>
                    </div>
                  </div>
                )}

                <div className="code-block">cpa-manager-plus initialize</div>
              </div>
              {handoff && (initErrorKind === 'transport' || initErrorKind === 'unavailable') && (
                <div className="wizard-card-footer">
                  <span style={{ fontSize: 12, color: 'var(--text-tertiary)' }}>
                    可尝试重新连接一次；若返回 HTTP 401（令牌已被消费），请在宿主机重新运行
                    cpa-manager-plus initialize 获取新 Handoff
                  </span>
                  <button
                    type="button"
                    className="btn btn-primary"
                    data-testid="retry-start-btn"
                    onClick={() => void executeStart(handoff)}
                  >
                    重试连接 Bootstrap 服务
                  </button>
                </div>
              )}
            </section>
          )}

          {phase === 'ready' && operation && (
            <section className="wizard-card" data-testid="bootstrap-operation-ready">
              <div className="wizard-card-header">
                <div className="wizard-step-tag">
                  <span>
                    {isAdminDone
                      ? 'Step 3 · 管理员初始化已完成'
                      : 'Step 2 · Fresh New 管理员密钥初始化'}
                  </span>
                  <span className="badge badge-neutral">
                    {t('lastRead')} · Revision #{operation.revision}
                  </span>
                </div>
                <h1 className="wizard-title">
                  {isAdminDone
                    ? 'Fresh Bootstrap 管理员密钥已完成初始化'
                    : '设置 CPAMP 管理员主密钥（Admin Key）'}
                </h1>
                <p className="wizard-desc">
                  {isAdminDone
                    ? '当前 Fresh 安装实例已完成管理员凭据绑定。服务端仅保存不可逆密钥校验值，不保存明文，后续状态查询与控制操作均受该安装身份约束。'
                    : '首次认领会话已建立。请通过 UI 生成或输入高强度管理员密钥并提交至 Manager 后端完成绑定，无需从容器日志读取随机初始密码。'}
                </p>
              </div>

              <div className="wizard-card-body">
                {feedback && (
                  <div
                    className={`alert alert-${feedback.tone}`}
                    role="status"
                    data-testid="bootstrap-action-feedback"
                  >
                    <span className="alert-icon" aria-hidden="true">
                      {feedback.tone === 'success' ? '✓' : feedback.tone === 'danger' ? '✕' : '!'}
                    </span>
                    <div className="alert-content">
                      <div className="alert-title" data-testid="feedback-title">
                        {feedback.title}
                      </div>
                      <div className="alert-desc" data-testid="feedback-detail">
                        {feedback.detail}
                      </div>
                    </div>
                  </div>
                )}

                <div className="info-card" data-testid="operation-status-card">
                  <div className="info-row">
                    <span className="info-key">Operation ID</span>
                    <span className="info-val" data-testid="op-id">
                      {operation.identity.operationId}
                    </span>
                  </div>
                  <div className="info-row">
                    <span className="info-key">Installation / Generation</span>
                    <span className="info-val" data-testid="op-install-gen">
                      {operation.identity.installationId} / {operation.identity.generation}
                    </span>
                  </div>
                  <div className="info-row">
                    <span className="info-key">Admission ID</span>
                    <span className="info-val" data-testid="op-admission-id">
                      {operation.identity.admissionId}
                    </span>
                  </div>
                  <div className="info-row">
                    <span className="info-key">会话建立方式</span>
                    <span className="info-val" data-testid="op-session-mode">
                      {handoff?.resume
                        ? '恢复会话（Resume Session）'
                        : '首次认领交换（Claim Exchange）'}
                    </span>
                  </div>
                  <div className="info-row">
                    <span className="info-key">管理员初始化状态</span>
                    <span
                      className="info-val"
                      data-testid="op-admin-status"
                      style={{
                        color: isAdminDone ? 'var(--emerald)' : 'var(--amber)',
                      }}
                    >
                      {isAdminDone ? '已初始化（Admin Initialized）' : '待初始化（Pending Commit）'}
                    </span>
                  </div>
                  <div className="info-row">
                    <span className="info-key">CPA 数据面网关状态</span>
                    <span
                      className="info-val"
                      data-testid="op-gateway-status"
                      style={{ color: 'var(--amber)' }}
                    >
                      {operation.gatewayPending
                        ? 'Pending · Fail-Closed（未激活，保持受控阻断）'
                        : 'Active'}
                    </span>
                  </div>
                  <div className="info-row">
                    <span className="info-key">正式路由切流状态</span>
                    <span className="info-val" data-testid="op-routing-status">
                      {verifiedRoute
                        ? t('routeVerifiedState')
                        : operation.routing.committed
                          ? `Committed (${operation.routing.target || 'console'})`
                          : '未切换 · 当前位于独立受控 Bootstrap 入口'}
                    </span>
                  </div>
                </div>

                {!isAdminDone ? (
                  <form
                    onSubmit={(e) => void handleCommitAdmin(e)}
                    data-testid="admin-init-form"
                    noValidate
                  >
                    {commitUnresolved && (
                      <p
                        className="alert alert-warn"
                        role="status"
                        data-testid="admin-commit-unresolved"
                      >
                        初始化提交结果未知。请先同步服务端状态；确认前不能更换密钥或再次提交。
                        若授权已过期，请保留已保存的密钥，在宿主机停止 Manager 后重新执行 initialize
                        获取恢复链接，勿删除数据卷。
                      </p>
                    )}
                    {candidateSubmitted && !commitUnresolved && (
                      <p className="alert alert-info" role="status">
                        已保留首次提交的密钥与
                        commitId。即使当前读取为待初始化，仍只可重试原提交，不能更换密钥。
                      </p>
                    )}
                    <div className="form-group">
                      <div className="form-label">
                        <label htmlFor="fresh-admin-key-input">管理员主密钥（Admin Key）</label>
                        <span className="form-hint">
                          24~256 字节，无空白字符，至少包含 3 类字符且不少于 12 个不同字符
                        </span>
                      </div>
                      <div className="input-wrap">
                        <input
                          ref={keyInputRef}
                          id="fresh-admin-key-input"
                          data-testid="admin-key-input"
                          type={showKey ? 'text' : 'password'}
                          className="form-input"
                          value={adminKey}
                          onChange={(e) => handleKeyInputChange(e.target.value)}
                          placeholder="输入自定义高强度密钥，或点击右侧“一键生成”"
                          autoComplete="off"
                          autoCorrect="off"
                          autoCapitalize="off"
                          spellCheck={false}
                          disabled={
                            submitting ||
                            generating ||
                            refreshing ||
                            commitUnresolved ||
                            candidateSubmitted
                          }
                        />
                        <div className="input-actions">
                          <button
                            type="button"
                            className="btn-xs"
                            data-testid="toggle-key-visibility-btn"
                            onClick={() => setShowKey((prev) => !prev)}
                            disabled={
                              submitting ||
                              generating ||
                              refreshing ||
                              commitUnresolved ||
                              candidateSubmitted
                            }
                          >
                            {showKey ? '隐藏' : '显示'}
                          </button>
                          <button
                            type="button"
                            className="btn-xs"
                            data-testid="generate-key-btn"
                            onClick={() => void handleGenerateKey()}
                            disabled={
                              submitting ||
                              generating ||
                              refreshing ||
                              commitUnresolved ||
                              candidateSubmitted
                            }
                          >
                            {generating ? '生成中…' : '一键生成'}
                          </button>
                          <button
                            type="button"
                            className="btn-xs"
                            data-testid="copy-key-btn"
                            onClick={() => void handleCopyKey()}
                            disabled={!adminKey || submitting || generating}
                          >
                            复制
                          </button>
                        </div>
                      </div>

                      <div className="strength-meter" aria-hidden="true">
                        {[1, 2, 3, 4].map((level) => {
                          let barClass = 'strength-bar';
                          if (strengthScore >= level) {
                            barClass +=
                              strengthScore === 4
                                ? ' active-strong'
                                : strengthScore >= 2
                                  ? ' active-medium'
                                  : ' active-weak';
                          }
                          return <div key={level} className={barClass} />;
                        })}
                      </div>
                      <div
                        className="strength-text"
                        data-testid="admin-key-validation-text"
                        style={{
                          color: !adminKey
                            ? 'var(--text-tertiary)'
                            : keyValidation.valid
                              ? 'var(--emerald)'
                              : 'var(--amber)',
                        }}
                      >
                        {keyValidation.reason}
                        {adminKey
                          ? `（当前：${keyValidation.byteLength} 字节 / ${keyValidation.classCount} 类字符 / ${keyValidation.distinctCount} 个不同字符）`
                          : ''}
                      </div>
                    </div>

                    <label
                      className={`checkbox-card ${confirmedSaved ? 'checked' : ''}`}
                      htmlFor="confirm-key-saved"
                      style={{ marginTop: 14 }}
                    >
                      <input
                        id="confirm-key-saved"
                        data-testid="confirm-key-saved-checkbox"
                        type="checkbox"
                        checked={confirmedSaved}
                        onChange={(e) => setConfirmedSaved(e.target.checked)}
                        disabled={
                          submitting ||
                          generating ||
                          refreshing ||
                          commitUnresolved ||
                          candidateSubmitted
                        }
                      />
                      <span className="checkbox-text">
                        <strong>我已复制并妥善保存该管理员密钥。</strong>
                        我理解提交后服务端仅保存不可逆密钥校验值，不保存明文，明文密钥不会保存在浏览器中，也无法再次从服务端查看。
                      </span>
                    </label>

                    <div
                      className="wizard-card-footer"
                      style={{ marginTop: 18, padding: '16px 0 0' }}
                    >
                      <button
                        type="button"
                        className="btn btn-secondary"
                        data-testid="refresh-operation-btn"
                        onClick={() => void handleRefreshOperation()}
                        disabled={submitting || generating || refreshing}
                      >
                        {refreshing ? '同步中…' : '同步服务端状态'}
                      </button>
                      <button
                        type="submit"
                        className="btn btn-primary"
                        data-testid="commit-admin-btn"
                        disabled={submitting || generating || refreshing || commitUnresolved}
                      >
                        {submitting ? '正在提交初始化…' : '提交并完成管理员初始化'}
                      </button>
                    </div>
                  </form>
                ) : (
                  <div data-testid="admin-init-complete-panel">
                    <div className="alert alert-success">
                      <span className="alert-icon" aria-hidden="true">
                        ✓
                      </span>
                      <div className="alert-content">
                        <div className="alert-title">
                          {commitCompletedInSession
                            ? '本次管理员密钥初始化已成功写入服务端'
                            : '已从服务端恢复管理员已初始化状态'}
                        </div>
                        <div className="alert-desc">
                          内存中的明文密钥已清理。当前 Fresh Bootstrap
                          第一个真实用户操作已完成，服务端 <code>adminInitialized = true</code>
                          （Revision #{operation.revision}）。
                        </div>
                      </div>
                    </div>

                    <div className="alert alert-info" style={{ marginTop: 12 }}>
                      <span className="alert-icon" aria-hidden="true">
                        i
                      </span>
                      <div className="alert-content">
                        <div className="alert-title">
                          安全与范围边界提示（CPA 数据面保持 Fail-Closed）
                        </div>
                        <div className="alert-desc">
                          管理员初始化完成不等同于 CPA
                          数据面就绪。在数据面安全条件获准并通过验收前，<code>gatewayPending</code>{' '}
                          保持为 <code>true</code>，CPA Gateway 不对公网开放数据面流量。
                        </div>
                      </div>
                    </div>

                    <form
                      onSubmit={(event) => void handleEnableConsole(event)}
                      data-testid="routing-form"
                      style={{ marginTop: 24 }}
                    >
                      <h2>{t('routeTitle')}</h2>
                      <label className="form-label" htmlFor="console-path">
                        {t('pathLabel')}
                      </label>
                      <div className="input-wrap">
                        <input
                          id="console-path"
                          className="form-input"
                          data-testid="console-path"
                          value={preparedTarget || routeTarget}
                          onChange={(event) => setRouteTarget(event.target.value)}
                          disabled={!!preparedTarget || routingBusy || !!verifiedRoute}
                          autoComplete="off"
                          spellCheck={false}
                        />
                      </div>
                      <p className="form-hint">{t('pathHelp')}</p>
                      {routingError && (
                        <p role="alert" className="alert alert-warn">
                          {routingError}
                        </p>
                      )}
                      {verifiedRoute ? (
                        <div role="status">
                          <p>{t('routeReady')}</p>
                          <a
                            className="btn btn-primary"
                            href={verifiedRoute}
                            data-testid="continue-console"
                          >
                            {t('continue')}
                          </a>
                        </div>
                      ) : (
                        <button
                          type="submit"
                          className="btn btn-primary"
                          data-testid="enable-console"
                          disabled={routingBusy || refreshing}
                        >
                          {routingBusy ? t('activating') : t('activate')}
                        </button>
                      )}
                    </form>

                    <div
                      className="wizard-card-footer"
                      style={{ marginTop: 18, padding: '16px 0 0' }}
                    >
                      <span style={{ fontSize: 12, color: 'var(--text-tertiary)' }}>
                        {verifiedRoute
                          ? t('loginHelp')
                          : '可通过受控会话令牌随时核验服务端实时 Operation 状态'}
                      </span>
                      <button
                        type="button"
                        className="btn btn-secondary"
                        data-testid="refresh-operation-btn"
                        onClick={() => void handleRefreshOperation()}
                        disabled={refreshing || routingBusy || !!verifiedRoute}
                      >
                        {refreshing ? '正在刷新…' : '刷新操作状态'}
                      </button>
                    </div>
                  </div>
                )}
              </div>
            </section>
          )}
        </div>
      </main>
    </div>
  );
}
