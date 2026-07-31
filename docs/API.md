# API 兼容说明

服务默认监听 `http://127.0.0.1:8000`，客户端不需要传入真实 OpenAI API key，但可以通过 `Authorization` 指定 OpenCode 上游模式。

## 鉴权与上游选择

- 无 `Authorization`，或 `Bearer public`
  - 走 public Zen 免费模型。
  - `/v1/models` 只返回 `-free` 模型和免费别名。
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
| `/v1/models` | `GET` | 返回上游模型和本地别名模型 |
| `/v1/chat/completions` | `POST` | OpenAI Chat Completions 兼容入口 |
| `/health` | `GET` | 健康检查 |
| `/api/config` | `GET`/`POST` | 管理面板配置接口 |
| `/api/stats` | `GET`/`DELETE` | token 统计接口 |
| `/api/reload` | `POST` | 刷新 OpenCode 会话和模型列表 |

`GET /v1/models` 的返回会随鉴权模式变化：

- `public` 只显示免费 Zen 模型。
- 默认或 `zen:` 模式显示 Zen 目录。
- `go:` 模式显示 Go 目录，并附带 public 可用的免费模型。

## Chat Completions

支持常见字段：

- `model`
- `messages`
- `stream`
- `extra_body`
- `tools`
- `tool_choice`

`model` 会先经过 `model_alias` 解析；`messages`、`tools`、`tool_choice` 保持原始 JSON 透传。`custom` 工具是 opencode 上游不支持的形态，出站前会做最小适配：工具声明转成 `function`，强制 `tool_choice` 降级为 `auto`，assistant 历史里的 custom `tool_calls` 转成 `function`；其余字段不改写。

## 流式响应

`stream: true` 时服务原样透传上游 SSE，仅解析 `usage` 用于 token 统计，不清除 `reasoning_content`，也不重建消息。
