# API 兼容说明

服务默认监听 `http://127.0.0.1:8000`。`/v1/chat/completions` 做 API 流级透传：客户端请求体原样转发到 OpenCode 上游，上游响应（含 SSE 流）原样回传，不做任何请求体/响应体重写、模型回退或重试。

## 鉴权与上游选择

- 无 `Authorization`，或 `Bearer public`
  - 走 public Zen 免费模型。
  - `/v1/models` 只返回 `-free` 模型。
- `Bearer <opencode-api-key>`
  - 默认走 Zen。
  - 如果请求的是仅存在于 Go 目录中的模型，代理会自动切到 Go。
- `Bearer zen:<opencode-api-key>`
  - 强制走 Zen。
- `Bearer go:<opencode-api-key>`
  - 优先走 Go 订阅目录。
  - 对同时存在于 Zen 和 Go 的模型，也会按 Go 路径请求。

## 路由

| 路由 | 方法 | 说明 |
| --- | --- | --- |
| `/v1/models` | `GET` | 按鉴权模式返回模型目录 |
| `/v1/chat/completions` | `POST` | OpenAI Chat Completions 兼容透传入口 |
| `/health` | `GET` | 健康检查 |
| `/api/config` | `GET`/`POST` | 管理面板 SOCKS5 配置接口 |
| `/api/reload` | `POST` | 刷新 OpenCode 会话和模型列表 |

`GET /v1/models` 的返回会随鉴权模式变化：

- `public` 只显示免费 Zen 模型。
- 默认或 `zen:` 模式显示 Zen 目录。
- `go:` 模式显示 Go 目录，并附带 public 可用的免费模型。

## Chat Completions

透传模式下，请求体里的所有字段（`model`、`messages`、`stream`、`tools`、`reasoning_effort`、`max_completion_tokens` 等）一律原样转发，不做映射、占位补齐或字段重命名。代理只读取 `model` 字段用于目录路由与会话头注入。

## 流式响应

`stream: true` 时服务原样透传上游 SSE 字节，不做 `[DONE]` 注入、不去除 `reasoning_content`、不解析 `usage`、也不重建消息。
