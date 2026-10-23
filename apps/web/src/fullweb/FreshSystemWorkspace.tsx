import { useState } from 'react';
import { useNavigate } from 'react-router-dom';

export function FreshSystemWorkspace({
  basePath,
}: {
  basePath: string;
  logoutDisabled?: boolean;
}) {
  const navigate = useNavigate();
  const [recoveryModalOpen, setRecoveryModalOpen] = useState(false);

  return (
    <div className="fresh-system-workspace" data-testid="fresh-system-workspace">
      <div className="page-head">
        <div>
          <div className="eyebrow">SYSTEM MANAGEMENT</div>
          <h1 id="system-workspace-title" tabIndex={-1}>
            系统配置
          </h1>
          <p className="muted">管理产品入口、启动方式与管理员安全。</p>
        </div>
        <span className="badge">系统管理 · CPAMP</span>
      </div>

      <div className="grid">
        <div className="stack">
          {/* 管理入口卡片 */}
          <section className="card" data-testid="system-entry-card">
            <div className="card-head">
              <div>
                <h2>管理入口</h2>
                <p className="muted">Console 的访问路径与变更状态</p>
              </div>
              <span className="badge badge-emerald">当前入口已验证</span>
            </div>
            <div className="row">
              <div>
                <label>当前已验证入口</label>
                <span className="path" data-testid="system-entry-path">
                  <code>{basePath || '/console'}</code>
                </span>
                <small>来源：CPAMP 产品设置 · 受控访问基准</small>
              </div>
            </div>
            <div className="notice">
              已验证安全入口保护中。如需切换管理前缀，请使用受控产品 operation 与对应更新流程，此处不提供未经证明的在线重定向。
            </div>
          </section>

          {/* 启动与实验功能 */}
          <section className="card" data-testid="system-startup-card">
            <div className="card-head">
              <div>
                <h2>启动与运行状态</h2>
                <p className="muted">宿主服务如何随系统启动与调度</p>
              </div>
              <span className="badge">宿主与部署</span>
            </div>
            <div className="row">
              <div>
                <label>系统服务</label>
                <small>由部署平台或宿主 supervisor 管理，配置变更随容器与宿主环境声明持久化。</small>
              </div>
              <span className="badge">正常运行</span>
            </div>
            <p className="detail" style={{ marginTop: '16px' }}>
              当前没有可用实验项。语言与主题偏好仍在导航顶部工具条管理。
            </p>
          </section>
        </div>

        <div className="stack">
          {/* 管理员安全 */}
          <section className="card" data-testid="system-security-card">
            <div className="card-head">
              <h2>管理员安全</h2>
              <span className="badge badge-emerald">已配置</span>
            </div>
            <p>当前会话使用已验证的管理员密钥。</p>
            <p className="muted">
              密钥丢失或需要轮换时，由主机管理员在停机状态下使用匹配版本的离线恢复工具处理。
            </p>
            <div className="actions">
              <button
                className="btn btn-secondary"
                data-testid="view-offline-recovery-btn"
                onClick={() => setRecoveryModalOpen(true)}
              >
                查看离线恢复说明
              </button>
            </div>
            <p className="detail" style={{ marginTop: '15px' }}>
              为保障凭证不可篡改与防重放，本页面绝不以明文回显、传输或在线重置管理员密钥。
            </p>
          </section>

          {/* 配置归属卡片 */}
          <section className="card" data-testid="system-ownership-card">
            <div className="card-head">
              <h2>配置归属</h2>
            </div>
            <p className="muted">
              网关请求与冷却策略在网关管理中集中处理。API 密钥、模型管理与上游提供商由对应专属工作区管理。
            </p>
            <div className="actions">
              <button
                className="btn btn-primary"
                data-testid="goto-gateway-workspace-btn"
                onClick={() => navigate('/gateway')}
              >
                前往网关管理
              </button>
            </div>
          </section>
        </div>
      </div>

      {/* 离线恢复说明模态框 */}
      {recoveryModalOpen && (
        <div
          className="modal-backdrop"
          role="presentation"
          onClick={() => setRecoveryModalOpen(false)}
        >
          <div
            className="card modal-content"
            role="dialog"
            aria-modal="true"
            aria-labelledby="recovery-modal-title"
            onClick={(e) => e.stopPropagation()}
            data-testid="recovery-modal"
          >
            <div className="card-head">
              <h2 id="recovery-modal-title">主机侧管理员恢复说明</h2>
              <button
                className="btn btn-secondary btn-icon"
                onClick={() => setRecoveryModalOpen(false)}
                aria-label="关闭"
              >
                ×
              </button>
            </div>
            <p>由系统管理员在受控主机环境中操作：</p>
            <ol className="recovery-steps">
              <li>
                <strong>定位数据卷与存储：</strong> 确认 SQLite 数据库文件与数据密钥（<code>data.key</code>）的正确存储路径。
              </li>
              <li>
                <strong>停机维护：</strong> 停止正在使用该数据卷的 Manager 服务进程或容器，避免并发写入冲突。
              </li>
              <li>
                <strong>执行离线重置：</strong> 运行与当前运行版本严格匹配的命令行工具 <code>adminreset</code>，生成新的管理员凭证摘要。
              </li>
              <li>
                <strong>重启并验证：</strong> 重新启动 Manager 服务，使用新密钥登录 Console 验证状态。
              </li>
            </ol>
            <div className="notice warn">
              请妥善保管数据备份；独立运行的 CPA 网关无需因 CPAMP 管理员密钥恢复而停止。
            </div>
            <div className="wizard-card-footer" style={{ marginTop: '20px' }}>
              <button
                className="btn btn-primary"
                onClick={() => setRecoveryModalOpen(false)}
              >
                已知悉
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
