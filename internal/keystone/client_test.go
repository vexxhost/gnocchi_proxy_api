package keystone

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yaguang-tang/gnocchi-proxy-api/internal/config"
)

func TestValidateTokenCachesSubjectToken(t *testing.T) {
	t.Parallel()

	var authCalls atomic.Int32
	var validateCalls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v3/auth/tokens":
			authCalls.Add(1)
			w.Header().Set("X-Subject-Token", "service-token")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token": map[string]any{
					"expires_at": time.Now().Add(time.Hour).Format(time.RFC3339),
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v3/auth/tokens":
			validateCalls.Add(1)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token": map[string]any{
					"expires_at": time.Now().Add(time.Hour).Format(time.RFC3339),
					"user":       map[string]any{"id": "user-a"},
					"project":    map[string]any{"id": "project-a"},
					"roles":      []map[string]any{{"name": "member"}},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := New(config.KeystoneConfig{
		AuthURL:           server.URL,
		Username:          "svc",
		Password:          "secret",
		ProjectName:       "service",
		UserDomainName:    "Default",
		ProjectDomainName: "Default",
		AdminRoles:        []string{"admin"},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	for range 2 {
		authCtx, err := client.ValidateToken(context.Background(), "user-token")
		if err != nil {
			t.Fatalf("validate token: %v", err)
		}
		if authCtx.ProjectID != "project-a" {
			t.Fatalf("unexpected project id %q", authCtx.ProjectID)
		}
	}

	if authCalls.Load() != 1 {
		t.Fatalf("expected one service auth call, got %d", authCalls.Load())
	}
	if validateCalls.Load() != 1 {
		t.Fatalf("expected one validation call, got %d", validateCalls.Load())
	}
}
