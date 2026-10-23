import React, { useCallback, useEffect, useRef, useState } from 'react';
import { fullWebText as t } from '../i18n/fullweb';
import { bootstrapJourneyText as journey } from '../i18n/bootstrapJourney';
import logo from '../assets/brand/cpamp-logo-horizontal-color.svg?inline';
import logoOnDark from '../assets/brand/cpamp-logo-horizontal-on-dark.svg?inline';
import { FullWebIcon } from './FullWebIcon';
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
type DiscoveryGuidance = ActionFeedback & { flow: 'fresh' | 'host'; retryable?: true };
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
      retryable: true,
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
  const [lightTheme, setLightTheme] = useState(true);
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
    document.documentElement.setAttribute('data-theme', lightTheme ? 'white' : 'dark');
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
        title: journey('generated'),
        detail: journey('generatedHelp'),
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
        title: journey('progressRead'),
        detail: journey(next.adminInitialized ? 'adminComplete' : 'adminPending'),
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
        title: journey('commitSaved'),
        detail: journey('commitSavedHelp'),
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
              title: journey('reconciled'),
              detail: journey('reconciledHelp'),
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
  const preparationStep = verifiedRoute ? 3 : isAdminDone ? 2 : phase === 'ready' ? 1 : 0;

  return (
    <div className="bootstrap-shell" data-testid="fresh-bootstrap-shell">
      <header className="bootstrap-header">
        <div className="brand-group">
          <img className="journey-logo" src={lightTheme ? logo : logoOnDark} alt="CPAMP" />
          <span className="badge badge-indigo">
            {phase === 'no_handoff' && discoveryNote?.flow !== 'fresh'
              ? journey('preflight')
              : journey('preview')}
          </span>
          {operation && (
            <span
              className={`badge ${operation.gatewayPending ? 'badge-amber' : 'badge-emerald'}`}
              data-testid="gateway-status-badge"
            >
              {operation.gatewayPending ? journey('pending') : journey('gatewayReady')}
            </span>
          )}
        </div>
        <div className="header-actions">
          <button
            type="button"
            className="btn btn-ghost btn-icon-only"
            onClick={() => setLightTheme((prev) => !prev)}
            aria-label={journey('theme')}
            title={journey('theme')}
          >
            <FullWebIcon name={lightTheme ? 'moon' : 'sun'} size={24} />
          </button>
          <span className="journey-version">v2</span>
        </div>
      </header>

      <main className="bootstrap-main">
        <div className="wizard-container">
          {!(phase === 'no_handoff' && discoveryNote?.flow !== 'fresh') && (
            <ol className="journey-steps" aria-label={journey('progress')}>
              {(['claim', 'administrator', 'address', 'continue'] as const).map((label, index) => (
                <li
                  key={label}
                  className={
                    index < preparationStep ? 'done' : index === preparationStep ? 'active' : ''
                  }
                  aria-current={index === preparationStep ? 'step' : undefined}
                >
                  <span className="journey-step-dot">
                    {index < preparationStep ? <FullWebIcon name="check" size={22} /> : index + 1}
                  </span>
                  <span>{journey(label)}</span>
                </li>
              ))}
            </ol>
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
                      ? journey('claimRequired')
                      : discoveryChecked
                        ? '来源状态无法确认，禁止直接初始化'
                        : '正在确认来源状态，禁止直接初始化'}
                </h1>
                <p className="wizard-desc">
                  {discoveryNote?.flow === 'host'
                    ? '公共 Discovery 仅提供来源类型提示，不读取或修改现有数据；后续操作必须在受信宿主机执行。'
                    : discoveryNote?.flow === 'fresh'
                      ? journey('claimRequiredHelp')
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
                      <FullWebIcon name="alert" size={22} />
                      <div className="alert-content">
                        <div className="alert-title">如何获取或恢复 Bootstrap 授权链接？</div>
                        <div className="alert-desc">
                          请在受信宿主机运行初始化命令，并使用输出的带 <code>#handoff=...</code>{' '}
                          片段的完整 URL 重新打开本页面：
                        </div>
                      </div>
                    </div>
                    <div className="code-block" data-testid="cli-handoff-command">
                      cpa-manager-plus initialize
                    </div>
                  </>
                ) : (
                  <div
                    className="alert alert-warn"
                    data-testid="bootstrap-discovery-unconfirmed"
                    role="status"
                  >
                    <div className="alert-content">
                      <div className="alert-title">
                        {discoveryChecked ? '来源状态无法确认' : '正在读取来源状态'}
                      </div>
                      <div className="alert-desc">
                        不提供 Fresh
                        初始化命令；请在受信宿主机确认来源、日志与服务状态。未知结果不会授予任何写入权限。
                      </div>
                    </div>
                  </div>
                )}
                {discoveryChecked && (!discoveryNote || discoveryNote.retryable) && (
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
                  <span>{journey('claim')}</span>
                </div>
                <h1 className="wizard-title">
                  {handoff?.resume ? journey('resuming') : journey('claiming')}
                </h1>
                <p className="wizard-desc">{journey('claimingHelp')}</p>
              </div>
              <div className="wizard-card-body">
                <details>
                  <summary>{journey('diagnostics')}</summary>
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
                </details>
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
                  <FullWebIcon name="alert" size={22} />
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
                  <span>{journey(isAdminDone ? 'address' : 'administrator')}</span>
                </div>
                <h1 className="wizard-title">
                  {isAdminDone ? journey('addressTitle') : journey('adminTitle')}
                </h1>
                <p className="wizard-desc">
                  {isAdminDone ? journey('addressHelp') : journey('adminHelp')}
                </p>
              </div>

              <div className="wizard-card-body">
                {feedback && (
                  <div
                    className={`alert alert-${feedback.tone}`}
                    role="status"
                    data-testid="bootstrap-action-feedback"
                  >
                    <FullWebIcon
                      name={
                        feedback.tone === 'success'
                          ? 'check'
                          : feedback.tone === 'danger'
                            ? 'alert'
                            : 'info'
                      }
                      size={22}
                    />
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

                <details data-testid="bootstrap-diagnostics">
                  <summary>{journey('diagnostics')}</summary>
                  <div className="info-card" data-testid="operation-status-card">
                    <div className="info-row">
                      <span className="info-key">{journey('revision')}</span>
                      <span className="info-val">#{operation.revision}</span>
                    </div>
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
                        {isAdminDone
                          ? '已初始化（Admin Initialized）'
                          : '待初始化（Pending Commit）'}
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
                </details>

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
                        {journey('original')}
                      </p>
                    )}
                    <div className="form-group">
                      <div className="form-label">
                        <label htmlFor="fresh-admin-key-input">{journey('key')}</label>
                        <span className="form-hint">{journey('keyRules')}</span>
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
                          placeholder={journey('keyPlaceholder')}
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
                            className="btn btn-secondary btn-xs"
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
                            <FullWebIcon name="eye" size={20} />
                            {journey(showKey ? 'hide' : 'show')}
                          </button>
                          <button
                            type="button"
                            className="btn btn-secondary btn-xs"
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
                            <FullWebIcon name="key" size={18} />
                            {journey(generating ? 'generating' : 'generate')}
                          </button>
                          <button
                            type="button"
                            className="btn btn-secondary btn-xs"
                            data-testid="copy-key-btn"
                            onClick={() => void handleCopyKey()}
                            disabled={!adminKey || submitting || generating}
                          >
                            <FullWebIcon name="copy" size={18} />
                            {journey('copy')}
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
                        <strong>{journey('savedConfirm')}</strong>
                        {journey('savedWarning')}
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
                        {journey(refreshing ? 'refreshing' : 'refresh')}
                      </button>
                      <button
                        type="submit"
                        className="btn btn-primary"
                        data-testid="commit-admin-btn"
                        disabled={submitting || generating || refreshing || commitUnresolved}
                      >
                        {journey(submitting ? 'saving' : 'save')}
                        <FullWebIcon name="arrowRight" size={20} />
                      </button>
                    </div>
                  </form>
                ) : (
                  <div data-testid="admin-init-complete-panel">
                    <div className="alert alert-success">
                      <FullWebIcon name="shield" size={24} />
                      <div className="alert-content">
                        <div className="alert-title">
                          {commitCompletedInSession ? journey('saved') : journey('restored')}
                        </div>
                        <div className="alert-desc">{journey('savedHelp')}</div>
                      </div>
                    </div>

                    <p className="journey-quiet">
                      <FullWebIcon name="info" size={20} />
                      {journey('installNotStarted')}
                    </p>

                    <form
                      onSubmit={(event) => void handleEnableConsole(event)}
                      data-testid="routing-form"
                      style={{ marginTop: 24 }}
                    >
                      <h2>{journey('routeTitle')}</h2>
                      <label className="form-label" htmlFor="console-path">
                        {journey('path')}
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
                      <p className="form-hint">{journey('pathHelp')}</p>
                      <div className="journey-preview">
                        <FullWebIcon name="link" size={22} />
                        <div>
                          {journey('pathPreview')}
                          <span>
                            {window.location.origin + (preparedTarget || routeTarget || '/')}
                          </span>
                        </div>
                      </div>
                      {routingError && (
                        <p role="alert" className="alert alert-warn">
                          {routingError}
                        </p>
                      )}
                      {verifiedRoute ? (
                        <div role="status">
                          <p>{journey('routeReady')}</p>
                          <a
                            className="btn btn-primary"
                            href={verifiedRoute}
                            data-testid="continue-console"
                          >
                            {journey('continue')}
                            <FullWebIcon name="arrowRight" size={20} />
                          </a>
                        </div>
                      ) : (
                        <button
                          type="submit"
                          className="btn btn-primary"
                          data-testid="enable-console"
                          disabled={routingBusy || refreshing}
                        >
                          {journey(routingBusy ? 'enabling' : 'enable')}
                          <FullWebIcon name="arrowRight" size={20} />
                        </button>
                      )}
                    </form>

                    <div
                      className="wizard-card-footer"
                      style={{ marginTop: 18, padding: '16px 0 0' }}
                    >
                      <span style={{ fontSize: 12, color: 'var(--text-tertiary)' }}>
                        {verifiedRoute ? journey('loginHelp') : journey('refresh')}
                      </span>
                      <button
                        type="button"
                        className="btn btn-secondary"
                        data-testid="refresh-operation-btn"
                        onClick={() => void handleRefreshOperation()}
                        disabled={refreshing || routingBusy || !!verifiedRoute}
                      >
                        {journey(refreshing ? 'refreshing' : 'refresh')}
                      </button>
                    </div>
                  </div>
                )}
              </div>
            </section>
          )}
        </div>
      </main>
      <footer className="journey-copyright">{journey('footer')}</footer>
    </div>
  );
}
