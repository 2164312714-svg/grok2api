package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGenerateStatsigShape(t *testing.T) {
	value, err := generateStatsig("/rest/app-chat/conversations/new", "POST", statsigEpoch+12345)
	if err != nil {
		t.Fatalf("generateStatsig() error = %v", err)
	}
	if len(value) != 94 {
		t.Fatalf("encoded length = %d, want 94", len(value))
	}
	decoded, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	if len(decoded) != 70 {
		t.Fatalf("decoded length = %d, want 70", len(decoded))
	}
	if decoded[69]^decoded[0] != statsigMark {
		t.Fatal("signature marker mismatch")
	}
}

func TestRequireMethod(t *testing.T) {
	handler := requireMethod(http.MethodPost, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})

	allowed := httptest.NewRecorder()
	handler(allowed, httptest.NewRequest(http.MethodPost, "/sign", nil))
	if allowed.Code != http.StatusNoContent {
		t.Fatalf("allowed status = %d, want %d", allowed.Code, http.StatusNoContent)
	}

	rejected := httptest.NewRecorder()
	handler(rejected, httptest.NewRequest(http.MethodGet, "/sign", nil))
	if rejected.Code != http.StatusMethodNotAllowed || rejected.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("rejected status/allow = %d/%q", rejected.Code, rejected.Header().Get("Allow"))
	}
}
