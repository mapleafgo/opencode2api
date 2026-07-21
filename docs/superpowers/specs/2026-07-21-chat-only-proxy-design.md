# 设计：仅保留 OpenAI Chat Completions 代理

日期：2026-07-21  
状态：已批准（待实现）

## 背景

`opencode2api` 当前作为本地 HTTP 代理，同时兼容三套客户端协议：

- OpenAI Chat Completions：`POST /v1/chat/completions`
- OpenAI Responses：`POST /v1/responses`
- Anthropic Messages：`POST /v1/messages`

实际上游统一转发到 OpenCode 的 Chat Completions 接口（Zen / Go）。Responses 与 Anthropic 入口仅为有限兼容层，维护成本高、覆盖不完整。决定剔除多协议支持，只做 Chat Completions 代理。

## 目标

- 对外只暴露 **OpenAI Chat Completions** 兼容入口
- 删除 Responses / Anthropic Messages 客户端协议相关代码、测试与文档
- 保留现有 Chat 能力：模型别名、reasoning/thinking 兼容、SSE 处理、鉴权选 Zen/Go、SOCKS5、token 统计、管理面板
- 保留 Chat 路径上 **上游偶发 Anthropic 响应 → OpenAI** 的兜底转换

## 非目标

- 不为 Responses / Anthropic 提供兼容层、重定向或迁移适配
- 不改变 OpenCode 上游对接 URL 与会话头逻辑
- 不借此机会对 `main.go` 做全局分层重构
- 不弱化 Chat 路径上的别名 / reasoning 映射等现有本地处理（「瘦身后的 Chat 代理」，非极简透传）

## 决策

采用 **直接删除**（方案 A）：

- 移除 `/v1/responses`、`/v1/messages` 路由注册与全部仅服务于这两条路径的实现
- 访问已删除路由时依赖 Go `net/http` 默认 **404**，不提供 410 废弃期

未选方案 B（先 410 再删）：用户已明确要剔除，中间态价值有限。  
未选方案 C（只摘路由保留 dead code）：与降低维护成本的目标相反。

## 对外 API 面

### 保留

| 路由 | 方法 | 说明 |
| --- | --- | --- |
| `/v1/chat/completions` | `POST` | OpenAI Chat Completions 兼容入口 |
| `/v1/models` | `GET` | 模型列表 |
| `/health` | `GET` | 健康检查 |
| `/` | `GET` | 管理面板 |
| `/login` / `/logout` | `POST`/`GET` | 管理登录登出 |
| `/api/config` | `GET`/`POST` | 配置读写（需管理鉴权） |
| `/api/stats` | `GET`/`DELETE` | 用量统计（需管理鉴权） |
| `/api/reload` | `POST` | 刷新上游会话与模型列表（需管理鉴权） |

### 删除

| 路由 | 方法 | 说明 |
| --- | --- | --- |
| `/v1/responses` | `POST` | OpenAI Responses 兼容入口 |
| `/v1/messages` | `POST` | Anthropic Messages 兼容入口 |

鉴权语义（`Bearer public` / 裸 key / `zen:` / `go:`）保持不变，仅作用于保留的 Chat 与 models 路由。

## 数据流（收敛后）

```text
Client (OpenAI Chat Completions JSON / SSE)
  → POST /v1/chat/completions
  → 本地处理（model_alias、reasoning_effort、thinking、鉴权模式）
  → OpenCode zen 或 go 的 /v1/chat/completions
  → 若上游返回 Anthropic 形态则转换为 OpenAI Chat 响应
  → 返回 OpenAI Chat 响应（JSON 或 SSE）
```

## 代码删除边界

### 删除（客户端协议）

包括但不限于：

- 路由：`http.HandleFunc("/v1/responses", ...)`、`http.HandleFunc("/v1/messages", ...)`
- Responses：`responsesHandler`、`responsesStreamHandler`、`convertChatToResponses`、`responsesInputToMessages`、Responses 工具/状态/内容转换等仅被该路径使用的函数与类型
- Anthropic Messages 客户端：`claudeMessagesHandler`、`claudeStreamHandler`、`claudeToOpenAIMessages`、`openAIToClaudeResponse` 等仅被该路径使用的函数与类型
- 专用测试文件：`responses_*.go`、`claude_messages_usage_test.go` 等

### 保留（Chat 链路）

- `chatCompletionsHandler` 及上游转发
- 上游响应兜底：`isAnthropicFormat`、`parseAnthropicSSE`、`convertAnthropicToOpenAI` 等
- Chat 共用结构：`OpenAIRequest`、`Message`、`Tool`、模型解析、代理、鉴权、统计、管理面板

### 删除时的约束

- 禁止误删 Chat 与上游兜底共用的类型或函数
- 删除后 `go test ./...`、`go vet ./...` 必须通过
- 不引入新依赖

## 文档变更

- `README.md`：功能列表与描述改为仅 Chat Completions
- `docs/API.md`：删除 Responses / Anthropic 章节与路由表项；流式说明仅保留 Chat
- 其他文档中若提及多协议，一并收敛
- `CHANGELOG.md`：记录破坏性变更（移除 `/v1/responses`、`/v1/messages`）

## 测试与验收

1. 现有 Chat / 模型 / 配置相关测试继续通过
2. Responses / Claude 专用测试文件删除后不再被构建
3. `/v1/responses` 与 `/v1/messages` 返回 404
4. `/v1/chat/completions` 与 `/v1/models` 行为相对当前主分支无功能回退（别名、鉴权、SSE 等）
5. `go test ./...` 与 `go vet ./...` 通过

## 风险与缓解

| 风险 | 缓解 |
| --- | --- |
| 依赖 Responses/Anthropic 入口的客户端立刻失效 | 预期破坏性变更；文档与 CHANGELOG 明确说明 |
| 误删 Chat 共用代码导致编译/运行失败 | 按「仅被 Responses/Claude 引用」原则删除；全量测试与 vet |
| 文档残留多协议描述 | 以 README + docs/API 为主路径统一修订 |

## 实现顺序（概要）

1. 删除路由注册与 Responses/Claude 客户端实现
2. 删除专用测试文件，修复因共用符号删除导致的编译问题
3. 更新 README、API 文档、CHANGELOG
4. 运行 `go test ./...`、`go vet ./...` 验收

## 成功标准

- 仓库中不再对外提供 `/v1/responses` 与 `/v1/messages`
- 仅 Chat Completions 作为 AI 请求协议入口
- 文档与实现一致，测试通过
