# X AI Operations 使用说明

本文说明如何配置账号与模型、运行 dry-run、完成 X OAuth 2.0 PKCE 授权，
以及切换到真实 X API。

## 1. 环境要求

- Go 1.25 或更高版本
- macOS：真实 X 登录的 refresh token 当前保存到 macOS Keychain
- 一个可用的本地或远程 LLM
- 真实运行时需要 X Developer App，以及支持所需 API 的 X API 套餐
- 建议安装 `curl` 和 `jq`，用于执行本文的 API 示例

首次运行前执行：

```bash
go test ./...
```

## 2. 配置文件

系统使用两份配置：

| 文件 | 用途 |
| --- | --- |
| `config/default.yaml` | 运行端口、数据库、账号、模型、X OAuth、计量和备份 |
| `config/rules.yaml` | 内容定位、互动边界、动作频率、时间窗和冷却规则 |

账号 handle 和时区应在两份文件中保持一致：

```yaml
# config/default.yaml
account:
  id: "default"
  handle: "@your_handle"
  timezone: "Asia/Shanghai"
```

```yaml
# config/rules.yaml
account:
  handle: "@your_handle"
  timezone: "Asia/Shanghai"
```

同时根据账号定位调整 `audience`、`promise`、`voice`、内容支柱和禁止主题。

`config/rules.yaml` 中的 `config.effective_at` 决定规则生效时间。未来时间会进入 `scheduled`；
当前或过去时间会立即生效。时间必须包含时区偏移。

## 3. 配置模型

### 3.1 OpenAI-compatible 本地模型

默认 Profile 指向本机：

```yaml
llm:
  default_profile: "local"
  profiles:
    local:
      protocol: "openai_compatible"
      base_url: "http://127.0.0.1:11434/v1"
      model: "your-installed-model"
      location: "local"
      capabilities: ["json_object"]
```

将 `model` 改为模型服务实际暴露的模型名，并确保 `/v1/models` 和
`/v1/chat/completions` 可访问。

### 3.2 远程模型

远程 API Key 不写入 YAML，只写 Keychain：

```bash
read -s LLM_API_KEY
security add-generic-password \
  -U \
  -s xops \
  -a llm-remote \
  -w "${LLM_API_KEY}"
unset LLM_API_KEY
```

然后增加远程 Profile：

```yaml
llm:
  profiles:
    remote:
      protocol: "openai_compatible"
      base_url: "https://provider.example.com/v1"
      api_key_ref: "keychain://xops/llm-remote"
      model: "provider-model"
      location: "remote"
      capabilities: ["json_schema"]
  routing:
    default: ["local", "remote"]
```

支持的协议名为 `openai_compatible`、`ollama_native`、`anthropic` 和
`gemini`。开发环境的 LLM Key 也可使用 `env://VARIABLE_NAME`，生产环境
应使用 Keychain。

## 4. 配置信息源

自动内容工作流需要可信素材。当前 Collector 支持 HTTPS RSS/Atom：

```yaml
sources:
  - name: "Official Engineering Blog"
    feed_url: "https://example.com/feed.xml"
    enabled: true
```

没有配置信息源时，系统不会凭空创建自动选题。外部内容只作为不可信素材，
Agent 仍需经过事实、政策、相似度和确定性审批。

## 5. 启动 dry-run

保持：

```yaml
runtime:
  dry_run: true
```

启动服务：

```bash
go run ./cmd/xops server -config ./config/default.yaml
```

打开控制台：

<http://127.0.0.1:8787>

检查状态：

```bash
curl -fsS http://127.0.0.1:8787/api/v1/status | jq
```

dry-run 会执行真实的规则、Agent、审批、配额、调度和 Operation 状态机，
但发布使用本地 DryRun Adapter，不会调用 X 写接口。

### 手动生成一条 dry-run 内容

先从状态接口获取当前 CSRF Token：

```bash
CSRF="$(
  curl -fsS http://127.0.0.1:8787/api/v1/status |
    jq -r '.csrf_token'
)"
```

提交一条带来源的素材：

```bash
curl -fsS \
  -X POST \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: ${CSRF}" \
  -d '{
    "evidence": [
      {
        "title": "Example release",
        "summary": "A verified summary used to generate the post.",
        "source_url": "https://example.com/release"
      }
    ],
    "recent_content": []
  }' \
  http://127.0.0.1:8787/api/v1/dry-run/posts | jq
```

该请求需要已配置的模型。生成结果仍必须通过所有审批门。

## 6. 创建 X Developer App

在 X Developer Portal 中为自己的账号创建 Project/App，并配置 OAuth 2.0：

1. 启用 OAuth 2.0 Authorization Code Flow with PKCE。
2. 使用不依赖 client secret 的 PKCE/public-client 配置。
3. 设置完全一致的回调地址：

   ```text
   http://127.0.0.1:8787/api/v1/oauth/callback
   ```

4. 申请以下 scope：

   ```text
   tweet.read tweet.write users.read offline.access
   ```

5. 保存 App 的 OAuth 2.0 Client ID。

回调地址、协议、端口或路径任何一处不一致，X 都会拒绝回调。

## 7. 登录自己的 X 账号

修改 `config/default.yaml`：

```yaml
runtime:
  dry_run: false

account:
  handle: "@your_handle"

x:
  oauth_redirect_url: "http://127.0.0.1:8787/api/v1/oauth/callback"
  client_id: "YOUR_OAUTH2_CLIENT_ID"
  credential_ref: "keychain://xops/oauth-refresh"
```

同步修改 `config/rules.yaml` 中的 handle，然后重启服务。

获取授权 URL：

```bash
CSRF="$(
  curl -fsS http://127.0.0.1:8787/api/v1/status |
    jq -r '.csrf_token'
)"

AUTH_URL="$(
  curl -fsS \
    -X POST \
    -H "X-CSRF-Token: ${CSRF}" \
    http://127.0.0.1:8787/api/v1/oauth/start |
    jq -r '.authorization_url'
)"

open "${AUTH_URL}"
```

在打开的 X 页面中登录目标账号并确认授权。成功后浏览器会回到本地控制台，
系统将 refresh token 写入 Keychain，access token 只保存在进程内存中。

可以确认 Keychain 条目存在，但不要输出 token：

```bash
security find-generic-password \
  -s xops \
  -a oauth-refresh >/dev/null &&
  echo "X OAuth credential is stored"
```

OAuth state 默认 10 分钟过期。页面停留过久或服务重启后，应重新调用
`/api/v1/oauth/start`，不要复用旧授权 URL。

## 8. 切换真实运行前检查

至少完成 7 天 dry-run，并确认：

- `publish_post` 每日槽位和最小间隔符合规则
- 没有集中补发、重复发布或跨时间窗发布
- 回复只来自原作者明确召唤或引用账号帖子的场景
- Provider 故障时能回退，非法 JSON 不进入发布队列
- 不确定写入进入对账，不直接重试
- 账号暂停后不再获取写入配额
- 告警、审计、费用账本和备份正常产生

真实运行会产生 X API 和模型服务费用。`metering.x_unit_cost` 只用于本地估算，
不会限制动作执行。

## 9. 控制台与数据

控制台提供：

- 账号暂停和恢复
- 当前规则版本与运行模式
- Job 状态、尝试次数和错误
- 内容审批与发布状态
- Provider Profile
- 开放告警和审计事件

默认本地文件：

```text
var/xops.db
var/backups/
```

这些运行文件已被 Git 忽略。OAuth token 和远程模型 API Key 不进入数据库或
备份。

## 10. 常见问题

### 状态中没有 active rule

检查 `config/rules.yaml` 的 `config.effective_at`。若它在未来，规则会保持
`scheduled`，直到设定时间自动激活。

### Provider 连接失败

确认模型进程已启动、`base_url` 和 `model` 正确，并检查 Keychain 引用。
可调用：

```bash
CSRF="$(
  curl -fsS http://127.0.0.1:8787/api/v1/status |
    jq -r '.csrf_token'
)"

curl -fsS \
  -X POST \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: ${CSRF}" \
  -d '{"name":"local"}' \
  http://127.0.0.1:8787/api/v1/providers/validate | jq
```

### OAuth 返回 invalid or expired

授权 state 已过期、被消费或服务已重启。重新获取授权 URL，并在 10 分钟内
完成授权。

### 修改接口返回 403

CSRF Token 会随服务重启而变化。重新读取 `/api/v1/status` 后再调用修改接口。

### 没有自动生成内容

检查是否存在 Active Rule、有效 Provider、已启用信息源，以及
`topic_generate` 和 `publish_post` 的时间窗。没有可信素材时系统会跳过生成，
不会强行编造内容。

### 收到 X 429

系统会保存速率限制的 reset 时间，并将可重试任务延后到 reset 之后。不要通过
切换账号或 App 绕过平台限制。
