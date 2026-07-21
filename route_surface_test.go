package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRemovedProtocolRoutesReturn404(t *testing.T) {
	mux := http.NewServeMux()
	registerRoutes(mux)
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
	mux := http.NewServeMux()
	registerRoutes(mux)
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
