package gookies

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDomainMatches(t *testing.T) {
	for _, tc := range []struct {
		domain, website string
		want            bool
	}{
		{"example.com", "example.com", true},
		{".example.com", "example.com", true},    // parent-domain cookie
		{"app.example.com", "example.com", true}, // subdomain cookie
		{".example.com", "app.example.com", true},
		{"Example.com", "example.com", true},
		{"notexample.com", "example.com", false},
		{"example.com.evil.io", "example.com", false},
		{"google.com", "example.com", false},
		{"", "example.com", false},
	} {
		assert.Equal(t, tc.want, domainMatches(tc.domain, tc.website), "%q vs %q", tc.domain, tc.website)
	}
}

func TestActivePortPath(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	dir := filepath.Join(config, "google-chrome")
	assert.NoError(t, os.MkdirAll(dir, 0o755))
	assert.NoError(t, os.WriteFile(filepath.Join(dir, "DevToolsActivePort"),
		[]byte("9222\n/devtools/browser/c3059a2e\n"), 0o644))

	path, ok := activePortPath("9222")
	assert.True(t, ok)
	assert.Equal(t, "/devtools/browser/c3059a2e", path)

	_, ok = activePortPath("9333")
	assert.False(t, ok, "a different port must not match")
}

func TestBrowserWebSocketURL_BadHostport(t *testing.T) {
	_, err := browserWebSocketURL(t.Context(), "127.0.0.1:9222")
	assert.Error(t, err)
}

func TestGetSession_FailsNoChrome(t *testing.T) {
	session, err := GetSession(t.Context(), "http://127.0.0.1:1222", "google.com")
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "no chrome debugging at")
	assert.Emptyf(t, session.Cookies, "Expected cookies to be empty when an error is returned.")
	assert.Empty(t, session.UserAgent, "Expected user agent to be empty when an error is returned.")
}

func TestFromVersion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		wantURL string
		wantErr string
	}{
		{
			name:    "valid",
			status:  http.StatusOK,
			body:    `{"webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/browser/abc"}`,
			wantURL: "ws://127.0.0.1:9222/devtools/browser/abc",
		},
		{
			name:    "http error",
			status:  http.StatusInternalServerError,
			body:    "boom",
			wantErr: "500",
		},
		{
			name:    "invalid json",
			status:  http.StatusOK,
			body:    "{",
			wantErr: "/json/version:",
		},
		{
			name:    "missing url",
			status:  http.StatusOK,
			body:    `{}`,
			wantErr: "no webSocketDebuggerUrl",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/json/version", r.URL.Path)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			got, err := fromVersion(t.Context(), srv.URL)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantURL, got)
		})
	}
}

func TestBrowserWebSocketURL_FallbackActivePort(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)

	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	dir := filepath.Join(config, "google-chrome")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "DevToolsActivePort"),
		[]byte(u.Port()+"\n/devtools/browser/fallback-id\n"), 0o644))

	wsURL, err := browserWebSocketURL(t.Context(), srv.URL)
	require.NoError(t, err)
	assert.Equal(t, "ws://"+u.Host+"/devtools/browser/fallback-id", wsURL)
}

func TestCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()

		for {
			var req struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if err := wsjson.Read(r.Context(), conn, &req); err != nil {
				return
			}
			switch req.Method {
			case "Test.zeroID":
				// Event with no "id" and a non-matching id must both be skipped.
				_ = wsjson.Write(r.Context(), conn, map[string]any{"method": "Target.targetCreated"})
				_ = wsjson.Write(r.Context(), conn, map[string]any{"id": 99, "result": map[string]any{"value": "wrong"}})
				_ = wsjson.Write(r.Context(), conn, map[string]any{"id": req.ID, "result": map[string]any{"value": "ok"}})
			case "Test.cdpError":
				_ = wsjson.Write(r.Context(), conn, map[string]any{"id": req.ID, "error": map[string]any{"message": "bad method"}})
			}
		}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.Dial(t.Context(), wsURL, nil)
	require.NoError(t, err)
	defer conn.CloseNow()

	var res struct {
		Value string `json:"value"`
	}
	require.NoError(t, call(t.Context(), conn, 0, "Test.zeroID", &res))
	assert.Equal(t, "ok", res.Value)

	err = call(t.Context(), conn, 1, "Test.cdpError", &res)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad method")
}

func TestGetSession_MockCDP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/version":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"webSocketDebuggerUrl":"ws://` + r.Host + `/devtools/browser/mock"}`))
		case "/devtools/browser/mock":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()

			for {
				var req struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
				}
				if err := wsjson.Read(r.Context(), conn, &req); err != nil {
					return
				}
				switch req.Method {
				case "Storage.getCookies":
					_ = wsjson.Write(r.Context(), conn, map[string]any{
						"id": req.ID,
						"result": map[string]any{
							"cookies": []map[string]string{
								{"name": "a", "value": "1", "domain": ".example.com"},
								{"name": "b", "value": "2", "domain": "app.example.com"},
								{"name": "c", "value": "3", "domain": "other.com"},
							},
						},
					})
				case "Browser.getVersion":
					_ = wsjson.Write(r.Context(), conn, map[string]any{
						"id": req.ID,
						"result": map[string]any{
							"userAgent": "TestChrome/1.0",
						},
					})
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	session, err := GetSession(t.Context(), srv.URL, "example.com")
	require.NoError(t, err)
	assert.Equal(t, "a=1; b=2", session.Cookies)
	assert.Equal(t, "TestChrome/1.0", session.UserAgent)

	session, err = GetSession(t.Context(), srv.URL, "missing.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no cookies for missing.com")
	assert.Empty(t, session)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = GetSession(ctx, srv.URL, "example.com")
	require.Error(t, err)
	_, _, err = fetch(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/devtools/browser/mock")
	require.ErrorIs(t, err, context.Canceled)
}
