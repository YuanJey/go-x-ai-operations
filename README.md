# X AI Operations

面向单个 X 账号的全自动、规则驱动运营系统。内容生产和分析在本地运行，
发布与指标读取使用 X 官方 API。

系统自动完成：

- Supervisor 编排内容、评论、风控和分析 Agent
- Post Writer Agent 生成动态，Reply Writer Agent 处理明确召唤账号的提及
- 内容查重、品牌语气、来源和平台政策检查
- 自动审批低风险动态和合规回复并按计划发布
- 按配置采集指标，归因增粉效果并调整内容策略
- OAuth token 刷新、失败对账、规则配额和平台限流处理

所有支持的操作均可配置：

- 动态数量，例如 `10 条/天`
- 合规回复数量、提及同步和指标采集频率
- 时间窗、最小间隔、并发数、积压策略和失败策略
- 配置变更版本、启用时间和回滚版本

系统不执行自动关注、点赞、转发、私信或未召唤的陌生帖子回复，不通过浏览器脚本
替代 API。费用持续记录但不限制执行，实际调用仍受 X 余额、速率限制和平台规则约束。

默认技术栈为 Go、SQLite、Provider-agnostic LLM Gateway 和本地任务调度。
模型可使用本地 Ollama/vLLM/LocalAI，也可使用远程服务商。系统仅在首次
OAuth 授权、策略变更或异常停机时需要人工介入。

模型接入优先使用 OpenAI-compatible API；不兼容该协议的服务商通过独立
Provider Adapter 接入。

## 文档

- [使用说明与 X OAuth 登录](docs/usage.md)
- [产品与规则设计](docs/design.md)
- [系统架构设计](docs/architecture.md)

## 核心原则

1. 以有效净增粉丝为北极星指标。
2. Agent 只生成结构化决策，外部写操作由确定性服务执行。
3. 所有操作先经过版本化规则、政策和平台限流闸门。
4. 使用 X 官方 API，不模拟鼠标、复用 Cookie 或规避平台限制。
5. 凭证异常、平台拒绝或发布结果不确定时默认停止。

## 当前实现

- Go 单进程、本地 HTTP 控制台和 SQLite WAL 持久化
- 不可变规则版本、定时激活、回滚、槽位计算和原子配额
- SQLite Jobs、Lease 恢复、队列溢出、Transactional Outbox 和审计
- OpenAI-compatible、Ollama Native、Anthropic、Gemini Provider Adapter
- Agent JSON Schema 校验、一次修复调用、运行记录和模型回退
- 动态选题、写作、事实核验、政策、相似度、编辑和自动审批工作流
- 召唤资格、提及去重、回复决策、对话保护和独立 Reply Publisher
- OAuth 2.0 PKCE、macOS Keychain、X API Adapter 和 Rate Limit Store
- Post/Reply Operation 状态机、不确定写入对账和 dry-run Adapter
- 账号/动态指标、策略建议、费用账本、告警和 SQLite 完整性备份

## 本地运行

要求 Go 1.25 或更高版本。

```bash
go test ./...
go run ./cmd/xops server -config ./config/default.yaml
```

默认以 `dry_run` 运行，控制台地址：

<http://127.0.0.1:8787>

账号、模型、信息源、X Developer App 和 OAuth 登录的完整配置步骤见
[使用说明](docs/usage.md)。

运行数据写入 `./var/xops.db`，该目录已被 Git 忽略。默认规则位于
[`config/rules.yaml`](config/rules.yaml)，运行配置位于
[`config/default.yaml`](config/default.yaml)。

默认 Provider 指向 `http://127.0.0.1:11434/v1`。需要将
`llm.profiles.local.model` 改为实际模型名；也可以增加远程 Profile，并用
`keychain://service/account` 引用 API Key。开发环境还支持 `env://NAME`，
但生产配置应使用 OS Keychain。

## 接入真实 X

1. 将 `runtime.dry_run` 改为 `false`。
2. 设置 `x.client_id`、OAuth 回调地址和 Keychain refresh token 引用。
3. 在 X Developer Portal 配置完全一致的 OAuth 回调地址。
4. 启动服务后，通过控制台对应 API 发起 PKCE 授权。
5. 确认 scope 包含 `tweet.read`、`tweet.write`、`users.read` 和
   `offline.access`。

系统不会把 token、API Key、X 密码或浏览器 Cookie 写入 SQLite。

## 上线闸门

代码已提供完整 dry-run 链路，但真实账号上线前仍必须完成架构文档定义的
7 天无人值守验证，重点确认 `10 条/天` 无突发补发、无重复发布、召唤回复
边界、Provider 故障回退和不确定写入对账。该验证需要真实模型和目标环境，
不应由单次本地测试替代。
