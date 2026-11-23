//go:build integration

package gookies

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests require chrome to be running in debug mode on the port as shown below.
const hostport = "http://127.0.0.1:9222"
const targetURL = "https://www.google.com/"
const invalidURL = "https://xgrooglex.com/"

func TestGetSession_Works(t *testing.T) {
	session, err := GetSession(t.Context(), hostport, targetURL)
	require.NoError(t, err, "Is chrome running in debug mode at %s?", hostport)
	assert.Greater(t, len(session.Cookies), 0)
	assert.Greater(t, len(session.UserAgent), 0)
}

func TestGetSession_FailsNoURL(t *testing.T) {
	session, err := GetSession(t.Context(), hostport, invalidURL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no cookies for")
	assert.Emptyf(t, session.Cookies, "Expected cookies to be empty when an error is returned.")
	assert.Empty(t, session.UserAgent, "Expected user agent to be empty when an error is returned.")
}
