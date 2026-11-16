package gookies

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
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
