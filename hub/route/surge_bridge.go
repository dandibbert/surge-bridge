package route

import (
	"context"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/metacubex/mihomo/common/utils"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/tunnel"

	"github.com/metacubex/chi"
	"github.com/metacubex/chi/render"
	"github.com/metacubex/http"
)

// The Surge Bridge endpoints are additive: standard Mihomo clients and
// /proxies/{name}/delay retain their upstream behaviour.
const (
	bridgeDefaultURL     = "https://www.gstatic.com/generate_204"
	bridgeDefaultTimeout = 7000
	bridgeHistoryLimit   = 100
)

type bridgeDiagnostic struct {
	Name       string    `json:"name"`
	ProxyType  string    `json:"proxyType"`
	URL        string    `json:"url"`
	TestedAt   time.Time `json:"testedAt"`
	DurationMS int64     `json:"durationMs"`
	DelayMS    uint16    `json:"delayMs,omitempty"`
	Status     string    `json:"status"`
	ErrorKind  string    `json:"errorKind,omitempty"`
	Error      string    `json:"error,omitempty"`
}

var (
	// Prevent a dashboard from accidentally starting hundreds of concurrent
	// probes; each request still tests the selected proxy directly.
	bridgeProbeSlots = make(chan struct{}, 8)
	bridgeHistoryMu   sync.Mutex
	bridgeHistory     []bridgeDiagnostic
	bridgeURLSecrets  = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^\s/@]+@`)
)

func surgeBridgeRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/status", getBridgeStatus)
	r.Get("/diagnostics", getBridgeDiagnostics)
	r.Route("/proxies/{name}", func(r chi.Router) {
		r.Use(parseProxyName, findProxyByName)
		r.Get("/test", testBridgeProxy)
	})
	return r
}

func getBridgeStatus(w http.ResponseWriter, r *http.Request) {
	render.JSON(w, r, render.M{
		"name":              "surge-bridge",
		"status":            "running",
		"proxyCount":        len(tunnel.Proxies()),
		"diagnostics":       true,
		"udpUserRouting":    false, // Shared-port UDP is NOT yet safe.
		"maxParallelTests":  cap(bridgeProbeSlots),
	})
}

func getBridgeDiagnostics(w http.ResponseWriter, r *http.Request) {
	bridgeHistoryMu.Lock()
	items := append([]bridgeDiagnostic{}, bridgeHistory...)
	bridgeHistoryMu.Unlock()
	render.JSON(w, r, render.M{"diagnostics": items})
}

func testBridgeProxy(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	target := query.Get("url")
	if target == "" {
		target = bridgeDefaultURL
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, newError("url must be an absolute HTTP(S) URL without credentials"))
		return
	}

	timeout := bridgeDefaultTimeout
	if query.Has("timeout") {
		timeout, err = strconv.Atoi(query.Get("timeout"))
		if err != nil || timeout < 250 || timeout > 30000 {
			render.Status(r, http.StatusBadRequest)
			render.JSON(w, r, newError("timeout must be between 250 and 30000 milliseconds"))
			return
		}
	}

	expectedInput := query.Get("expected")
	if expectedInput == "" && target == bridgeDefaultURL {
		expectedInput = "204"
	}
	expected, err := utils.NewUnsignedRanges[uint16](expectedInput)
	if err != nil {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, newError("invalid expected HTTP status range"))
		return
	}

	select {
	case bridgeProbeSlots <- struct{}{}:
		defer func() { <-bridgeProbeSlots }()
	default:
		render.Status(r, http.StatusTooManyRequests)
		render.JSON(w, r, newError("too many concurrent diagnostic tests"))
		return
	}

	proxy := r.Context().Value(CtxKeyProxy).(C.Proxy)
	probe := bridgeDiagnostic{
		Name:      proxy.Name(),
		ProxyType: proxy.Type().String(),
		URL:       target,
		TestedAt:  time.Now(),
		Status:    "ok",
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeout)*time.Millisecond)
	defer cancel()
	delay, probeErr := proxy.URLTest(ctx, target, expected)
	probe.DurationMS = time.Since(start).Milliseconds()

	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		probe.Status = "failed"
		probe.ErrorKind = "timeout"
		probe.Error = "diagnostic timeout exceeded"
	case errors.Is(ctx.Err(), context.Canceled):
		probe.Status = "failed"
		probe.ErrorKind = "canceled"
		probe.Error = "diagnostic canceled"
	case probeErr != nil:
		probe.Status = "failed"
		probe.ErrorKind = bridgeClassifyError(probeErr)
		probe.Error = bridgeSanitizeError(probeErr)
	case delay == 0:
		probe.Status = "failed"
		probe.ErrorKind = "no_measurement"
		probe.Error = "URLTest did not return a measurable delay"
	default:
		probe.DelayMS = delay
	}

	bridgeHistoryMu.Lock()
	bridgeHistory = append([]bridgeDiagnostic{probe}, bridgeHistory...)
	if len(bridgeHistory) > bridgeHistoryLimit {
		bridgeHistory = bridgeHistory[:bridgeHistoryLimit]
	}
	bridgeHistoryMu.Unlock()
	render.JSON(w, r, probe)
}

// This is a best-effort error category, NOT an instrumented handshake stage.
// Deep DNS/TLS/REALITY stage traces need instrumentation in the dialer itself.
func bridgeClassifyError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	value := strings.ToLower(err.Error())
	switch {
	case strings.Contains(value, "dns"), strings.Contains(value, "lookup "), strings.Contains(value, "no such host"):
		return "dns"
	case strings.Contains(value, "reality"), strings.Contains(value, "tls"), strings.Contains(value, "certificate"), strings.Contains(value, "x509"):
		return "tls_or_reality"
	case strings.Contains(value, "connect:"), strings.Contains(value, "dial tcp"), strings.Contains(value, "connection refused"), strings.Contains(value, "network unreachable"), strings.Contains(value, "connection reset"):
		return "network"
	case strings.Contains(value, "unexpected status"), strings.Contains(value, "status code"):
		return "http_status"
	default:
		return "unknown"
	}
}

func bridgeSanitizeError(err error) string {
	if err == nil {
		return ""
	}
	message := bridgeURLSecrets.ReplaceAllString(err.Error(), "${1}[redacted]@")
	if len(message) > 512 {
		message = message[:512] + "..."
	}
	return message
}
