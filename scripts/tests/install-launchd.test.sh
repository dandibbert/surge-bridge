#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT HUP INT TERM
mkdir -p "$TMP/home" "$TMP/bin"
cat > "$TMP/bin/launchctl" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod +x "$TMP/bin/launchctl"
SOURCE="https://example.invalid/download/combined?target=SurgeMac&token=private-value"
HOME="$TMP/home" PATH="$TMP/bin:$PATH" sh "$ROOT/scripts/install-launchd.sh" \
  "$SOURCE" /bin/true 5m >/dev/null
PLIST="$TMP/home/Library/LaunchAgents/dev.surgebridge.engine.plist"
URL_FILE="$TMP/home/Library/Application Support/SurgeBridge/substore-url.txt"
test -f "$PLIST" && test -f "$URL_FILE"
test "$(cat "$URL_FILE")" = "$SOURCE"
if grep -q "private-value" "$PLIST"; then
  echo "subscription secret leaked into launchd argv" >&2
  exit 1
fi
python3 - "$PLIST" <<'PY'
import plistlib, sys
with open(sys.argv[1], "rb") as f:
    p = plistlib.load(f)
assert p["KeepAlive"] is True
assert p["ProgramArguments"][0] == "/bin/true"
assert "-bridge-source-file" in p["ProgramArguments"]
assert "-bridge-poll" in p["ProgramArguments"]
assert p["ThrottleInterval"] == 30
PY
