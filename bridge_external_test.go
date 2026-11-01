package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBridgeExternalFindsAuthenticatedCore(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/surge-bridge/status" || r.Header.Get("Authorization") != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"name":"surge-bridge","status":"running"}`))
	}))
	defer s.Close()

	cfg, _ := json.Marshal(map[string]string{
		"external-controller": strings.TrimPrefix(s.URL, "http://"),
		"secret": "test-key",
	})
	if !bridgeHasRunningDaemon(cfg) { t.Fatal("expected healthy standalone core to be detected") }

	bad, _ := json.Marshal(map[string]string{
		"external-controller": strings.TrimPrefix(s.URL, "http://"),
		"secret": "wrong-key",
	})
	if bridgeHasRunningDaemon(bad) { t.Fatal("wrong controller token must not cause suppression") }
	if bridgeHasRunningDaemon([]byte("{}")) { t.Fatal("ordinary Mihomo config must not trigger duplicate detection") }
}
