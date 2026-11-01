// Package surgebridge reads the SAME Sub-Store SurgeMac output that Surge
// subscribes to. It is an optional read-only source for an independently
// supervised core, not an HTTP proxy for Sub-Store or a Surge config generator.
package surgebridge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const MaxResponseBytes int64 = 24 << 20

var (
	execField = regexp.MustCompile(`(?:^|,)\s*exec\s*=\s*"([^"]+)"`)
	argField  = regexp.MustCompile(`(?:^|,)\s*args\s*=\s*"([^"]*)"`)
)

// ExtractEngineConfig finds the single Surge Bridge External entry and
// extracts its -config argument. The output is *not* passed to Surge.
func ExtractEngineConfig(content []byte) ([]byte, error) {
	if len(content) > int(MaxResponseBytes) {
		return nil, errors.New("SurgeMac subscription response is too large")
	}
	var found []byte
	for _, rawLine := range bytes.Split(content, []byte("\n")) {
		line := strings.TrimSpace(string(rawLine))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		_, policy, ok := strings.Cut(line, "=")
		if !ok || !strings.HasPrefix(strings.TrimSpace(policy), "external,") {
			continue
		}
		exec := execField.FindStringSubmatch(policy)
		if len(exec) < 2 || !strings.Contains(strings.ToLower(filepath.Base(exec[1])), "surge-bridge") {
			continue
		}
		args := argField.FindAllStringSubmatch(policy, -1)
		if len(args) == 0 {
			continue
		}
		for i := 0; i+1 < len(args); i++ {
			if args[i][1] != "-config" {
				continue
			}
			decoded, err := base64.StdEncoding.DecodeString(args[i+1][1])
			if err != nil || !json.Valid(decoded) {
				return nil, errors.New("Surge Bridge External configuration is not valid base64 JSON")
			}
			if len(decoded) > int(MaxResponseBytes) {
				return nil, errors.New("decoded Surge Bridge configuration is too large")
			}
			if found != nil {
				return nil, errors.New("multiple Surge Bridge engines found in one subscription")
			}
			found = decoded
			break
		}
	}
	if found == nil {
		return nil, errors.New("no Surge Bridge External configuration found in Sub-Store SurgeMac output")
	}
	return found, nil
}

// FetchEngineConfig reads the URL for the same Sub-Store output used by Surge.
// It does not alter the Surge subscription or act as an HTTP intermediary.
func FetchEngineConfig(ctx context.Context, client *http.Client, source string) ([]byte, error) {
	u, err := url.Parse(source)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") ||
		u.User != nil {
		return nil, errors.New("bridge-source must be an http(s) Sub-Store URL without embedded userinfo")
	}
	if u.Scheme == "http" {
		// Remote URLs may contain credentials in query parameters. Avoid
		// sending them over cleartext beyond the loopback interface.
		host := strings.ToLower(u.Hostname())
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			return nil, errors.New("non-loopback Sub-Store URL must use HTTPS")
		}
	}
	if client == nil { client = &http.Client{Timeout: 20 * time.Second} }

	// Consistent target selection even when Surge relies on User-Agent.
	q := u.Query()
	if q.Get("target") == "" && !strings.HasSuffix(strings.ToLower(u.Path), "/surgemac") {
		q.Set("target", "SurgeMac")
		u.RawQuery = q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil { return nil, errors.New("could not prepare Sub-Store request") }
	req.Header.Set("User-Agent", "SurgeMac SurgeBridge")
	resp, err := client.Do(req)
	if err != nil { return nil, errors.New("Sub-Store fetch failed (verify network and URL)") }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Sub-Store returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil || len(data) > int(MaxResponseBytes) {
		return nil, errors.New("Sub-Store returned an unreadable or oversized response")
	}
	return ExtractEngineConfig(data)
}

func DefaultCachePath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil { return "", err }
	return filepath.Join(root, "SurgeBridge", "last-good-config.json"), nil
}

func ReadLastGood(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil { return nil, err }
	if len(data) == 0 || len(data) > int(MaxResponseBytes) || !json.Valid(data) {
		return nil, errors.New("invalid cached Surge Bridge configuration")
	}
	return data, nil
}

// WriteLastGood protects the credentials embedded in Mihomo JSON and leaves
// the previous valid configuration untouched if writing fails.
func WriteLastGood(path string, data []byte) error {
	if len(data) == 0 || len(data) > int(MaxResponseBytes) || !json.Valid(data) {
		return errors.New("refusing to cache invalid configuration")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil { return err }
	f, err := os.CreateTemp(dir, ".surge-bridge-*")
	if err != nil { return err }
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil { f.Close(); return err }
	if _, err := f.Write(data); err != nil { f.Close(); return err }
	if err := f.Sync(); err != nil { f.Close(); return err }
	if err := f.Close(); err != nil { return err }
	return os.Rename(f.Name(), path)
}
