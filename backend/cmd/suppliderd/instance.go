// Command suppliderd single-instance handoff.
//
// The desktop shell launches this sidecar on a fixed loopback port
// (127.0.0.1:7612). If a previous sidecar is ever orphaned — the Tauri
// process hard-crashed or was force-quit before its exit handler could
// kill the child — it keeps holding the port and the SQLite file. A naive
// second launch then fails to bind and exits, leaving the UI stuck on
// "后端未连接" with nothing a non-technical user can do.
//
// This file implements the recovery rule: when the bind fails because the
// port is in use, probe the holder. If a healthy Supplider sidecar is
// already serving, the newcomer exits successfully and the UI simply talks
// to the running instance (same per-user data directory — double-click
// always works). If the holder is anything else, the bind error is fatal
// as before. The check runs BEFORE any database/restore side effect, so a
// deferred duplicate never swaps the live library out from under the
// running instance.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// knownTiers is the set of feature-matrix "tier" values a genuine Supplider
// sidecar reports. Anything without one of these at /api/v1/features is a
// foreign process squatting on the port and must NOT be deferred to.
var knownTiers = map[string]bool{
	"personal": true, "small_business": true, "enterprise": true,
}

// probeOnce asks the holder of baseURL whether it is a healthy Supplider
// sidecar: /readyz must answer 200 {"status":"ok"} and /api/v1/features
// must 200 with a recognized tier. Any mismatch/timeout returns false.
func probeOnce(ctx context.Context, baseURL string) bool {
	ready, ok := getJSON(ctx, baseURL+"/readyz")
	if !ok {
		return false
	}
	if status, _ := ready["status"].(string); status != "ok" {
		return false
	}
	feats, ok := getJSON(ctx, baseURL+"/api/v1/features")
	if !ok {
		return false
	}
	tier, _ := feats["tier"].(string)
	return knownTiers[tier]
}

// getJSON does a bounded GET and decodes a JSON object; false on any error,
// non-200 status, or non-object body.
func getJSON(ctx context.Context, url string) (map[string]any, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	var out map[string]any
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<16)).Decode(&out); err != nil {
		return nil, false
	}
	return out, true
}

// awaitExistingInstance polls probeOnce for up to wait so a peer that is
// still starting (port bound but not yet answering) is recognized instead
// of forcing the newcomer to abort. Returns true only when a healthy
// Supplider answers.
func awaitExistingInstance(ctx context.Context, baseURL string, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for {
		if probeOnce(ctx, baseURL) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
}
