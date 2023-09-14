package gookies

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

// These tests require chrome to be running in debug mode on the port as shown below.
const hostport = "http://127.0.0.1:9222"
const invalidHostport = "http://127.0.0.1:1222"
const website = "google.com"
const invalidWebsite = "xgrooglex.com"

func TestGetCookies_Works(t *testing.T) {
	if cookies, err := GetCookies(hostport, website); err != nil {
		assert.Nil(t, err, "Is you browser running in debug mode @ %v with a \"%v\" tab open?", hostport, website)
	} else {
		assert.Greater(t, len(cookies), 0)
	}
}

func TestGetCookies_FailsNoChrome(t *testing.T) {
	cookies, err := GetCookies(invalidHostport, website)
	assert.NotNil(t, err)
	assert.Emptyf(t, cookies, "Expected cookies to be empty when an error is returned.")
}

func TestGetCookies_FailsNoWebsite(t *testing.T) {
	cookies, err := GetCookies(hostport, invalidWebsite)
	assert.NotNil(t, err)
	assert.Emptyf(t, cookies, "Expected cookies to be empty when an error is returned.")
}
