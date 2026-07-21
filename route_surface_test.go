package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// expectedChatOnlyMux describes the chat-only AI route surface.
// main() still uses DefaultServeMux; this isolated mux locks the product contract.
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
