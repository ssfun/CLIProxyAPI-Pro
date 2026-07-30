# OAuth Model Policy

`oauth-model-policy` 是 CLIProxyAPI Pro 的动态库插件，用于按 OAuth 账号的提供商和套餐，从该账号原本可用的模型集合中减去模型。Core 暴露的是通用 `AuthModelFilter` 能力，不包含任何提供商套餐知识。

## 生效顺序

账号模型注册按以下顺序处理：

1. upstream 全局或逐账号 `excluded_models`
2. `oauth-model-policy` 套餐规则
3. OAuth 模型 alias 与账号 prefix
4. 写入模型注册表

插件只能返回需要排除的现有模型 ID，不能增加模型或修改模型元数据。最终模型注册表同时决定 `/v1/models` 的聚合结果和请求调度时的账号候选集合。

## 提供商与套餐

内置规则键如下；每个提供商也支持配置任意自定义套餐键。

| 提供商 | 内置套餐键 | 缺少本地套餐时的来源 |
| --- | --- | --- |
| `xai` | `free`、`supergrok`、`x-premium-plus`、`supergrok-heavy`、`paid-unknown` | xAI billing API |
| `codex` | `free`、`plus`、`pro`、`pro-lite`、`team` | auth metadata / ID token |
| `claude` | `free`、`pro`、`max`、`team` | Anthropic OAuth profile API |
| `gemini-cli` | `free`、`legacy`、`standard`、`pro`、`ultra` | Google `loadCodeAssist` |
| `antigravity` | `free`、`pro`、`ultra`、`ultra-lite` | Google `loadCodeAssist` |
| `kimi` | 无稳定固定套餐键 | auth metadata；使用 UI 添加观察到的自定义套餐键 |

所有提供商都支持两个回退键：

- `_unknown`：套餐无法解析时使用
- `_default`：套餐已识别但没有更具体规则时使用

xAI 付费套餐按 billing 月限额映射：

- `free`
- `supergrok`：月限额 15000 cents
- `x-premium-plus`：月限额 20000 cents
- `supergrok-heavy`：月限额 150000 cents
- `paid-unknown`：已付费但限额未识别
插件先从 auth metadata、attributes 和 storage 中读取 `plan_type`、`planType`、`plan`、`package`、`tierId` 等字段及常见嵌套结构。Codex 还会解析 ID token 的 `chatgpt_plan_type`。本地信息缺失时，xAI、Claude、Gemini CLI 和 Antigravity 会通过 Core 的受控 HTTP callback 查询各自套餐接口。解析成功的结果按 `provider + auth ID` 缓存；临时探测失败时优先使用过期缓存，最后匹配 `_unknown`。探测失败不会中断模型注册。

## 配置示例

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    oauth-model-policy:
      enabled: true
      priority: 10
      cache-ttl: 30m
      resolve-timeout: 15s
      providers:
        xai:
          plans:
            free:
              excluded-models:
                - "grok-pro-*"
            supergrok:
              excluded-models:
                - "grok-4.5-*"
            x-premium-plus:
              excluded-models: []
            supergrok-heavy:
              excluded-models: []
            paid-unknown:
              excluded-models:
                - "grok-experimental-*"
            _unknown:
              excluded-models:
                - "grok-pro-*"
        codex:
          plans:
            free:
              excluded-models:
                - "gpt-5-pro*"
            team:
              excluded-models: []
        claude:
          plans:
            max:
              excluded-models: []
        gemini-cli:
          plans:
            free:
              excluded-models:
                - "gemini-*-pro*"
        antigravity:
          plans:
            ultra-lite:
              excluded-models:
                - "claude-opus-*"
        kimi:
          plans:
            enterprise:
              excluded-models: []
```

`excluded-models` 使用区分大小写无关的 Go `path.Match` 通配规则。模型 ID 通常不包含 `/`，可使用 `*`、`?` 和字符集合。

## 套餐探测

当本地没有套餐信息时，插件请求：

```text
GET https://cli-chat-proxy.grok.com/v1/billing
Authorization: Bearer <access_token>
x-xai-token-auth: xai-grok-cli
x-grok-client-version: 0.2.91
x-userid: <optional user id>
```

access token 与可选 user ID 仍由 Core 持有，插件通过绑定当前 auth 的 Host HTTP callback 发起请求，不自行创建绕过 Core transport policy 的网络客户端。

Claude 使用 `GET https://api.anthropic.com/api/oauth/profile`；Gemini CLI 与 Antigravity 使用各自的 Google `loadCodeAssist` 端点。所有请求都通过绑定当前 auth 的 Host HTTP callback，并受 `resolve-timeout` 限制。

## 构建

```bash
go test ./...
CGO_ENABLED=1 go build -buildmode=c-shared -o oauth-model-policy.so .
```

发布产物会按 `plugins/<goos>/<goarch>/oauth-model-policy.<ext>` 打包。Windows ARM64、FreeBSD 和 `_no-plugin` 产物暂不内置动态插件。
