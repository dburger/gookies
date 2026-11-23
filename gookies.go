// Package gookies reads cookies and the user-agent out of a running Chrome through
// the Chrome DevTools Protocol (CDP).
//
// Chrome can expose CDP in two ways, and both work:
//
//   - Started with --remote-debugging-port and a non-default --user-data-dir.
//     Chrome then serves the HTTP endpoints (/json/version and friends).
//   - Remote debugging switched on from chrome://inspect/#remote-debugging in
//     a running Chrome (144 and later). Chrome then serves only the browser
//     websocket, whose path it writes to DevToolsActivePort in the profile
//     directory, and asks you to allow each connection.
//
// Example usage:
//
//	session, err := gookies.GetSession(*chrome, *site)
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	req, err := http.NewRequest(http.MethodGet, *url, nil)
//	if err != nil {
//		log.Fatal(err)
//	}
//	req.Header.Set("Cookie", session.Cookies)
//	req.Header.Set("User-Agent", session.UserAgent)
//	res, err := http.DefaultClient.Do(req)
package gookies

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// timeout bounds a whole GetSession call, including the time you take to
// allow the connection when Chrome asks.
const timeout = time.Minute

// profileDirs are the profile directories searched for DevToolsActivePort,
// relative to the user config directory.
var profileDirs = []string{"google-chrome", "google-chrome-beta", "google-chrome-unstable", "chromium"}

// Session is what a request needs to look like it came from your Chrome.
type Session struct {
	// Cookies is "name=value; name=value", ready for a Cookie header.
	Cookies string
	// UserAgent is the User-Agent header Chrome sends.
	UserAgent string
}

// GetSession returns the cookies for website and Chrome's User-Agent,
// over a single connection (one prompt to allow it).
func GetSession(hostport, website string) (Session, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	wsURL, err := browserWebSocketURL(ctx, hostport)
	if err != nil {
		return Session{}, err
	}

	cookies, userAgent, err := fetch(ctx, wsURL)
	if err != nil {
		return Session{}, err
	}

	var pairs []string
	for _, c := range cookies {
		if domainMatches(c.Domain, website) {
			pairs = append(pairs, c.Name+"="+c.Value)
		}
	}
	if len(pairs) == 0 {
		return Session{}, fmt.Errorf("no cookies for %v in chrome", website)
	}
	return Session{Cookies: strings.Join(pairs, "; "), UserAgent: userAgent}, nil
}

// browserWebSocketURL finds the browser websocket of Chrome debugging at
// hostport: from /json/version when Chrome serves it, otherwise from a
// DevToolsActivePort file whose port matches.
func browserWebSocketURL(ctx context.Context, hostport string) (string, error) {
	u, err := url.Parse(hostport)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("bad hostport %q, want e.g. http://127.0.0.1:9222", hostport)
	}

	if wsURL, err := fromVersion(ctx, hostport); err == nil {
		return wsURL, nil
	}

	if path, ok := activePortPath(u.Port()); ok {
		return "ws://" + u.Host + path, nil
	}

	return "", fmt.Errorf("no chrome debugging at %v: start chrome with --remote-debugging-port and "+
		"--user-data-dir, or turn it on at chrome://inspect/#remote-debugging", hostport)
}

// fromVersion reads webSocketDebuggerUrl from /json/version.
func fromVersion(ctx context.Context, hostport string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(hostport, "/")+"/json/version", nil)
	if err != nil {
		return "", err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("/json/version: %v", res.Status)
	}

	var version struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(res.Body).Decode(&version); err != nil {
		return "", fmt.Errorf("/json/version: %w", err)
	}
	if version.WebSocketDebuggerURL == "" {
		return "", errors.New("/json/version: no webSocketDebuggerUrl")
	}
	return version.WebSocketDebuggerURL, nil
}

// activePortPath returns the browser websocket path from the first
// DevToolsActivePort file whose port is port.
func activePortPath(port string) (string, bool) {
	config, err := os.UserConfigDir()
	if err != nil {
		return "", false
	}
	for _, dir := range profileDirs {
		f, err := os.Open(filepath.Join(config, dir, "DevToolsActivePort"))
		if err != nil {
			continue
		}
		var lines []string
		scanner := bufio.NewScanner(f)
		for len(lines) < 2 && scanner.Scan() {
			lines = append(lines, strings.TrimSpace(scanner.Text()))
		}
		f.Close()
		if len(lines) == 2 && lines[0] == port && strings.HasPrefix(lines[1], "/devtools/browser/") {
			return lines[1], true
		}
	}
	return "", false
}

type cookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
}

// fetch returns every cookie in the browser (Storage.getCookies) and its
// User-Agent (Browser.getVersion), over one connection to the browser
// websocket.
func fetch(ctx context.Context, wsURL string) ([]cookie, string, error) {
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("unable to connect to chrome at %v (did you allow it?): %w", wsURL, err)
	}
	defer conn.CloseNow()
	// Cookie lists can be large.
	conn.SetReadLimit(64 << 20)

	var cookies struct {
		Cookies []cookie `json:"cookies"`
	}
	if err := call(ctx, conn, 1, "Storage.getCookies", &cookies); err != nil {
		return nil, "", err
	}
	var version struct {
		UserAgent string `json:"userAgent"`
	}
	if err := call(ctx, conn, 2, "Browser.getVersion", &version); err != nil {
		return nil, "", err
	}

	conn.Close(websocket.StatusNormalClosure, "")
	return cookies.Cookies, version.UserAgent, nil
}

// call sends the CDP command method with id and decodes its reply's result
// into result, skipping anything else that arrives first, such as events.
func call(ctx context.Context, conn *websocket.Conn, id int, method string, result any) error {
	if err := wsjson.Write(ctx, conn, map[string]any{"id": id, "method": method}); err != nil {
		return fmt.Errorf("%v: %w", method, err)
	}
	for {
		var reply struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := wsjson.Read(ctx, conn, &reply); err != nil {
			return fmt.Errorf("%v: %w", method, err)
		}
		if reply.ID == nil || *reply.ID != id {
			continue
		}
		if reply.Error != nil {
			return fmt.Errorf("%v: %v", method, reply.Error.Message)
		}
		if err := json.Unmarshal(reply.Result, result); err != nil {
			return fmt.Errorf("%v: %w", method, err)
		}
		return nil
	}
}

// domainMatches reports whether a cookie with domain belongs to website:
// the same host, a subdomain of website, or a parent domain of website.
func domainMatches(domain, website string) bool {
	host := strings.ToLower(strings.TrimPrefix(domain, "."))
	site := strings.ToLower(strings.TrimPrefix(website, "."))
	if host == "" || site == "" {
		return false
	}
	return host == site || strings.HasSuffix(host, "."+site) || strings.HasSuffix(site, "."+host)
}
