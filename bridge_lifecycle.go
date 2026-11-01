package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/metacubex/mihomo/hub"
	"github.com/metacubex/mihomo/hub/executor"
	"github.com/metacubex/mihomo/internal/surgebridge"
	"github.com/metacubex/mihomo/log"
)

var (
	bridgeSourceURL  string
	bridgeSourceFile string
	bridgeCacheFile  string
	bridgePoll       = 5 * time.Minute
)

func bridgeWatchEnabled() bool {
	return bridgeSourceURL != "" || bridgeSourceFile != ""
}

func bridgeResolveSource() (string, error) {
	if bridgeSourceURL != "" && bridgeSourceFile != "" {
		return "", errors.New("use only one of -bridge-source and -bridge-source-file")
	}
	if bridgeSourceFile != "" {
		data, err := os.ReadFile(bridgeSourceFile)
		if err != nil { return "", errors.New("cannot read Surge Bridge subscription URL file") }
		if len(data) > 8192 { return "", errors.New("Surge Bridge subscription URL file is too large") }
		source := strings.TrimSpace(string(data))
		if strings.ContainsAny(source, "\r\n\x00") {
			return "", errors.New("Surge Bridge subscription URL file must contain exactly one URL")
		}
		return source, nil
	}
	if bridgeSourceURL == "" { return "", errors.New("no Surge Bridge subscription source configured") }
	return bridgeSourceURL, nil
}

func bridgeGetCachePath() (string, error) {
	if bridgeCacheFile != "" { return bridgeCacheFile, nil }
	return surgebridge.DefaultCachePath()
}

func bridgeFetchAndValidate() ([]byte, error) {
	source, err := bridgeResolveSource()
	if err != nil { return nil, err }
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	data, err := surgebridge.FetchEngineConfig(ctx, nil, source)
	if err != nil { return nil, err }
	if _, err := executor.ParseWithBytes(data); err != nil {
		return nil, fmt.Errorf("refusing invalid Sub-Store Mihomo config: %w", err)
	}
	return data, nil
}

func bridgeLoadInitial() ([]byte, error) {
	cache, err := bridgeGetCachePath()
	if err != nil { return nil, err }
	if fetched, err := bridgeFetchAndValidate(); err == nil {
		return fetched, nil
	} else {
		// Never log the subscription URL, which may contain authentication.
		log.Warnln("Bridge subscription unavailable; checking last-good cache: %s", err)
	}
	cached, err := surgebridge.ReadLastGood(cache)
	if err != nil { return nil, errors.New("could not fetch subscription and no valid cached configuration exists") }
	if _, err := executor.ParseWithBytes(cached); err != nil {
		return nil, errors.New("last-good configuration failed validation")
	}
	log.Warnln("Starting Bridge using the last known-good configuration")
	return cached, nil
}

func bridgeSaveLastGood(data []byte) {
	cache, err := bridgeGetCachePath()
	if err != nil {
		log.Warnln("Could not determine Bridge cache location: %s", err)
		return
	}
	if err := surgebridge.WriteLastGood(cache, data); err != nil {
		log.Warnln("Could not persist the Bridge last-good configuration: %s", err)
	}
}

// bridgeRefreshIfChanged only applies a fully parsed configuration. If applying
// fails, it makes a best-effort rollback to the previous in-memory snapshot.
// This is additive to Mihomo's existing -config behaviour.
func bridgeRefreshIfChanged(previous []byte, options []hub.Option) []byte {
	updated, err := bridgeFetchAndValidate()
	if err != nil {
		log.Warnln("Bridge subscription refresh failed; keeping existing config: %s", err)
		return previous
	}
	if bytes.Equal(previous, updated) { return previous }
	if err := hub.Parse(updated, options...); err != nil {
		log.Errorln("Bridge config reload failed, restoring previous config: %s", err)
		if rollbackErr := hub.Parse(previous, options...); rollbackErr != nil {
			log.Errorln("Bridge config rollback also failed: %s", rollbackErr)
		}
		return previous
	}
	bridgeSaveLastGood(updated)
	log.Infoln("Surge Bridge successfully loaded updated Sub-Store configuration")
	return updated
}
