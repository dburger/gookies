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
//	session, err := gookies.GetSession(ctx, *chrome, *url)
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
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// Session is what a request needs to look like it came from your Chrome.
type Session struct {
	// Cookies is "name=value; name=value", ready for a Cookie header.
	Cookies string
	// UserAgent is the User-Agent header Chrome sends.
	UserAgent string
}

// GetSession returns the cookies for rawURL (per RFC 6265) and Chrome's
// User-Agent, over a single connection (one prompt to allow it).
func GetSession(ctx context.Context, hostport, rawURL string) (Session, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return Session{}, fmt.Errorf("bad url %q, want e.g. https://example.com/", rawURL)
	}
	reqPath := u.Path
	if reqPath == "" {
		reqPath = "/"
	}

	wsURLs, err := browserWebSocketURLs(ctx, hostport)
	if err != nil {
		return Session{}, err
	}

	var cookies []cookie
	var userAgent string
	for _, wsURL := range wsURLs {
		cookies, userAgent, err = fetch(ctx, wsURL)
		if err == nil || ctx.Err() != nil {
			break
		}
	}
	if err != nil {
		return Session{}, err
	}

	now := time.Now()
	host := u.Hostname()
	isHTTPS := u.Scheme == "https"
	var matched []cookie
	for _, c := range cookies {
		if cookieMatches(c, host, reqPath, isHTTPS, now) {
			matched = append(matched, c)
		}
	}
	// Prefer more specific paths and host-only cookies when deduplicating names.
	slices.SortStableFunc(matched, func(a, b cookie) int {
		if len(a.Path) != len(b.Path) {
			return len(b.Path) - len(a.Path)
		}
		aDomain := strings.HasPrefix(a.Domain, ".")
		bDomain := strings.HasPrefix(b.Domain, ".")
		if aDomain != bDomain {
			if !aDomain {
				return -1
			}
			return 1
		}
		return 0
	})

	seen := make(map[string]struct{}, len(matched))
	var pairs []string
	for _, c := range matched {
		if _, ok := seen[c.Name]; ok {
			continue
		}
		seen[c.Name] = struct{}{}
		pairs = append(pairs, c.Name+"="+c.Value)
	}
	if len(pairs) == 0 {
		return Session{}, fmt.Errorf("no cookies for %v in chrome", rawURL)
	}
	return Session{Cookies: strings.Join(pairs, "; "), UserAgent: userAgent}, nil
}

// browserWebSocketURLs finds candidate browser websockets of Chrome debugging at
// hostport: from /json/version when Chrome serves it, otherwise from any
// DevToolsActivePort file whose port matches.
func browserWebSocketURLs(ctx context.Context, hostport string) ([]string, error) {
	u, err := url.Parse(hostport)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("bad hostport %q, want e.g. http://127.0.0.1:9222", hostport)
	}

	wsURL, versionErr := fromVersion(ctx, hostport)
	if versionErr == nil {
		return []string{wsURL}, nil
	}

	if isLoopbackHost(u.Hostname()) {
		if paths := activePortPaths(u.Port()); len(paths) > 0 {
			wsScheme := "ws"
			if u.Scheme == "https" {
				wsScheme = "wss"
			}
			urls := make([]string, len(paths))
			for i, path := range paths {
				urls[i] = wsScheme + "://" + u.Host + path
			}
			return urls, nil
		}
	}

	return nil, fmt.Errorf("no chrome debugging at %v (%w): start chrome with --remote-debugging-port and "+
		"--user-data-dir, or turn it on at chrome://inspect/#remote-debugging", hostport, versionErr)
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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

// activePortPaths returns the browser websocket paths from all
// DevToolsActivePort files whose port is port.
func activePortPaths(port string) []string {
	var paths []string
	for _, dir := range chromeProfileDirs(runtime.GOOS) {
		f, err := os.Open(filepath.Join(dir, "DevToolsActivePort"))
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
			if !slices.Contains(paths, lines[1]) {
				paths = append(paths, lines[1])
			}
		}
	}
	return paths
}

func chromeProfileDirs(goos string) []string {
	switch goos {
	case "windows":
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			if home, err := os.UserHomeDir(); err == nil {
				localAppData = filepath.Join(home, "AppData", "Local")
			}
		}
		if localAppData == "" {
			return nil
		}
		return joinAll(localAppData,
			filepath.Join("Google", "Chrome", "User Data"),
			filepath.Join("Google", "Chrome Beta", "User Data"),
			filepath.Join("Google", "Chrome SxS", "User Data"),
			filepath.Join("Chromium", "User Data"),
		)
	case "darwin":
		config, err := os.UserConfigDir()
		if err != nil {
			return nil
		}
		return joinAll(config,
			filepath.Join("Google", "Chrome"),
			filepath.Join("Google", "Chrome Beta"),
			filepath.Join("Google", "Chrome Canary"),
			"Chromium",
		)
	default:
		config, err := os.UserConfigDir()
		if err != nil {
			return nil
		}
		return joinAll(config, "google-chrome", "google-chrome-beta", "google-chrome-unstable", "chromium")
	}
}

func joinAll(base string, subdirs ...string) []string {
	dirs := make([]string, len(subdirs))
	for i, sub := range subdirs {
		dirs[i] = filepath.Join(base, sub)
	}
	return dirs
}

type cookie struct {
	Name    string  `json:"name"`
	Value   string  `json:"value"`
	Domain  string  `json:"domain"`
	Path    string  `json:"path"`
	Expires float64 `json:"expires"`
	Secure  bool    `json:"secure"`
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

func cookieMatches(c cookie, host, reqPath string, isHTTPS bool, now time.Time) bool {
	if c.Secure && !isHTTPS {
		return false
	}
	if c.Expires > 0 && float64(now.UnixNano())/1e9 >= c.Expires {
		return false
	}
	return domainMatches(c.Domain, host) && pathMatches(c.Path, reqPath)
}

// pathMatches reports whether reqPath matches cookiePath per RFC 6265 section 5.1.4.
func pathMatches(cookiePath, reqPath string) bool {
	if cookiePath == "" {
		cookiePath = "/"
	}
	if reqPath == cookiePath {
		return true
	}
	if !strings.HasPrefix(reqPath, cookiePath) {
		return false
	}
	return strings.HasSuffix(cookiePath, "/") || reqPath[len(cookiePath)] == '/'
}

// domainMatches reports whether a cookie with cookieDomain applies to domain per
// RFC 6265: host-only cookies (no leading dot) require an exact match; domain
// cookies (leading dot) match the domain itself and its subdomains.
func domainMatches(cookieDomain, domain string) bool {
	site := strings.ToLower(strings.TrimPrefix(domain, "."))
	if cookieDomain == "" || site == "" {
		return false
	}
	if strings.HasPrefix(cookieDomain, ".") {
		host := strings.ToLower(strings.TrimPrefix(cookieDomain, "."))
		return host != "" && (site == host || strings.HasSuffix(site, "."+host))
	}
	return site == strings.ToLower(cookieDomain)
}
