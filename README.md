# gookies

A golang library for reading cookies and the user-agent out of a running Chrome through the
Chrome DevTools Protocol.

```go
session, err := gookies.GetSession(ctx, "http://127.0.0.1:9222", "example.com")
// session.Cookies is "name=value; name=value", ready for a Cookie header
// session.UserAgent is the User-Agent header Chrome sends
```

It returns the cookies that apply to the website per RFC 6265 (exact host matches and
parent domain cookies). No tab for the site needs to be open.

## Putting Chrome in debug mode

Either of these works:

- **Your running Chrome (144 and later):** open `chrome://inspect/#remote-debugging` and turn
  remote debugging on. Chrome writes the port and websocket path to `DevToolsActivePort` in your
  profile directory, which gookies reads, and asks you to allow each connection.
- **A separate profile:** since Chrome 136 `--remote-debugging-port` is ignored on the default
  profile, so give it a profile of its own and sign in there once:

  ```bash
  google-chrome-stable --remote-debugging-port=9222 --user-data-dir="$HOME/.config/chrome-debug"
  ```

## Tests

`make test` runs the unit tests; `make test-integration` (or `go test -tags=integration ./...`)
runs the live-Chrome tests, which need Chrome in debug mode on port 9222 with google.com
cookies (and you to allow the connection).
