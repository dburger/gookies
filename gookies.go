package gookies

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
	"strings"
	"time"
)

// getWebSocketUrl returns the websocket URL for the given website as loaded
// from chrome running in debug mode at the given hostport.
func getWebSocketUrl(hostport, website string) (string, error) {
	res, err := http.Get(fmt.Sprintf("%v/json", hostport))
	if err != nil {
		return "", fmt.Errorf("unable to connect to cdp, are you running in debug mode?: %w", err)
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", fmt.Errorf("unable to read body from cdp: %w", err)
	}

	var obj interface{}
	err = json.Unmarshal(body, &obj)
	if err != nil {
		return "", fmt.Errorf("unable to unmarshall cdp response: %w", err)
	}

	for _, resource := range obj.([]interface{}) {
		info := resource.(map[string]interface{})
		rtype := info["type"].(string)
		url := info["url"].(string)
		if rtype == "page" && strings.Contains(url, website) {
			// Take the first one and ignore the rest if any.
			return info["webSocketDebuggerUrl"].(string), nil
		}
	}

	return "", errors.New("unable to find OddsJam tab")
}

// GetCookies returns the cookies for the given website by pulling them from
// chrome running in debug mode at the given hostport.
func GetCookies(hostport, website string) (string, error) {
	wsUrl, err := getWebSocketUrl(hostport, website)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsUrl, nil)
	if err != nil {
		return "", fmt.Errorf("unable to connect to cdp websocket: %w", err)
	}
	defer conn.Close(websocket.StatusInternalError, "Job complete.")

	err = wsjson.Write(ctx, conn, map[string]interface{}{
		"id":     2,
		"method": "Network.getCookies",
		"params": nil,
	})
	if err != nil {
		return "", fmt.Errorf("unable to request cookies for OddsJam: %w", err)
	}

	var obj interface{}
	err = wsjson.Read(ctx, conn, &obj)
	if err != nil {
		return "", fmt.Errorf("unable to read cookies for OddsJam: %w", err)
	}

	defer conn.Close(websocket.StatusNormalClosure, "")

	buf := strings.Builder{}
	cookies := obj.(map[string]interface{})["result"].(map[string]interface{})["cookies"].([]interface{})
	for i, v := range cookies {
		entry := v.(map[string]interface{})
		buf.WriteString(fmt.Sprintf("%v=%v", entry["name"], entry["value"]))
		if i < len(cookies)-1 {
			buf.WriteString("; ")
		}
	}

	return buf.String(), nil
}
