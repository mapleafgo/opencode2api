package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestVersionStringIncludesBuildMetadata(t *testing.T) {
	oldVersion, oldCommit, oldDate := version, commit, date
	t.Cleanup(func() {
		version, commit, date = oldVersion, oldCommit, oldDate
	})

	version = "v1.2.3"
	commit = "abc1234"
	date = "2026-06-04T00:00:00Z"

	got := versionString()
	for _, want := range []string{"opencode2api", "v1.2.3", "abc1234", "2026-06-04T00:00:00Z"} {
		if !strings.Contains(got, want) {
			t.Fatalf("versionString() = %q, want it to contain %q", got, want)
		}
	}
}

type fakeUpstreamResponse struct {
	status int
	body   string
	header http.Header
}

// fakeTransport 记录转发出去的原始请求和请求头，返回预设响应。
type fakeTransport struct {
	t         *testing.T
	responses []fakeUpstreamResponse
	requests  []*http.Request
	rawBodies []string
}

func (f *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.requests = append(f.requests, req)

	raw, err := io.ReadAll(req.Body)
	if err != nil {
		f.t.Fatalf("read request body: %v", err)
	}
	f.rawBodies = append(f.rawBodies, string(raw))

	if len(f.responses) == 0 {
		f.t.Fatalf("unexpected request to %s", req.URL.String())
	}
	next := f.responses[0]
	f.responses = f.responses[1:]
	header := next.header
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode: next.status,
		Header:     header.Clone(),
		Body:       io.NopCloser(strings.NewReader(next.body)),
		Request:    req,
	}, nil
}

func installFakeOpenCodeClient(t *testing.T, responses []fakeUpstreamResponse) *fakeTransport {
	t.Helper()

	oldHTTPClient := httpClient
	oldModelsCache := modelsCache
	oldGoModelsCache := goModelsCache
	oldModelsLoaded := modelsLoaded
	oldOCClientVer := ocClientVer
	oldOCSessionID := ocSessionID
	oldOCProjectID := ocProjectID
	oldActiveSocks5 := activeSocks5
	oldSocks5Client := socks5Client
	oldSocks5ClientAddr := socks5ClientAddr

	transport := &fakeTransport{
		t:         t,
		responses: append([]fakeUpstreamResponse(nil), responses...),
	}
	httpClient = &http.Client{Transport: transport}

	modelMu.Lock()
	modelsCache = []ModelInfo{{ID: "deepseek-v4-flash-free"}}
	goModelsCache = []ModelInfo{{ID: "glm-5.2"}}
	modelsLoaded = true
	modelMu.Unlock()

	socks5Mu.Lock()
	activeSocks5 = ""
	socks5Client = nil
	socks5ClientAddr = ""
	socks5Mu.Unlock()

	ocOnce = sync.Once{}
	ocOnce.Do(func() {})
	ocClientVer = "test-version"
	ocSessionID = "ses_test"
	ocProjectID = "project_test"

	t.Cleanup(func() {
		httpClient = oldHTTPClient
		modelMu.Lock()
		modelsCache = oldModelsCache
		goModelsCache = oldGoModelsCache
		modelsLoaded = oldModelsLoaded
		modelMu.Unlock()
		socks5Mu.Lock()
		activeSocks5 = oldActiveSocks5
		socks5Client = oldSocks5Client
		socks5ClientAddr = oldSocks5ClientAddr
		socks5Mu.Unlock()
		ocOnce = sync.Once{}
		ocClientVer = oldOCClientVer
		ocSessionID = oldOCSessionID
		ocProjectID = oldOCProjectID
	})

	return transport
}

// TestChatCompletionsRawPassthrough 验证请求体和响应体均原样透传、不做任何改写。
func TestChatCompletionsRawPassthrough(t *testing.T) {
	transport := installFakeOpenCodeClient(t, []fakeUpstreamResponse{
		{status: http.StatusOK, body: `{"id":"chatcmpl_test","choices":[]}`},
	})

	clientBody := `{
		"model":"primary-model",
		"messages":[{"role":"user","content":"x"}],
		"max_completion_tokens":2048,
		"reasoning_effort":"high",
		"thinking":{"type":"enabled"}
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(clientBody))
	req.Header.Set("Authorization", "Bearer sk-validkey0123456789abcdef")
	rec := httptest.NewRecorder()

	chatCompletionsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != `{"id":"chatcmpl_test","choices":[]}` {
		t.Fatalf("response body = %q, want upstream passthrough", rec.Body.String())
	}
	if len(transport.rawBodies) != 1 {
		t.Fatalf("forwarded requests = %d, want 1", len(transport.rawBodies))
	}
	// 请求体必须与客户端原始字节完全一致，不做字段重写。
	if transport.rawBodies[0] != clientBody {
		t.Fatalf("forwarded body = %q, want identical client body %q", transport.rawBodies[0], clientBody)
	}
	if got := transport.requests[0].Header.Get("Authorization"); got != "Bearer sk-validkey0123456789abcdef" {
		t.Fatalf("Authorization = %q, want passthrough of client key", got)
	}
}

// TestChatCompletionsStreamPassthrough 验证 SSE 流原样透传、不做 [DONE]/usage 处理。
func TestChatCompletionsStreamPassthrough(t *testing.T) {
	upstreamSSE := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: {\"usage\":{\"total_tokens\":9}}\n\ndata: [DONE]\n\n"
	transport := installFakeOpenCodeClient(t, []fakeUpstreamResponse{
		{
			status: http.StatusOK,
			header: http.Header{"Content-Type": []string{"text/event-stream"}},
			body:   upstreamSSE,
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"primary-model","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	rec := httptest.NewRecorder()

	chatCompletionsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream passthrough", got)
	}
	if rec.Body.String() != upstreamSSE {
		t.Fatalf("streamed body = %q, want verbatim passthrough", rec.Body.String())
	}
	if len(transport.rawBodies) != 1 {
		t.Fatalf("forwarded requests = %d, want 1", len(transport.rawBodies))
	}
}

// TestChatForwardsUpstreamErrorRaw 验证上游错误状态与响应体原样透传、不标准化。
func TestChatForwardsUpstreamErrorRaw(t *testing.T) {
	transport := installFakeOpenCodeClient(t, []fakeUpstreamResponse{
		{status: http.StatusBadRequest, body: `{"error":{"message":"ctx too small","type":"invalid_request_error","code":"context_length_exceeded"}}`},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"primary-model","messages":[{"role":"user","content":"x"}]}`))
	rec := httptest.NewRecorder()

	chatCompletionsHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if rec.Body.String() != `{"error":{"message":"ctx too small","type":"invalid_request_error","code":"context_length_exceeded"}}` {
		t.Fatalf("response body = %q, want upstream error passthrough", rec.Body.String())
	}
	if len(transport.requests) != 1 {
		t.Fatalf("upstream requests = %d, want 1 (no retry/fallback)", len(transport.requests))
	}
}

// TestForwardUpstreamRoutesByAuthAndModel 验证目录路由只取决于认证模式与模型。
func TestForwardUpstreamRoutesByAuthAndModel(t *testing.T) {
	tests := []struct {
		name    string
		auth    UpstreamAuth
		modelID string
		wantURL string
	}{
		{
			name:    "public stays on zen surface",
			auth:    UpstreamAuth{Mode: AuthRoutePublic},
			modelID: "deepseek-v4-flash-free",
			wantURL: "https://opencode.ai/zen/v1/chat/completions",
		},
		{
			name:    "go prefix sends shared model to go surface",
			auth:    UpstreamAuth{Mode: AuthRouteGo, Token: "sk-gokey"},
			modelID: "glm-5.2",
			wantURL: "https://opencode.ai/zen/go/v1/chat/completions",
		},
		{
			name:    "auto key sends go-only model to go surface",
			auth:    UpstreamAuth{Mode: AuthRouteAuto, Token: "sk-autokey"},
			modelID: "kimi-k2.7-code",
			wantURL: "https://opencode.ai/zen/go/v1/chat/completions",
		},
		{
			name:    "zen prefix forces zen surface for shared model",
			auth:    UpstreamAuth{Mode: AuthRouteZen, Token: "sk-zenkey"},
			modelID: "glm-5.2",
			wantURL: "https://opencode.ai/zen/v1/chat/completions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := installFakeOpenCodeClient(t, []fakeUpstreamResponse{
				{status: http.StatusOK, body: `{"id":"chatcmpl_test","choices":[]}`},
			})
			oldModelsCache := modelsCache
			oldGoModelsCache := goModelsCache
			modelMu.Lock()
			modelsCache = []ModelInfo{
				{ID: "glm-5.2"},
				{ID: "deepseek-v4-flash-free"},
			}
			goModelsCache = []ModelInfo{
				{ID: "glm-5.2"},
				{ID: "kimi-k2.7-code"},
			}
			modelMu.Unlock()
			t.Cleanup(func() {
				modelMu.Lock()
				modelsCache = oldModelsCache
				goModelsCache = oldGoModelsCache
				modelMu.Unlock()
			})

			body := `{"model":"` + tt.modelID + `","messages":[]}`
			resp, err := forwardUpstream([]byte(body), tt.auth.shouldUseGoEndpoint(tt.modelID), tt.auth)
			if err != nil {
				t.Fatalf("forwardUpstream() error = %v", err)
			}
			defer resp.Body.Close()

			if got := transport.requests[0].URL.String(); got != tt.wantURL {
				t.Fatalf("URL = %q, want %q", got, tt.wantURL)
			}
			wantAuth := "Bearer public"
			if tt.auth.Mode != AuthRoutePublic {
				wantAuth = "Bearer " + tt.auth.Token
			}
			if got := transport.requests[0].Header.Get("Authorization"); got != wantAuth {
				t.Fatalf("Authorization = %q, want %q", got, wantAuth)
			}
			if got := transport.requests[0].Header.Get("x-opencode-session"); got != "ses_test" {
				t.Fatalf("x-opencode-session = %q, want ses_test", got)
			}
			if transport.rawBodies[0] != body {
				t.Fatalf("forwarded body = %q, want %q (no model injection)", transport.rawBodies[0], body)
			}
		})
	}
}

func TestListModelsHandlerSeparatesPublicZenAndGoCatalogs(t *testing.T) {
	oldModelsCache := modelsCache
	oldGoModelsCache := goModelsCache
	oldModelsLoaded := modelsLoaded
	modelMu.Lock()
	modelsCache = []ModelInfo{
		{ID: "deepseek-v4-flash-free"},
		{ID: "glm-5.2"},
		{ID: "gpt-5.5"},
	}
	goModelsCache = []ModelInfo{
		{ID: "glm-5.2"},
		{ID: "kimi-k2.7-code"},
	}
	modelsLoaded = true
	modelMu.Unlock()
	t.Cleanup(func() {
		modelMu.Lock()
		modelsCache = oldModelsCache
		goModelsCache = oldGoModelsCache
		modelsLoaded = oldModelsLoaded
		modelMu.Unlock()
	})

	tests := []struct {
		name       string
		authHeader string
		wantIDs    []string
	}{
		{
			name:    "public only sees free zen models",
			wantIDs: []string{"deepseek-v4-flash-free"},
		},
		{
			name:       "bare zen key sees zen catalog only",
			authHeader: "Bearer sk-auto0123456789abcdef",
			wantIDs:    []string{"deepseek-v4-flash-free", "glm-5.2", "gpt-5.5"},
		},
		{
			name:       "go prefix sees free and go catalog",
			authHeader: "Bearer go:sk-go0123456789abcdef",
			wantIDs:    []string{"deepseek-v4-flash-free", "glm-5.2", "kimi-k2.7-code"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()

			listModelsHandler(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("listModelsHandler() status = %d, want %d", rec.Code, http.StatusOK)
			}
			var payload struct {
				Data []ModelInfo `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("unmarshal models response: %v", err)
			}
			gotIDs := make([]string, 0, len(payload.Data))
			for _, model := range payload.Data {
				gotIDs = append(gotIDs, model.ID)
			}
			if !reflect.DeepEqual(gotIDs, tt.wantIDs) {
				t.Fatalf("listModelsHandler() ids = %#v, want %#v", gotIDs, tt.wantIDs)
			}
		})
	}
}

func TestExtractUpstreamAuthKeyValidation(t *testing.T) {
	tests := []struct {
		name       string
		authHeader string
		wantMode   AuthRouteMode
		wantToken  string
	}{
		{"no header", "", AuthRoutePublic, ""},
		{"bearer empty", "Bearer ", AuthRoutePublic, ""},
		{"bearer public", "Bearer public", AuthRoutePublic, ""},
		{"bearer no-key-required placeholder", "Bearer no-key-required", AuthRoutePublic, ""},
		{"bearer random non-key", "Bearer abc123xyz", AuthRoutePublic, ""},
		{"valid sk key", "Bearer sk-validkey0123456789abcdef", AuthRouteAuto, "sk-validkey0123456789abcdef"},
		{"go prefix with sk key", "Bearer go:sk-gokey0123456789abcdef", AuthRouteGo, "sk-gokey0123456789abcdef"},
		{"zen prefix with sk key", "Bearer zen:sk-zenkey0123456789abcdef", AuthRouteZen, "sk-zenkey0123456789abcdef"},
		{"go prefix with placeholder falls to public", "Bearer go:no-key-required", AuthRoutePublic, ""},
		{"bare sk- with no suffix is invalid", "Bearer sk-", AuthRoutePublic, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			auth := extractUpstreamAuth(req)
			if auth.Mode != tt.wantMode {
				t.Fatalf("mode = %v, want %v", auth.Mode, tt.wantMode)
			}
			if auth.Token != tt.wantToken {
				t.Fatalf("token = %q, want %q", auth.Token, tt.wantToken)
			}
		})
	}
}
