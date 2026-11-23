package gookies

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// These tests require chrome to be running in debug mode on the port as shown below.
const hostport = "http://127.0.0.1:9222"
const invalidHostport = "http://127.0.0.1:1222"
const website = "google.com"
const invalidWebsite = "xgrooglex.com"

func TestGetSession_Works(t *testing.T) {
	session, err := GetSession(t.Context(), hostport, website)
	assert.Nil(t, err, fmt.Sprintf("Is chrome running in debug mode at %s?", hostport))
	assert.Greater(t, len(session.Cookies), 0)
	assert.Greater(t, len(session.UserAgent), 0)
}

func TestGetSession_FailsNoChrome(t *testing.T) {
	session, err := GetSession(t.Context(), invalidHostport, website)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "no chrome debugging at")
	assert.Emptyf(t, session.Cookies, "Expected cookies to be empty when an error is returned.")
	assert.Empty(t, session.UserAgent, "Expected user agent to be empty when an error is returned.")
}

func TestGetSession_FailsNoWebsite(t *testing.T) {
	session, err := GetSession(t.Context(), hostport, invalidWebsite)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "no cookies for")
	assert.Emptyf(t, session.Cookies, "Expected cookies to be empty when an error is returned.")
	assert.Empty(t, session.UserAgent, "Expected user agent to be empty when an error is returned.")
}
