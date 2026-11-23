//go:build integration

package gookies

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests require chrome to be running in debug mode on the port as shown below.
const hostport = "http://127.0.0.1:9222"
const domain = "google.com"
const invalidDomain = "xgrooglex.com"

func TestGetSession_Works(t *testing.T) {
	session, err := GetSession(t.Context(), hostport, domain)
	require.NoError(t, err, "Is chrome running in debug mode at %s?", hostport)
	assert.Greater(t, len(session.Cookies), 0)
	assert.Greater(t, len(session.UserAgent), 0)
}

func TestGetSession_FailsNoDomain(t *testing.T) {
	session, err := GetSession(t.Context(), hostport, invalidDomain)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no cookies for")
	assert.Emptyf(t, session.Cookies, "Expected cookies to be empty when an error is returned.")
	assert.Empty(t, session.UserAgent, "Expected user agent to be empty when an error is returned.")
}
