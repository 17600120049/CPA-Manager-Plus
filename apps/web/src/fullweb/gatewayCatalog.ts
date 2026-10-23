export interface GatewayFieldMeta {
  path: string;
  label: string;
  type: 'number' | 'bool' | 'select' | 'text' | 'duration' | 'url';
  value: string | number | boolean;
  help: string;
  min?: number;
  options?: Record<string, string>;
  depends?: string;
  risk?: boolean;
}

export interface GatewaySectionMeta {
  id: string;
  tab: 'routing' | 'retry' | 'requests' | 'media' | 'compat';
  title: string;
  description: string;
  fields: GatewayFieldMeta[];
}

export interface GatewayOwnerMeta {
  path: string;
  owner: string;
  description: string;
}

export const COMMON_GATEWAY_PATHS = new Set([
  'routing.strategy',
  'routing.session-affinity',
  'routing.force-model-prefix',
  'routing.retry.request-retry',
  'routing.retry.max-retry-interval',
  'requests.proxy-url',
  'requests.passthrough-headers',
  'requests.streaming.keepalive-seconds',
  'multimedia.disable-image-generation',
]);

export const GATEWAY_SECTIONS: GatewaySectionMeta[] = [
  {
    id: 'routing',
    tab: 'routing',
    title: '路由与会话亲和',
    description: '决定请求分配到哪一份凭据，以及后续会话如何保持绑定。',
    fields: [
      {
        path: 'routing.strategy',
        label: '调度策略',
        type: 'select',
        value: 'round-robin',
        help: '加权轮询使用凭据自身的权重；权重在凭据页面管理。',
        options: {
          'round-robin': '轮询',
          'weighted-round-robin': '加权轮询',
          'fill-first': '优先填满',
        },
      },
      {
        path: 'routing.session-affinity',
        label: '会话亲和',
        type: 'bool',
        value: false,
        help: '将同一会话绑定到凭据；凭据不可用时自动故障转移。',
      },
      {
        path: 'routing.session-affinity-ttl',
        label: '绑定保留时间',
        type: 'duration',
        value: '1h',
        help: '会话绑定的有效期。',
        depends: 'routing.session-affinity',
      },
      {
        path: 'routing.session-affinity-subagents',
        label: '子代理继承会话绑定',
        type: 'bool',
        value: true,
        help: '继承父会话凭据以复用缓存；关闭时按调度策略分配。',
        depends: 'routing.session-affinity',
      },
      {
        path: 'routing.force-model-prefix',
        label: '要求模型前缀匹配',
        type: 'bool',
        value: false,
        help: '无前缀请求只使用无前缀凭据（前缀等于模型名的情况除外）。',
      },
    ],
  },
  {
    id: 'retry',
    tab: 'retry',
    title: '重试轮次与冷却等待',
    description: '明确额外轮次、每轮凭据上限与冷却等待的不同含义。',
    fields: [
      {
        path: 'routing.retry.request-retry',
        label: '额外重试轮次',
        type: 'number',
        value: 2,
        help: '首轮耗尽可用凭据后的额外轮次；0 禁止额外轮次。范围 0–10。',
        min: 0,
      },
      {
        path: 'routing.retry.max-retry-credentials',
        label: '每轮最多尝试凭据',
        type: 'number',
        value: 0,
        help: '0 表示尝试所有可用凭据。凭据级覆盖仍优先。',
        min: 0,
      },
      {
        path: 'routing.retry.max-retry-interval',
        label: '最大冷却等待（秒）',
        type: 'number',
        value: 30,
        help: '0 禁止等待冷却；正数限制最长等待秒数（0–300）。不等于请求超时。',
        min: 0,
      },
      {
        path: 'routing.cooldown.disable-cooling',
        label: '禁用全局冷却',
        type: 'bool',
        value: false,
        help: '开启后禁用凭据/模型冷却调度，可能提高上游失败频率。',
      },
      {
        path: 'routing.cooldown.save-cooldown-status',
        label: '持久化冷却状态',
        type: 'bool',
        value: false,
        help: '将冷却状态保存到凭据旁的 .cds 文件；关闭时仅保留在内存。',
      },
      {
        path: 'routing.cooldown.transient-error-cooldown-seconds',
        label: '瞬态错误冷却（秒）',
        type: 'number',
        value: 0,
        help: '0 沿用 60 秒历史行为；-1 禁用瞬态错误冷却。',
        min: -1,
      },
    ],
  },
  {
    id: 'requests',
    tab: 'requests',
    title: '代理与响应传输',
    description: '控制网关全局网络出口与非流式响应行为。',
    fields: [
      {
        path: 'requests.proxy-url',
        label: '全局代理地址',
        type: 'url',
        value: '',
        help: '支持 socks5 / http / https。带凭据代理需专用安全引用，不支持在 URL 中包含明文密码。',
      },
      {
        path: 'requests.passthrough-headers',
        label: '透传筛选后的响应头',
        type: 'bool',
        value: false,
        help: '只传递经过筛选的上游响应头。',
      },
      {
        path: 'requests.nonstream-keepalive-interval',
        label: '非流式保活间隔（秒）',
        type: 'number',
        value: 0,
        help: '大于 0 时周期性输出空行；0 禁用。',
        min: 0,
      },
      {
        path: 'requests.streaming.keepalive-seconds',
        label: 'SSE 保活间隔（秒）',
        type: 'number',
        value: 15,
        help: '示例值为 15；上游默认 0（关闭），非正数关闭保活。',
        min: -1,
      },
      {
        path: 'requests.streaming.bootstrap-retries',
        label: '首字节前重试次数',
        type: 'number',
        value: 1,
        help: '示例值为 1；上游默认 0，不重放已发送的响应。',
        min: 0,
      },
    ],
  },
  {
    id: 'media',
    tab: 'media',
    title: '图片与视频',
    description: '全局多媒体能力；Codex Live 与 TURN 在对应提供商内管理。',
    fields: [
      {
        path: 'multimedia.disable-image-generation',
        label: '图片生成限制',
        type: 'select',
        value: 'false',
        help: 'true 全局禁用；chat 保留图片接口但禁用聊天内生图。',
        options: {
          false: '不禁用',
          true: '全部禁用',
          chat: '禁用聊天内生图',
        },
      },
      {
        path: 'multimedia.gpt-image-2-base-model',
        label: 'GPT Image 2 基础模型',
        type: 'text',
        value: 'gpt-5.4-mini',
        help: '上游示例模型名；实际可用性由目标模型能力决定。',
      },
      {
        path: 'multimedia.video-result-auth-cache-ttl',
        label: '视频结果授权保留',
        type: 'duration',
        value: '3h',
        help: '视频 ID 与创建者 API 密钥的授权关联保留时间。',
      },
    ],
  },
  {
    id: 'compat',
    tab: 'compat',
    title: '客户端与上游兼容性',
    description: '作用于客户端请求与上游协议适配的共享行为。',
    fields: [
      {
        path: 'client.codex.enable-apply-patch',
        label: '扩展 apply_patch 声明',
        type: 'bool',
        value: false,
        help: '为受支持的非模板/桥接模型声明 freeform apply_patch。',
      },
      {
        path: 'client.codex.optimize-multi-agent-v2',
        label: 'Multi-agent v2 兼容',
        type: 'bool',
        value: false,
        help: '调整受支持 Codex 客户端的多代理请求形态。',
      },
      {
        path: 'upstream.codex.response-steering',
        label: '全双工响应控制',
        type: 'bool',
        value: false,
        help: '实验 WebSocket 模式；一个连接绑定同一上游账号/模型，不重放已接受输入。',
        risk: true,
      },
      {
        path: 'upstream.codex.disable-codex-cloaking',
        label: '禁用 Codex 请求伪装',
        type: 'bool',
        value: false,
        help: '停止强制注入官方 Codex User-Agent / Originator。',
      },
      {
        path: 'upstream.claude.model-level-cooling',
        label: 'Claude 模型级限额冷却',
        type: 'bool',
        value: false,
        help: '避免一个模型的额度问题冷却全部同凭据模型。',
      },
      {
        path: 'upstream.claude.disable-claude-cloak-mode',
        label: '禁用 Claude 请求伪装',
        type: 'bool',
        value: false,
        help: '凭据属性和每个 key 的 cloak 模式可覆盖此默认值。',
      },
      {
        path: 'upstream.xai.inject-x-search',
        label: '补充 x_search 工具',
        type: 'bool',
        value: false,
        help: '仅在请求未声明工具时注入原生 x_search。',
      },
    ],
  },
];

export const GATEWAY_OWNERS: GatewayOwnerMeta[] = [
  {
    path: 'access.api-keys',
    owner: 'API 密钥',
    description: '客户端访问密钥、分组与权限；不在网关全局页复制编辑器。',
  },
  {
    path: 'api-keys.*',
    owner: 'AI 提供商',
    description: 'Gemini / Interactions / Vertex / Codex / Claude / xAI / Meta / OpenAI-compatible；每项 URL、key、模型与 override。',
  },
  {
    path: 'models.* / oauth.model-alias / oauth.excluded-models',
    owner: '模型管理',
    description: '模型目录、OAuth 模型别名和排除项；避免与路由策略混写。',
  },
  {
    path: 'oauth.auth-dir / oauth.auth-auto-refresh-workers / oauth.settings / oauth.request-scoped-errors',
    owner: '凭据管理 / 凭据健康',
    description: '凭据存放与刷新策略、请求级错误处理；auth-dir 属部署来源。',
  },
  {
    path: 'oauth.providers.codex.live-media-relay.*',
    owner: '提供商 · Codex Live',
    description: 'enabled、max-sessions、私网候选限制、公网 IP、UDP 端口范围、ICE URL 和 TURN username/credential；需专用凭据动作，非通用多媒体 secret。',
  },
  {
    path: 'oauth.providers.codex.header-defaults.* / oauth.providers.aistudio.ws-auth',
    owner: '对应提供商',
    description: 'Codex OAuth 头部回退与 AI Studio WebSocket 认证；不伪装成所有网关通用项。',
  },
  {
    path: 'oauth.providers.antigravity.* / oauth.providers.devin.*',
    owner: '对应提供商',
    description: '敏感词、连接池、签名与 credits 等 provider-specific 行为。',
  },
  {
    path: 'credentials.concurrency.* / credentials.in-flight.*',
    owner: '运行状态 / 凭据治理',
    description: 'Home 可能拥有权威；revision 等系统元数据只读，不能作为普通用户开关。',
  },
  {
    path: 'observability.logs.* / observability.pprof.*',
    owner: '系统日志 / 运行状态',
    description: '日志级别、文件保留、请求日志与性能诊断；调试监听属于部署边界。',
  },
  {
    path: 'observability.usage.*',
    owner: '请求监控 / 用量维护',
    description: '采集开关与队列保留；不混入 CPAMP 系统基础设置。',
  },
  {
    path: 'server.* / management.*',
    owner: '运行状态 / 部署信息',
    description: '监听、TLS、受信代理、CPA 管理保护和面板来源；CPAMP 管理入口由 Product operation 管理。',
  },
  {
    path: 'plugins.*',
    owner: '插件管理',
    description: '全局启用、目录、商店来源/环境变量凭据及每插件配置；opaque schema 不能自动做通用编辑器。',
  },
  {
    path: 'config-version / quota-exceeded.switch-project / quota-exceeded.switch-preview-model',
    owner: '格式 / 历史兼容',
    description: 'config-version=8 为格式元数据；两个旧 quota 开关不在当前 active v8 模板中，不能作为新产品开关。',
  },
];
