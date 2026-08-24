package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestAppConfigSocks5PaidDirect(t *testing.T) {
	var cfg AppConfig
	if err := json.Unmarshal([]byte(`{"socks5_paid_direct":true}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Socks5PaidDirect {
		t.Fatal("socks5_paid_direct=true was not decoded")
	}

	oldPaidDirect := socks5PaidDirect
	t.Cleanup(func() {
		socks5Mu.Lock()
		socks5PaidDirect = oldPaidDirect
		socks5Mu.Unlock()
	})

	applyConfig(cfg)
	socks5Mu.RLock()
	got := socks5PaidDirect
	socks5Mu.RUnlock()
	if !got {
		t.Fatal("applyConfig did not enable paid direct")
	}
}

func TestGetHTTPClientForTierSocks5PaidDirectDefaultUsesProxy(t *testing.T) {
	oldHTTPClient := httpClient
	oldProxies := socks5Proxies
	oldActive := activeSocks5
	oldPaidDirect := socks5PaidDirect
	oldClient := socks5Client
	oldClientAddr := socks5ClientAddr
	t.Cleanup(func() {
		httpClient = oldHTTPClient
		socks5Mu.Lock()
		socks5Proxies = oldProxies
		activeSocks5 = oldActive
		socks5PaidDirect = oldPaidDirect
		socks5Client = oldClient
		socks5ClientAddr = oldClientAddr
		socks5Mu.Unlock()
	})

	directClient := &http.Client{}
	httpClient = directClient
	socks5Mu.Lock()
	socks5Proxies = []Socks5Proxy{{Addr: "127.0.0.1:1080", Name: "test"}}
	activeSocks5 = "127.0.0.1:1080"
	socks5PaidDirect = false
	socks5Client = nil
	socks5ClientAddr = ""
	socks5Mu.Unlock()

	paid := getHTTPClientForTier(TierPaid)
	free := getHTTPClientForTier(TierFree)
	if paid == directClient {
		t.Fatal("default socks5_paid_direct=false should send paid traffic through SOCKS5")
	}
	if free == directClient {
		t.Fatal("free traffic should use SOCKS5 when active_socks5 is set")
	}
	if paid != free {
		t.Fatal("paid and free should share the cached SOCKS5 client when paid_direct is false")
	}

	socks5Mu.Lock()
	socks5PaidDirect = true
	socks5Client = nil
	socks5ClientAddr = ""
	socks5Mu.Unlock()
	if getHTTPClientForTier(TierPaid) != directClient {
		t.Fatal("socks5_paid_direct=true should keep paid traffic on the direct client")
	}
	if getHTTPClientForTier(TierFree) == directClient {
		t.Fatal("free traffic should still use SOCKS5 when paid_direct is true")
	}
}

func TestAdminConfigHandlerReturnsSocks5PaidDirect(t *testing.T) {
	oldPaidDirect := socks5PaidDirect
	t.Cleanup(func() {
		socks5Mu.Lock()
		socks5PaidDirect = oldPaidDirect
		socks5Mu.Unlock()
	})

	socks5Mu.Lock()
	socks5PaidDirect = true
	socks5Mu.Unlock()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "/api/config", nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	adminConfigHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var cfg AppConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Socks5PaidDirect {
		t.Fatalf("socks5_paid_direct = %t, want true", cfg.Socks5PaidDirect)
	}
}

func TestBuildProxyClientPinsEgressConnection(t *testing.T) {
	client := buildProxyClient(Socks5Proxy{Addr: "127.0.0.1:1080"})
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport type = %T, want *http.Transport", client.Transport)
	}
	if transport.MaxIdleConnsPerHost != 2 {
		t.Fatalf("MaxIdleConnsPerHost = %d, want 2", transport.MaxIdleConnsPerHost)
	}
	if transport.IdleConnTimeout != 30*time.Minute {
		t.Fatalf("IdleConnTimeout = %s, want 30m", transport.IdleConnTimeout)
	}
}

func TestResolveConfigPathPrecedence(t *testing.T) {
	tmpDir := t.TempDir()
	localConfig := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(localConfig, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	userConfigRoot := filepath.Join(tmpDir, "user-config")
	switch runtime.GOOS {
	case "darwin":
		t.Setenv("HOME", tmpDir)
		userConfigRoot = filepath.Join(tmpDir, "Library", "Application Support")
	case "windows":
		t.Setenv("AppData", userConfigRoot)
	default:
		t.Setenv("XDG_CONFIG_HOME", userConfigRoot)
	}
	fallback := filepath.Join(userConfigRoot, "opencode2api", "config.json")

	tests := []struct {
		name      string
		env       string
		flagValue string
		explicit  bool
		want      string
	}{
		{
			name:      "environment wins over explicit flag",
			env:       "/tmp/env-config.json",
			flagValue: "/tmp/flag-config.json",
			explicit:  true,
			want:      "/tmp/env-config.json",
		},
		{
			name:      "explicit flag wins over local file",
			flagValue: "/tmp/flag-config.json",
			explicit:  true,
			want:      "/tmp/flag-config.json",
		},
		{
			name: "existing local file is backward compatible",
			want: "config.json",
		},
		{
			name:      "ignored flag value does not hide local file",
			flagValue: "/tmp/ignored.json",
			want:      "config.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env == "" {
				t.Setenv("OPENCODE2API_CONFIG", "")
			} else {
				t.Setenv("OPENCODE2API_CONFIG", tt.env)
			}

			if got := resolveConfigPath(tt.flagValue, tt.explicit); got != tt.want {
				t.Fatalf("resolveConfigPath() = %q, want %q", got, tt.want)
			}
		})
	}

	if err := os.Remove(localConfig); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENCODE2API_CONFIG", "")
	if got := resolveConfigPath("", false); got != fallback {
		t.Fatalf("user fallback = %q, want %q", got, fallback)
	}
}

func TestSaveConfigCreatesUserConfigDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode2api", "config.json")
	if err := saveConfig(path, AppConfig{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("saved config missing: %v", err)
	}
}

func TestRunHTTPServerStopsOnContextCancellation(t *testing.T) {
	var lc net.ListenConfig
	listener, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runHTTPServer(ctx, server, time.Second, listener)
	}()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runHTTPServer() = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server shutdown")
	}
	dialer := net.Dialer{Timeout: 100 * time.Millisecond}
	if _, err := dialer.DialContext(context.Background(), "tcp", listener.Addr().String()); err == nil {
		t.Fatal("listener is still accepting connections after shutdown")
	}
}

type fakeUpstreamResponse struct {
	status  int
	body    string
	header  http.Header
	bodyErr error
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
	body := io.NopCloser(strings.NewReader(next.body))
	if next.bodyErr != nil {
		body = &interruptedBody{data: next.body, err: next.bodyErr}
	}
	return &http.Response{
		StatusCode: next.status,
		Header:     header.Clone(),
		Body:       body,
		Request:    req,
	}, nil
}

type interruptedBody struct {
	data string
	err  error
	read bool
}

func (b *interruptedBody) Read(p []byte) (int, error) {
	if !b.read {
		b.read = true
		return copy(p, b.data), nil
	}
	return 0, b.err
}

func (b *interruptedBody) Close() error { return nil }

func TestChatCompletionsSSECopyErrorPreservesPassthrough(t *testing.T) {
	installFakeOpenCodeClient(t, []fakeUpstreamResponse{{
		status:  http.StatusOK,
		body:    "data: {\"choices\":[]}\n\n",
		header:  http.Header{"Content-Type": []string{"text/event-stream"}},
		bodyErr: errors.New("secret io: read/write on closed pipe"),
	}})

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer sk-validkey0123456789abcdef")
	rec := httptest.NewRecorder()
	chatCompletionsHandler(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "data: {\"choices\":[]}") {
		t.Fatalf("original SSE chunk missing: %q", body)
	}
	wantBody := "data: {\"choices\":[]}\n\n"
	if body != wantBody {
		t.Fatalf("body = %q, want passthrough only %q", body, wantBody)
	}
	if strings.Contains(body, "secret io") || strings.Contains(body, "closed pipe") {
		t.Fatalf("transport error leaked to client: %q", body)
	}
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
		{ID: "big-pickle"},
		{ID: "deepseek-v4-flash-free", Deprecated: true},
		{ID: "glm-5.2", PositiveInputCost: true},
		{ID: "gpt-5.5", PositiveInputCost: true},
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
			wantIDs: []string{"big-pickle"},
		},
		{
			name:       "bare zen key sees zen catalog only",
			authHeader: "Bearer sk-auto0123456789abcdef",
			wantIDs:    []string{"big-pickle", "deepseek-v4-flash-free", "glm-5.2", "gpt-5.5"},
		},
		{
			name:       "go prefix sees free and go catalog",
			authHeader: "Bearer go:sk-go0123456789abcdef",
			wantIDs:    []string{"big-pickle", "glm-5.2", "kimi-k2.7-code"},
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

func TestIsFreeModelFollowsOpenCodeCatalogRule(t *testing.T) {
	tests := []struct {
		name  string
		model ModelInfo
		want  bool
	}{
		{
			name:  "zero input cost without suffix is free",
			model: ModelInfo{ID: "big-pickle"},
			want:  true,
		},
		{
			name:  "positive input cost with free suffix is paid",
			model: ModelInfo{ID: "future-model-free", PositiveInputCost: true},
			want:  false,
		},
		{
			name:  "deprecated free model is disabled",
			model: ModelInfo{ID: "deepseek-v4-flash-free", Deprecated: true},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isFreeModel(tt.model); got != tt.want {
				t.Fatalf("isFreeModel(%q) = %v, want %v", tt.model.ID, got, tt.want)
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
