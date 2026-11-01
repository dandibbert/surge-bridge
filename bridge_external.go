package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/metacubex/mihomo/log"
)

// If the same standalone daemon already serves this exact authenticated
// Bridge controller, Surge's External invocation acts as a lightweight
// compatibility handle rather than attempting to bind a second proxy core.
// It is intentionally opt-in by detection: regular Mihomo -config stays normal.
func bridgeHasRunningDaemon(config []byte) bool {
	var opts struct {
		Controller string `json:"external-controller"`
		Secret     string `json:"secret"`
	}
	if json.Unmarshal(config, &opts) != nil || opts.Secret == "" || opts.Controller == "" {
		return false
	}
	host, _, err := net.SplitHostPort(opts.Controller)
	if err != nil || (host != "127.0.0.1" && host != "::1" && host != "localhost") {
		return false
	}
	target := &url.URL{Scheme: "http", Host: opts.Controller, Path: "/surge-bridge/status"}
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil { return false }
	req.Header.Set("Authorization", "Bearer "+opts.Secret)
	client := &http.Client{Timeout: 700*time.Millisecond, Transport: &http.Transport{
		Proxy: nil, // localhost probe must never traverse Surge / env HTTP proxies.
	}}
	resp, err := client.Do(req)
	if err != nil { return false }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK { return false }
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil { return false }
	var status struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	return json.Unmarshal(body, &status) == nil &&
		strings.EqualFold(status.Name, "surge-bridge") &&
		status.Status == "running"
}

func bridgeWaitForDaemonTermination() {
	log.Infoln("Surge Bridge standalone daemon is already running; not starting a second proxy core")
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(interrupt)
	<-interrupt
}
