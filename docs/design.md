# X 全自动规则驱动运营系统设计

详细的模块、数据、运行时和部署架构见
[X AI Operations 架构设计](architecture.md)。

## 1. 产品目标

构建一个服务于单个 X 账号的全自动运营系统，通过稳定输出原创内容和持续实验增加有效粉丝。

系统采用本地优先架构：

- 研究、选题、写作、质检和分析通过统一 LLM Gateway 使用本地或远程模型
- 状态、任务和指标保存在本地 SQLite
- 动态发布、被召唤后的评论回复和指标读取使用 X 官方 API
- 不使用浏览器脚本操作 X，不模拟鼠标或复用登录 Cookie

“全自动”指首次完成账号策略配置、X 开发者应用设置和 OAuth 授权后，正常运行不需要人工逐条审批或点击发布。遇到凭证失效、余额不足、平台拒绝或安全闸门阻断时，系统自动停机并告警，不通过其他通道绕过。

### 北极星指标

`每周有效净增粉丝 = 周末粉丝数 - 周初粉丝数 - 异常粉丝估算`

| 层级 | 指标 | 用途 |
| --- | --- | --- |
| 结果 | 净增粉丝、周增长率 | 判断是否真正增长 |
| 转化 | 每千次曝光净增粉丝 | 判断流量质量 |
| 内容 | 曝光、回复、转发、点赞 | 定位有效内容 |
| 健康 | 取消关注、发布失败率、内容重复率 | 控制账号风险 |
| 成本 | API 月消耗、单个净增粉丝成本 | 控制投入 |

### 非目标

- 自动关注、取关、点赞、转发、私信或未召唤的陌生帖子回复
- 关键词搜索和批量互动
- 多账号发布相同或近似内容
- 搬运、洗稿或大批量生成低差异内容
- 浏览器模拟、设备指纹伪装或绕过平台限制
- 承诺固定粉丝数量

## 2. 默认配置

```yaml
config:
  version: 1
  effective_at: "2026-10-17T00:00:00+08:00"

account:
  handle: "@example"
  language: "zh-CN"
  timezone: "Asia/Shanghai"
  audience: "关注 AI 工程实践的开发者和技术管理者"
  promise: "每天提供一个可直接复用的 AI 工程经验"
  voice: ["直接", "有证据", "不夸张"]
  forbidden_topics: ["未披露广告", "未经证实的爆料"]

content_policy:
  allow_urls: true
  allow_media: false
  allow_threads: false
  approval_mode: "automatic_policy_gate"
  pillars:
    - name: "实战方法"
      initial_weight: 0.40
    - name: "案例拆解"
      initial_weight: 0.30
    - name: "行业观点"
      initial_weight: 0.20
    - name: "幕后过程"
      initial_weight: 0.10

engagement:
  enabled: true
  reply_mode: "summoned_only"
  require_original_author_summon: true
  allow_unsolicited_replies: false
  allow_direct_messages: false
  allow_quote_posts: false
  allow_mentions_outside_thread: false

automation:
  mode: "full_auto_rule_driven"
  x_write_channel: "official_api"
  oauth_scopes:
    - "tweet.read"
    - "tweet.write"
    - "users.read"
    - "offline.access"

llm:
  default_profile: "local"
  profiles:
    local:
      protocol: "openai_compatible"
      base_url: "http://127.0.0.1:11434/v1"
      api_key_ref: null
      model: "configured-local-model"
      capabilities: ["json_object"]
    remote:
      protocol: "openai_compatible"
      base_url: "https://provider.example.com/v1"
      api_key_ref: "keychain://xops/llm/remote"
      model: "configured-remote-model"
      capabilities: ["json_schema"]
  routing:
    default: ["local", "remote"]
    agents:
      fact_check: ["remote", "local"]
  privacy:
    allow_remote: true
    redact_sensitive_fields: true
  metering:
    track_tokens: true
    track_cost: true

billing:
  budget_mode: "unlimited"
  enforce_spending_limit: false
  track_cost: true
  currency: "USD"
  on_credit_exhausted: "pause_and_alert"
  pricing_catalog_version: "2026-10-16"
  pricing_catalog_max_age_days: 30
  unit_cost:
    post_create: 0.015
    post_create_with_url: 0.200
    post_create_summoned: 0.010
    post_read: 0.005
    user_read: 0.010
    owned_read: 0.001

actions:
  source_collect:
    enabled: true
    trigger: "interval"
    every: "30m"
    max_concurrency: 2
    retry: {max_attempts: 3, backoff: "exponential"}

  topic_generate:
    enabled: true
    trigger: "quota"
    quota: {per_day: 30}
    schedule: {windows: ["00:00-06:00"]}
    max_concurrency: 1

  publish_post:
    enabled: true
    trigger: "quota"
    quota: {per_day: 10}
    schedule:
      timezone: "Asia/Shanghai"
      days: ["mon", "tue", "wed", "thu", "fri", "sat", "sun"]
      windows: ["08:00-23:00"]
    cooldown:
      min_interval: "60m"
      same_topic: "24h"
    queue:
      max_pending: 20
      max_age: "24h"
      overflow: "drop_oldest"
    retry: {max_attempts: 2, backoff: "platform_aware"}

  sync_mentions:
    enabled: true
    trigger: "interval"
    every: "15m"
    max_resources_per_day: null
    max_concurrency: 1

  publish_reply:
    enabled: true
    trigger: "quota"
    quota: {per_day: 5}
    schedule: {windows: ["08:00-23:00"]}
    cooldown:
      min_interval: "30m"
      same_user: "24h"
    retry: {max_attempts: 1, backoff: "platform_aware"}

  sync_account_metrics:
    enabled: true
    trigger: "interval"
    every: "24h"

  sync_post_metrics:
    enabled: true
    trigger: "delayed"
    after_publish: ["1h", "24h", "7d"]

  update_strategy:
    enabled: true
    trigger: "cron"
    expression: "0 2 * * 1"
```

OpenAI-compatible API 是优先接入协议，但不是所有本地模型和远程服务商都原生兼容。LLM Gateway 通过 Provider Adapter 统一 `openai_compatible`、`ollama_native`、`anthropic`、`gemini` 等协议，并根据结构化输出、视觉、工具调用和上下文长度能力选择可用模型。远程 API Key 只保存 Keychain 引用，不写入规则文件。

`null` 表示应用不设置数量上限，但仍受 X API 速率限制、余额和平台规则约束。所有配置均需通过 Schema 与可行性校验后才能生效。

## 3. 规则引擎

### 3.1 规则优先级

执行优先级固定为：

```text
平台政策
  > OAuth scope
  > X API rate limit/reset
  > 账号级暂停与熔断
  > action enabled
  > action quota
  > schedule window
  > cooldown
  > queue/retry policy
```

业务配置不能覆盖更高优先级约束。例如将 `publish_reply.quota.per_day` 设置为 100，不会允许系统回复未明确召唤账号的帖子。

### 3.2 通用动作模型

每个受支持操作通过 `actions.<action_name>` 配置：

| 字段 | 含义 |
| --- | --- |
| `enabled` | 是否启用该操作 |
| `trigger` | `quota`、`interval`、`cron` 或 `delayed` |
| `quota` | 每分钟、每小时、每天的应用级数量 |
| `schedule` | 时区、星期和允许执行时间窗 |
| `cooldown` | 全局、同主题或同用户最小间隔 |
| `max_concurrency` | 同时运行的任务数 |
| `queue` | 最大积压、任务时效和溢出策略 |
| `retry` | 最大尝试次数、退避和可重试错误 |

系统启动时计算规则是否可实现。例如 `10 条/天`、窗口 `08:00-23:00`、最小间隔 `60m` 可以执行；若数量、窗口和间隔互相冲突，则拒绝该配置版本，不静默修改用户规则。

### 3.3 配置版本

- 每次修改生成不可变 `rule_version`
- 新任务使用当前生效版本，已创建任务继续使用原版本
- 配置支持 `effective_at` 定时生效和一键回滚
- 每个任务记录规则版本、命中配额和计算出的执行时间
- 修改数量不会集中补发旧版本未执行的积压任务
- 只有配置文件或管理接口可以创建规则版本，Agent 无权修改
- 分析结果不会自动扩量、降频或改变时间窗

### 3.4 费用计量

`budget_mode: unlimited` 时，费用不参与放行决策：

- 每次 API 调用仍记录端点、资源数、估算费用和实际费用
- X Usage 与本地账本定期对账
- 价格目录过期只触发告警，不停止任务
- 余额不足时 API 无法继续，系统暂停对应操作并告警
- 可配置费用告警，但告警不会自动降低 `per_day`

价格和资源数仅用于可观测性，调度器只按照当前 `rule_version` 执行。

## 4. Agent 职责

采用一个 `Supervisor` 编排职责单一的 Agent。Agent 只产生结构化结果，不持有 X token、不能修改生效规则，也不能直接调用 X API。

“评论”在本设计中专指对 X 帖子的 Reply。自助和按量付费层只处理原作者明确召唤账号的场景，不包含主动搜索陌生帖子后发表评论。

这些是逻辑 Agent，通过同一个 LLM Gateway 路由到本地或远程模型，不要求每个 Agent 对应独立进程。

| X 动作 | 负责 Agent | 执行组件 | 状态 |
| --- | --- | --- | --- |
| 发布普通动态 | `Post Writer Agent` | `Post Publisher` | 启用 |
| 回复账号提及 | `Reply Writer Agent` | `Reply Publisher` | 仅召唤场景启用 |
| 主动评论陌生帖子 | 无 | 无 | 禁止 |
| 点赞、转发、关注 | 无 | 无 | 禁止 |
| 私信 | 无 | 无 | 禁止 |
| 采集指标 | `Metrics Analyst Agent` | `Metrics Collector` | 按规则启用 |

### 4.1 编排 Agent

| Agent | 输入 | 输出 | 边界 |
| --- | --- | --- | --- |
| `Supervisor Agent` | 定时事件、策略版本、队列状态 | 工作流 DAG、任务优先级 | 不生成正文，不调用外部 API |
| `Strategy Agent` | 账号定位、历史指标、动作规则 | 周策略、内容配比、实验假设 | 不能突破政策和生效规则 |

`Supervisor Agent` 只负责任务路由和失败处置。具体内容必须由对应的专业 Agent 生成，避免一个 Agent 同时决定“发什么”和“是否发送”。

### 4.2 动态内容 Agent

| Agent | 输入 | 输出 | 边界 |
| --- | --- | --- | --- |
| `Research Agent` | 白名单信息源 | 带来源、时间和哈希的素材卡片 | 不读取 X 时间线 |
| `Topic Agent` | 素材卡片、内容支柱 | 候选选题、评分、目标受众问题 | 不写最终正文 |
| `Post Writer Agent` | 选题、语气、来源、内容策略 | 两个动态候选稿 | 不决定发布时间 |
| `Post Editor Agent` | 候选稿、核验与风控结果 | 最终动态正文 | 不改变事实结论 |

### 4.3 评论 Agent

| Agent | 输入 | 输出 | 边界 |
| --- | --- | --- | --- |
| `Mention Triage Agent` | 已通过召唤资格检查的提及 | 问题、反馈、合作、骚扰、无需回复 | 不读取未召唤帖子 |
| `Reply Decision Agent` | 提及、分类、对话上下文 | `reply/ignore/escalate` 及理由 | 默认忽略低价值或敏感会话 |
| `Reply Writer Agent` | 原帖、线程上下文、账号语气 | 一个回复候选稿 | 不新增线程外用户，不带推广链接 |
| `Conversation Guard Agent` | 原帖、回复稿、参与者 | 上下文一致性、冒犯和泄露风险 | 风险不明时直接阻断 |

评论规则：

- 原帖作者必须明确 `@` 提及账号，或引用账号的帖子
- 只能提及当前线程已经存在的参与者
- 回复数量、执行时间和同用户冷却由 `actions.publish_reply` 配置
- 不回复政治、医疗、法律、金融建议、骚扰或身份不明的敏感请求
- 不发送私信、不主动拉群、不包含外部 URL 或营销话术
- 没有高质量回复时选择 `ignore`，不为完成指标强行评论

### 4.4 风控 Agent

| Agent | 输入 | 输出 | 边界 |
| --- | --- | --- | --- |
| `Fact-check Agent` | 动态或回复、来源 | 逐条事实核验、证据引用 | 无来源的事实标记失败 |
| `Policy Agent` | 内容、平台与账号规则 | 风险等级、命中规则、阻断原因 | 只能收紧，不能放宽规则 |
| `Similarity Agent` | 内容、历史动态和回复 | 语义重复度、模板化风险 | 超阈值直接阻断 |

风控 Agent 之间相互独立。任何一个返回阻断，内容都不能进入执行队列。

### 4.5 分析 Agent

| Agent | 输入 | 输出 | 边界 |
| --- | --- | --- | --- |
| `Metrics Analyst Agent` | 账号、动态、回复指标 | 增粉归因、主题和时间窗表现 | 明确区分真实值与估算值 |
| `Experiment Agent` | 假设、实验组、样本量 | 继续、停止或扩大实验的建议 | 样本不足时不得宣布胜负 |

### 4.6 确定性执行服务

以下组件不是 Agent，不使用 LLM：

| 服务 | 职责 |
| --- | --- |
| `Summon Eligibility Gate` | 验证原作者是否明确提及账号或引用账号帖子 |
| `Content Approval Gate` | 校验 Schema、URL、字符数、禁止主题和风控结果 |
| `Rule Engine` | 加载规则版本并计算动作是否启用、配额和执行窗口 |
| `Quota Counter` | 原子统计分钟、小时和每日操作数量 |
| `Cost Meter` | 记录端点、资源数与费用，不参与放行 |
| `Scheduler` | 执行动态、回复和同主题冷却 |
| `Post Publisher` | 只负责创建普通动态 |
| `Reply Publisher` | 只负责对通过资格检查的帖子创建 Reply |
| `Metrics Collector` | 按动作规则读取账号、自有帖子和提及 |
| `OAuth Manager` | 保存密钥引用、刷新 token 和校验 scope |
| `Reconciler` | 对账不确定写入和 X Usage |

`Post Publisher` 与 `Reply Publisher` 使用不同任务类型、配额计数器和幂等状态，不能互相代替。

### 4.7 动态工作流

```text
schedule tick
  -> Research Agent
  -> Topic Agent
  -> Post Writer Agent
  -> Fact-check/Policy/Similarity Agents
  -> Post Editor Agent
  -> Content Approval Gate
  -> Rule Engine/Quota Counter
  -> Scheduler
  -> Post Publisher
  -> Reconciler
```

### 4.8 评论工作流

```text
mention sync
  -> Summon Eligibility Gate
  -> Mention Triage Agent
  -> Reply Decision Agent
  -> Reply Writer Agent
  -> Fact-check/Policy/Similarity/Conversation Guard Agents
  -> Content Approval Gate
  -> Rule Engine/Quota Counter
  -> reply cooldown
  -> Reply Publisher
  -> Reconciler
```

每个 Agent 设置最大执行次数、token 上限和超时。输出不符合 Schema 时最多修复一次，仍失败则放弃本次任务，不进入无限循环。

### 4.9 自动审批条件

动态和回复都必须满足：

- 所有事实性陈述均能映射到已保存来源或当前对话
- 风险评分和语义重复度低于阈值
- URL、媒体和帖子串符合当前 `content_policy`
- 不包含私信引导或未披露商业关系
- 不包含未经验证的数字、引语或第三方指控
- 长度符合账号能力和 X 限制
- 所有风控 Agent 与确定性检查均通过
- 当前策略、模型和提示词版本未被暂停

动态还必须属于允许的内容支柱。回复还必须通过召唤资格、线程参与者和回复冷却检查。未通过的内容直接丢弃，不降级绕过检查。

## 5. 信息源与内容策略

### 5.1 信息源

- 一级：账号所有者提供的原创文章、产品更新和项目记录
- 二级：官方博客、论文、RSS、发布说明和指定行业媒体
- 三级：允许自动访问的白名单公开网页

每条素材保存来源 URL、作者、发布时间、摘要、内容哈希、访问时间和许可类型。来源 URL 仅用于内部事实追踪，默认不写入 X 帖子。

浏览器 Worker 只能读取白名单外部信息源：

- 不登录或操作 `x.com`、`twitter.com`
- 使用一次性 Profile，不读取用户浏览器 Cookie
- 遵守来源网站的访问约束
- 网页内容按不可信输入处理，防止提示词注入

### 5.2 选题评分

```text
score =
  0.30 * audience_relevance +
  0.20 * account_authority +
  0.15 * novelty +
  0.15 * timeliness +
  0.10 * conversation_potential +
  0.10 * historical_fit -
  risk_penalty
```

低于阈值的选题不进入写作阶段。初始内容组合采用 `70/20/10`：

- 70% 已验证主题，维持稳定价值输出
- 20% 相邻主题或新表达形式
- 10% 高不确定实验

## 6. OAuth 与发布

### 6.1 OAuth

使用 OAuth 2.0 Authorization Code Flow with PKCE。首次启动由账号所有者授权，申请最小权限：

- `tweet.read`
- `tweet.write`
- `users.read`
- `offline.access`

`offline.access` 用于获得 refresh token，使系统在无人值守状态下刷新短期 access token。refresh token 和 client secret 保存到操作系统 Keychain，SQLite 只保存密钥引用、授权账号 ID、scope 和过期时间。

默认不申请 `follows.write`、`like.write`、`dm.write` 等互动权限。后续启用媒体时再单独评审并申请 `media.write`。

### 6.2 发布状态机

```text
idea -> drafted -> checking -> approved_auto
     -> rule_admitted -> scheduled -> publishing -> published
                                      \-> uncertain -> reconciling
                                      \-> rejected
                                      \-> failed
```

动态和回复共用状态机，但每条任务必须携带 `kind=post|reply`。回复任务还必须保存 `parent_post_id`、原作者 ID 和召唤证据。

调度器只处理 `approved_auto` 且通过当前规则的内容：

- 动态数量、时间窗和间隔来自 `actions.publish_post`
- 回复数量、时间窗和间隔来自 `actions.publish_reply`
- 同主题和同用户冷却分别从对应动作规则读取
- 发布前再次运行内容策略、重复度、时效、规则和平台限流检查
- 回复发布前重新验证召唤关系与线程参与者
- 正文哈希、策略版本、规则版本、模型版本和来源集合写入审计记录

### 6.3 不确定发布对账

网络超时不代表 X 未接收请求，直接重试可能产生重复帖子。因此：

1. 调用前持久化 `operation_id`、正文哈希、规则版本和计划时间。
2. 明确成功时保存 X 返回的帖子 ID。
3. 明确 `4xx` 时不重试，按错误类型暂停或丢弃。
4. 网络超时或连接中断时进入 `uncertain`，不立即再次创建帖子。
5. 预留一次读取成本，通过自己的帖子时间线核对发布时间和正文哈希。
6. 找到匹配项则转为 `published`；找不到则标记 `failed_ambiguous` 并告警。

默认不自动重试不确定的写操作，宁可少发一条，也不承担重复发布和重复计费风险。

### 6.4 错误处理

- `401`：刷新 token 后重试一次；再次失败则停机
- `403`：立即停机并告警，不改走浏览器
- `429`：遵守平台返回的重置时间，不切换应用规避限制
- `5xx`：仅对明确未提交的请求有限重试
- 连续 3 次失败：打开账号级熔断器
- 价格目录过期或用量对账异常：继续执行规则并告警
- 账户余额不足：暂停对应动作并告警

## 7. 指标与策略更新

### 7.1 配置化读取

读取任务完全由动作规则控制：

- `sync_account_metrics.every` 控制账号指标频率
- `sync_post_metrics.after_publish` 控制每条动态的采样时间点
- `sync_mentions.every` 控制提及同步频率
- `max_resources_per_day: null` 表示应用不限制资源数
- 关闭某个 action 后，不再创建对应读取任务

读取费用持续计量，但不会自动改变频率。X 返回 `429` 时，任务延后到平台重置时间。

### 7.2 增粉归因

X 不一定提供单条帖子直接带来的关注事件，因此使用时间窗估算：

```text
post_attributed_follows =
  interval_net_follows
  * post_impressions_weight
  * profile_action_weight
  * recency_decay
```

归因值只用于相对比较。日报同时展示真实净增粉丝、估算贡献、未归因部分和置信度。

### 7.3 策略调权

```text
performance =
  0.45 * normalized_follow_gain +
  0.25 * quality_engagement +
  0.15 * saves_or_bookmarks +
  0.15 * profile_actions -
  negative_feedback_penalty
```

策略更新频率由 `actions.update_strategy` 配置。任何调权不得突破禁止主题、动作规则和平台限制。

## 8. 系统架构

```text
Local Web Console
    |
Go API
    |-- Account & Strategy
    |-- Source & Topic
    |-- Agent Orchestrator
    |-- Mention & Reply Workflow
    |-- Policy & Approval Gate
    |-- Scheduler & Cooldown
    |-- Rule Engine & Quota Counter
    |-- Cost Meter
    |-- OAuth, Post & Reply Publishers
    |-- Analytics & Audit
    |
SQLite ---- Local Job Scheduler ---- Supervisor
                                      |-- Agents -> LLM Gateway
                                      |-- Research Worker -> approved sources
                                      |-- Deterministic Gates
                                      |-- Cost Ledger
                                      |-- X API Executor
                                                        |
                                                        +-- X OAuth 2.0
                                                        +-- Post Publisher
                                                        +-- Reply Publisher
                                                        +-- Own Posts/Mentions
```

模块划分：

```text
cmd/server
internal/account
internal/source
internal/topic
internal/content
internal/engagement
internal/agent
internal/llm
internal/orchestrator
internal/workflow
internal/approval
internal/rules
internal/quota
internal/scheduler
internal/cooldown
internal/metering
internal/oauth
internal/xapi
internal/publisher
internal/replier
internal/analytics
internal/experiment
internal/policy
internal/audit
```

### 8.1 关键数据表

| 表 | 核心字段 |
| --- | --- |
| `accounts` | id, x_user_id, handle, timezone, status |
| `credential_refs` | owner_type, owner_id, kind, keychain_ref, expires_at |
| `provider_profiles` | name, protocol, base_url, key_ref, config, state |
| `model_catalogs` | provider_profile, model, capabilities, pricing |
| `llm_usage` | agent_run_id, provider, model, tokens, latency, cost |
| `strategy_versions` | account_id, audience, voice, pillars, version |
| `pricing_catalogs` | version, currency, prices, confirmed_at |
| `rule_versions` | account_id, version, config, effective_at, state |
| `action_counters` | account_id, action, period, count, rule_version |
| `cost_ledger` | operation_id, endpoint, resources, estimated, settled |
| `sources` | type, url, trust_level, license, enabled |
| `source_items` | source_id, canonical_url, content_hash, published_at |
| `topics` | title, score_parts, risk_score, status |
| `content_items` | topic_id, body, body_hash, state, strategy_version |
| `content_sources` | content_id, source_item_id, claim_refs |
| `schedules` | action, content_id, rule_version, publish_at, status |
| `mentions` | x_post_id, author_id, summon_type, context, state |
| `reply_decisions` | mention_id, decision, reason, risk_score |
| `replies` | mention_id, content_id, x_reply_id, state, published_at |
| `interaction_cooldowns` | account_id, target_user_id, next_allowed_at |
| `api_operations` | operation_id, type, rule_version, request_hash, state, attempts |
| `x_posts` | content_id, x_post_id, published_at |
| `metric_snapshots` | account_id, x_post_id, captured_at, metrics |
| `experiments` | hypothesis, variable, groups, start_at, end_at |
| `workflow_runs` | type, state, strategy_version, rule_version, cost, started_at |
| `agent_runs` | workflow_id, agent_type, input_ref, output_ref, status |
| `audit_logs` | actor, action, resource, before, after, created_at |

## 9. 安全与可靠性

- OAuth token、client secret 和远程模型 API Key 只保存在操作系统 Keychain
- 日志过滤 Authorization、token、Cookie 和私信内容
- Agent 不能访问凭证、规则写接口或 X API 客户端
- 所有外部写操作必须通过政策、规则、配额和冷却闸门
- API 操作使用持久化 outbox，进程重启后从确定状态恢复
- 规则决策、请求、响应和费用结算可审计
- 全局暂停、账号熔断和平台限流独立生效
- 数据库每日备份，本地备份不包含明文凭证
- 浏览器 Worker 失效不影响 OAuth 发布器

## 10. 合规边界

- 仅通过 X 官方 API 执行 X 内写操作
- 不保存 X 密码，不通过浏览器模拟操作账号
- 不执行批量、激进或垃圾互动
- 仅回复原作者明确提及账号或引用账号帖子的召唤场景
- 回复只能提及当前线程参与者，不主动引入其他用户
- 不跨账号发布相同或近似内容
- 不补发积压内容形成突发发布
- 平台政策发生变化时先停机，再更新硬约束
- 平台价格变化时更新计量目录并告警，不自动修改动作规则

参考：

- [X Developer Policy](https://developer.x.com/en/developer-terms/policy)
- [X Automation Rules](https://help.x.com/en/rules-and-policies/x-automation)
- [X Platform Manipulation and Spam](https://help.x.com/en/rules-and-policies/platform-manipulation)
- [X API Pricing](https://docs.x.com/x-api/getting-started/pricing)
- [X API Create Post](https://docs.x.com/x-api/posts/create-post)
- [X API Get Posts](https://docs.x.com/x-api/posts/get-posts-by-ids)
- [X API Usage](https://docs.x.com/x-api/usage/introduction)
- [X API Reply Restrictions](https://devcommunity.x.com/t/x-api-v2-update-addressing-llm-generated-spam/257909)
- [X OAuth 2.0 Authorization Code Flow with PKCE](https://docs.x.com/fundamentals/authentication/oauth-2-0/authorization-code)

## 11. MVP 路线

### 第一阶段：自动发布

- 单账号策略和内容支柱配置
- LLM Gateway、Provider Adapter 和多 Agent 工作流
- 确定性自动审批
- OAuth 2.0 PKCE 与 refresh token
- X Create Post 发布器
- 版本化规则引擎、Quota Counter 和可行性校验
- SQLite outbox、冷却、熔断、费用计量和审计

验收：

- 连续 7 天无人值守运行
- 示例规则 `publish_post.quota.per_day: 10` 被准确执行
- 时间窗、间隔、队列和溢出策略符合配置
- 无重复帖子和未授权操作
- 每条帖子可追溯到来源、策略、规则和模型版本

### 第二阶段：评论自动化

- 配置化账号提及同步
- `Summon Eligibility Gate`
- `Mention Triage`、`Reply Decision` 和 `Reply Writer` Agent
- `Conversation Guard` 与配置化同用户冷却
- 独立 `Reply Publisher` 和回复动作规则

验收：

- 未召唤帖子无法进入评论 Agent
- 回复不会引入线程外用户、URL 或营销话术
- 评论失败不会影响普通动态发布
- 动态与评论分别计费、熔断和审计

### 第三阶段：增长闭环

- 每日账号快照和自有帖子表现快照
- 增粉归因、实验报告和策略调权
- X Usage 与本地费用账本对账
- 不确定发布自动核对
- 规则执行率、积压和费用报表

验收：

- 所有动作频率均能追溯到生效规则版本
- 能回答哪些主题和时间窗具有更高增粉效率
- 读取任务按配置启停和调整频率

### 第四阶段：可控扩展

- 原创媒体生成和 `media.write` 动作规则
- 多模型评估和提示词版本回放
- 多账号租户隔离和独立规则
- 更细粒度的异常检测和运营告警

## 12. 上线标准

- 账号定位、受众、禁止主题和信息源白名单已配置
- 动作配置 Schema、可行性校验、定时生效和回滚经过测试
- OAuth scope 与设计一致，refresh token 轮换经过验证
- 召唤资格、线程参与者和同用户评论冷却经过测试
- URL/媒体策略、自动审批和规则版本切换经过测试
- 超时、`401`、`403`、`429`、`5xx` 和进程重启场景经过测试
- 不确定发布不会被直接重试
- 网页提示词注入和 Agent 越权调用经过测试
- 全局暂停、账号熔断和平台限流可独立触发

业务验证至少运行四周，以周为周期观察净增粉丝和每千次曝光增粉，不能用单条爆款判断系统有效。
