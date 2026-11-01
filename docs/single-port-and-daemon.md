# Surge Bridge: single-port setup and supervised service

This is an **experimental** macOS ARM64 build. Keep your existing Mihomo binary and current Surge policy-path untouched until end-to-end testing succeeds.

## Architecture

```
Sub-Store combined subscription ──(existing URL)──> Surge for macOS
              │                                  │
              └──(read-only, same URL)──> Surge Bridge (one core)
                                                 ▲
Surge policy-path SOCKS5 node ───same port + per-node credentials──┘
```

The Bridge **does not** produce another Surge profile and **is not** an HTTP reverse proxy in front of Sub-Store. It reads the same exported SurgeMac policy list to obtain the embedded Mihomo JSON. The only extra fetch is for the optional standalone service. It can restart from its protected last-known-good config even if Sub-Store becomes temporarily unreachable.

## 1. Use the single-port Sub-Store script

In the **final combined subscription**, add this as its last **节点操作 → 脚本操作**:

- [single-port-surgemac.js](../scripts/sub-store/single-port-surgemac.js)
- Ensure its output target is `SurgeMac` (not `Surge`). Continue to use the existing Sub-Store URL in Surge's `policy-path`.
- Do **not** combine it with [compat-surgemac.js](../scripts/sub-store/compat-surgemac.js) or the legacy `mihomoMerge` conversion. The single-port script does the conversion itself.
- It routes VLESS, SSR, or explicitly `_mihomoExternal` / `_surgeBridge` tagged nodes through Bridge; native Surge-compatible protocols remain unchanged.
- Existing Surge proxy names are retained, including native nodes.
- A stable **single SOCKS port per combined output** is derived from the combination name. To override it, provide script argument `port=xxxxx`. There is **no localPort to configure per source subscription or per node**.
- Optional script arguments: `exec=/absolute/path/surge-bridge`, `port`, `controllerPort`, `secret`, `udp=false` (TCP-only dry run).

⚠️ If multiple independent `policy-path` resources each run this script, each produces its own engine and port. Hash-derived ports have a low but **non-zero collision possibility** and do not merge multiple resource files into one process. The one-process setup assumes your **combined subscription** is the sole Bridge source.

The new bridge listener uses `mode: rule`, `IN-USER` mappings, mandatory SOCKS5 authentication, and a separate ephemeral UDP port per authenticated UDP ASSOCIATE session. The standalone controller is bound to `127.0.0.1` and requires a secret; it is not exposed to the LAN.

## 2. Install the independent per-user engine

Download the macOS ARM64 `surge-bridge-darwin-arm64` binary from the new Actions workflow. Rename it to `surge-bridge`, make it executable, and place it at `/opt/homebrew/bin/surge-bridge` (or specify a different path consistently in the script and installer).

```bash
# Do not replace the original mihomo binary.
chmod +x /opt/homebrew/bin/surge-bridge

# Use the SAME existing Sub-Store SurgeMac URL already in Surge;
# the installer prompts for it rather than recording the token in shell history.
sh scripts/install-launchd.sh
```

The installer sets up `~/Library/LaunchAgents/dev.surgebridge.engine.plist` (RunAtLoad + KeepAlive) and stores your URL in `~/Library/Application Support/SurgeBridge/substore-url.txt` with mode 0600. It enables the binary's `-bridge-source-file` watch mode, which:

1. Fetches the existing Sub-Store output (read-only) and extracts its single Engine External configuration.
2. Validates the new config before applying it.
3. Saves the last known-good config to `~/Library/Application Support/SurgeBridge/last-good-config.json` with mode 0600.
4. Reloads changed configs every five minutes or on SIGHUP; if fetching or validation fails, retains the current configuration and on a cold restart tries the last-good cache.
5. Is independently restarted by `launchd` if it exits.

**Important:** The Engine External entry in Surge still exists to carry the encoded config, but should **not** be selected when the separate LaunchAgent is running. Surge only needs the individual SOCKS5 nodes. Selecting the Engine entry would cause Surge to try to launch a second binary for the same local port; this interaction still needs dedicated integration testing and a better single-instance handshake.

Check service status:

```bash
launchctl print gui/$(id -u)/dev.surgebridge.engine
lsof -nP -iTCP -sTCP:LISTEN | grep surge-bridge
tail -n 100 "$HOME/Library/Application Support/SurgeBridge/stderr.log"
```

Under Surge Enhanced Mode, prevent nested interception of this **independent** proxy core. Add the following near the top of your main Surge `[Rule]` section and verify via Surge's request log / CLI:

```ini
PROCESS-NAME,surge-bridge,DIRECT
```

## 3. Diagnose an individual node

The script puts `external-controller: 127.0.0.1:<controllerPort>` and a secret in the generated config. It is stored in the mode-0600 last-good file, so you can inspect it locally rather than copy it from base64:

```bash
CONFIG="$HOME/Library/Application Support/SurgeBridge/last-good-config.json"
API=$(jq -r '."external-controller"' "$CONFIG")
TOKEN=$(jq -r '.secret' "$CONFIG")
curl -fsSG "http://$API/surge-bridge/proxies/NODE_ID/test" \
  -H "Authorization: Bearer $TOKEN" \
  --data-urlencode "timeout=8000"
```

`NODE_ID` is the internal outbound name (prefixed `sb-out-`), **not** the original Surge display name; use the `proxies` list in the cached config to find it. A future menu-bar panel will resolve these names automatically.

Mihomo's default `/proxies/{name}/delay` now also retains the underlying error instead of the generic Yacd 503 message. The diagnostic API is still best-effort categorization, **not** an instrumented transcript of every DNS, TCP, TLS, and REALITY handshake stage.

## Remaining limitations

- Direct Mac/Surge end-to-end testing of SOCKS5 UDP ASSOCIATE and subscription refresh is still required.
- A standalone engine duplicates the Sub-Store *read* but does not intercept or alter Surge's subscription URL. This creates a small possible version-skew window during separate refreshes; preserving stable node IDs mitigates reorders, not an actual credential change.
- Two independently imported Sub-Store resources cannot yet be combined in a single running core automatically.
- No SwiftUI menu-bar UI yet; current control interface is the authenticated Mihomo/Bridge API.
- Last-good config and source URL contain credentials: protect these files and do not include them in bug reports.
