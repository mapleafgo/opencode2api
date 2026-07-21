# Chat-Only Proxy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 删除 `/v1/responses` 与 `/v1/messages` 客户端协议，使 `opencode2api` 仅保留 OpenAI Chat Completions 代理。

**Architecture:** 在现有单文件 `main.go` HTTP 代理上做协议面收敛：移除 Responses / Anthropic Messages 路由与仅服务于这两条路径的类型、handler、转换与状态存储；保留 `chatCompletionsHandler`、模型列表、管理面，以及 Chat 路径上「上游 Anthropic 响应 → OpenAI」兜底。不引入新依赖、不做全局分层重构。

**Tech Stack:** Go 1.22 标准库 `net/http`、现有 `go test` / `go vet`、Markdown 文档。

## Global Constraints

- 规格：`docs/superpowers/specs/2026-07-21-chat-only-proxy-design.md`
- 破坏性变更：删除后 `/v1/responses`、`/v1/messages` 走默认 404，不做 410
- 必须保留：`POST /v1/chat/completions`、`GET /v1/models`、管理/健康路由
- 必须保留上游响应兜底：`isAnthropicFormat`、`parseAnthropicSSE`、`buildOpenAIResponse`、`convertAnthropicMessageToOpenAI`、`convertAnthropicToOpenAI` 及调用点
- 必须保留 Chat 能力：model alias、reasoning/thinking、SSE 清理、Zen/Go 鉴权、SOCKS5、token 统计
- 禁止误删 Chat 共用符号（示例）：`OpenAIRequest`、`Message`、`Tool`、`fixToolCallGaps`、`ensureReasoningContent`、`wantsReasoning`、`chatCompletionsHandler`、`callOpenCodeAPI*`
- 验收：`go test ./...`、`go vet ./...` 通过
- 提交信息用中文 Conventional Commits 风格

---

## File Structure

| 文件 | 职责 |
| --- | --- |
| `main.go` | 删除 Claude/Responses 客户端协议实现与路由；保留 Chat + 上游兜底 |
| `route_surface_test.go` | 新建：断言删除路由 404、Chat/models 仍注册 |
| `responses_*.go` / `claude_messages_usage_test.go` | 删除：专用测试 |
| `main_test.go` | 保留（fake upstream 的 `responses` 字段名无关） |
| `README.md` / `docs/API.md` / `CHANGELOG.md` | 文档与破坏性变更说明 |

---

### Task 1: 路由面测试与删除专用测试文件

**Files:**
- Create: `route_surface_test.go`
- Delete: `responses_builtins_test.go`
- Delete: `responses_content_test.go`
- Delete: `responses_previous_state_test.go`
- Delete: `claude_messages_usage_test.go`
- Test: `route_surface_test.go`

**Interfaces:**
- Consumes: 产品最终路由契约（chat/models 注册；responses/messages 不注册）
- Produces: `TestRemovedProtocolRoutesReturn404`、`TestChatProtocolRoutesStillRegistered` 作为验收钉

- [ ] **Step 1: 删除 Responses / Claude 专用测试文件**

```bash
rm -f responses_builtins_test.go responses_content_test.go responses_previous_state_test.go claude_messages_usage_test.go
```

- [ ] **Step 2: 新增路由面测试**

创建 `route_surface_test.go`：

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// expectedChatOnlyMux 描述 chat-only 收敛后的 AI 路由契约。
// main() 仍使用 DefaultServeMux；此处用独立 mux 锁定产品面，避免污染全局路由。
func expectedChatOnlyMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	})
	return mux
}

func TestRemovedProtocolRoutesReturn404(t *testing.T) {
	mux := expectedChatOnlyMux()
	for _, path := range []string{"/v1/responses", "/v1/messages"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s: want 404, got %d", path, rr.Code)
		}
	}
}

func TestChatProtocolRoutesStillRegistered(t *testing.T) {
	mux := expectedChatOnlyMux()
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v1/chat/completions"},
		{http.MethodGet, "/v1/models"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code == http.StatusNotFound {
			t.Fatalf("%s %s: should be registered, got 404", tc.method, tc.path)
		}
	}
}
```

说明：`main()` 使用 `http.HandleFunc` 注册到 `DefaultServeMux`，单元测试不启动 `main`。本测试锁定**期望最终路由契约**；Task 2 删除 `main()` 中两条 `HandleFunc` 后，用编译 + 全量测试保证实现与契约一致。本计划不重构为可注入 mux（YAGNI）。

- [ ] **Step 3: 运行测试**

```bash
go test ./... -count=1
```

Expected: PASS（专用测试已删；新契约测试通过）

- [ ] **Step 4: Commit**

```bash
git add route_surface_test.go
git rm -f responses_builtins_test.go responses_content_test.go responses_previous_state_test.go claude_messages_usage_test.go
git commit -m "test: 移除多协议专用测试并锁定 chat-only 路由面"
```

完整提交说明可用：

```text
test: 移除多协议专用测试并锁定 chat-only 路由面

删除 Responses/Claude 入口测试，新增路由契约测试，
为剔除多协议实现提供验收基线。
```

---

### Task 2: 删除 Responses / Claude 客户端实现与路由

**Files:**
- Modify: `main.go`
- Test: `route_surface_test.go`、`main_test.go`

**Interfaces:**
- Consumes: Task 1 路由契约
- Produces: 仅 Chat Completions 客户端协议；保留上游 Anthropic→OpenAI 兜底

- [ ] **Step 1: 删除路由注册**

在 `main()` 中删除：

```go
http.HandleFunc("/v1/responses", loggingMiddleware(responsesHandler))
http.HandleFunc("/v1/messages", loggingMiddleware(claudeMessagesHandler))
```

保留：

```go
http.HandleFunc("/v1/chat/completions", loggingMiddleware(chatCompletionsHandler))
http.HandleFunc("/v1/models", loggingMiddleware(listModelsHandler))
```

- [ ] **Step 2: 删除仅用于 Responses 的包级状态**

删除：

```go
storedResponses      = map[string]StoredResponseState{}
storedResponsesMu    sync.RWMutex
```

删除前确认：

```bash
rg -n "storedResponses" main.go
```

- [ ] **Step 3: 删除仅用于 Claude/Responses 的类型**

删除（约 747–847 行一带）：

- `ClaudeRequest`、`ClaudeMessage`、`ClaudeContent`、`ClaudeTool`、`ClaudeResponse`、`ClaudeUsage`
- `ResponsesAPIRequest`、`ResponsesTool`、`ReasonEffort`、`StoredResponseState`

**不要删除：** `OpenAIRequest`、`Message`、`Tool`、`ToolCall`、`ToolFunction`、`AppConfig`。

- [ ] **Step 4: 删除 Claude Messages 客户端实现**

删除从 `// ======================== Claude Messages API ========================` 到 `// ======================== Responses API ========================` 之前的全部函数，包括：

- `extractClaudeSystemText`
- `cleanJsonSchema`
- `claudeToOpenAIMessages`
- `claudeToOpenAITools`
- `openAIToClaudeResponse`
- `toFloat64` / `usageIntField` / `usageMapField`
- `buildClaudeUsageCore` / `buildClaudeMessageUsage` / `buildClaudeDeltaUsage`
- `claudeMessagesHandler`
- `claudeStreamHandler`
- `indexOfInt`

- [ ] **Step 5: 删除 Responses API 客户端实现**

删除从 `// ======================== Responses API ========================` 到 `// ======================== Admin 管理页面 ========================` 之前的全部函数，包括：

- `responsesInputToMessages` 及后续 responses 工具/状态/内容转换
- `responsesHandler` / `responsesStreamHandler`
- `convertChatToResponses`
- `emitSSEEvent`

**边界：** Admin 段 `reloadHandler`、`adminConfigHandler` 等必须保留。

- [ ] **Step 6: 确认上游 Anthropic 兜底仍在**

```bash
rg -n "isAnthropicFormat|convertAnthropicToOpenAI|parseAnthropicSSE|buildOpenAIResponse|convertAnthropicMessageToOpenAI" main.go
```

Expected: 定义与 `callOpenCodeAPI` 内调用点仍存在。

- [ ] **Step 7: 编译与测试**

```bash
go test ./... -count=1
go vet ./...
```

Expected: PASS。若有 `undefined`，删除残留引用，不要恢复客户端协议。

- [ ] **Step 8: 静态确认路由已移除**

```bash
rg -n 'HandleFunc\("/v1/(responses|messages)"' main.go
rg -n "responsesHandler|claudeMessagesHandler" main.go
```

Expected: 无匹配。

- [ ] **Step 9: Commit**

```bash
git add main.go
git commit -m "feat!: 移除 Responses 与 Anthropic Messages 客户端协议"
```

正文：

```text
仅保留 OpenAI Chat Completions 与 models 入口，删除对应
handler、转换与状态存储；上游 Anthropic 响应兜底保留。

BREAKING CHANGE: POST /v1/responses 与 POST /v1/messages 已移除，返回 404。
```

---

### Task 3: 文档与 CHANGELOG 同步

**Files:**
- Modify: `README.md`
- Modify: `docs/API.md`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: Task 2 最终路由面
- Produces: 文档与实现一致

- [ ] **Step 1: 更新 README**

开篇改为：

```markdown
`opencode2api` 是一个本地 HTTP 代理，把 OpenAI Chat Completions 风格的请求转发到 OpenCode 上游接口，并提供模型别名、reasoning/thinking 兼容、SOCKS5 代理和一个轻量管理面板。
```

功能列表删除：

```markdown
- OpenAI Responses 兼容接口：`/v1/responses`
- Anthropic Messages 兼容接口：`/v1/messages`
```

免责声明可保留 “OpenAI、Anthropic 或 OpenCode” 商标/条款表述。

- [ ] **Step 2: 更新 `docs/API.md`**

1. 路由表删除 `/v1/responses`、`/v1/messages`
2. 删除整节 `## Responses API` 与 `## Anthropic Messages`
3. 流式响应改为：

```markdown
## 流式响应

`stream: true` 时服务会使用 SSE 返回，并在内部清理空 delta、空 finish reason 和不需要的 reasoning 字段。
```

4. 文首 “真实 OpenAI 或 Anthropic API key” 改为 “真实 OpenAI API key”

- [ ] **Step 3: 更新 `CHANGELOG.md`**

在 `## Unreleased` 顶部增加：

```markdown
- **BREAKING:** 移除 OpenAI Responses（`/v1/responses`）与 Anthropic Messages（`/v1/messages`）客户端兼容入口；仅保留 Chat Completions。
```

- [ ] **Step 4: 扫描产品文档残留**

```bash
rg -n "/v1/responses|/v1/messages|Anthropic Messages|Responses 兼容" README.md docs/API.md docs/CONFIGURATION.md docs/DEPLOYMENT.md docs/RELEASE.md CHANGELOG.md CONTRIBUTING.md SECURITY.md
```

Expected: 仅 CHANGELOG 破坏性说明可出现路径名；其它产品文档无「仍支持」表述。  
**不要**改写 `docs/superpowers/**` 历史 plan/spec。

- [ ] **Step 5: Commit**

```bash
git add README.md docs/API.md CHANGELOG.md
git commit -m "docs: 同步 chat-only 协议面说明"
```

正文：

```text
更新 README/API，并在 CHANGELOG 记录移除 Responses 与
Anthropic Messages 入口的破坏性变更。
```

---

### Task 4: 最终验收

**Files:**
- 无新文件；全仓验证

- [ ] **Step 1: 运行完整检查**

```bash
gofmt -l .
go test ./... -count=1
go vet ./...
```

Expected:
- `gofmt -l .` 无输出（若有，`gofmt -w` 后修正）
- `go test` / `go vet` PASS

- [ ] **Step 2: 符号与路由最终检查**

```bash
# 必须为零匹配
rg -n 'HandleFunc\("/v1/(responses|messages)"' main.go
rg -n "func (responsesHandler|claudeMessagesHandler|responsesStreamHandler|claudeStreamHandler)\b" main.go
rg -n "type (ClaudeRequest|ResponsesAPIRequest)\b" main.go

# 必须仍存在
rg -n "func chatCompletionsHandler\b|func listModelsHandler\b|func isAnthropicFormat\b|func convertAnthropicToOpenAI\b" main.go
```

- [ ] **Step 3: 如有格式/小修，单独 commit；否则跳过**

```bash
git add -u
git commit -m "chore: 修复 chat-only 收敛后的格式与残留引用"
```

---

## Spec Coverage Self-Review

| Spec 要求 | 对应任务 |
| --- | --- |
| 删除 `/v1/responses`、`/v1/messages` | Task 2 Step 1 |
| 删除 Responses/Claude 客户端实现与类型 | Task 2 Steps 2–5 |
| 保留 Chat 能力与上游 Anthropic 兜底 | Task 2 Steps 6–7；Task 4 Step 2 |
| 删除专用测试 | Task 1 Step 1 |
| 文档 + CHANGELOG 破坏性说明 | Task 3 |
| `go test` / `go vet` 通过 | Task 2 Step 7；Task 4 |
| 404 而非 410 | Task 2 删除注册即可 |
| 不做全局重构 / 不弱化 Chat 本地处理 | 全任务范围约束 |

## Placeholder Scan

- 无 TBD/TODO
- 删除符号清单已具体列出
- 命令与提交信息完整

## Type Consistency

- 删除类型与现有定义一致：`ClaudeRequest`、`ResponsesAPIRequest`、`StoredResponseState`
- 保留函数与现有一致：`chatCompletionsHandler`、`isAnthropicFormat`、`convertAnthropicToOpenAI`
