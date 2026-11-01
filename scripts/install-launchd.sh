#!/bin/sh
# Install a per-user LaunchAgent for the standalone Surge Bridge core.
# Surge still reads the same Sub-Store policy-path directly; this service
# only fetches that output for the Mihomo core configuration.
set -eu
umask 077

if [ "$#" -lt 1 ] || [ "$#" -gt 3 ]; then
  echo "Usage: $0 '<existing Sub-Store SurgeMac subscription URL>' [binary path] [refresh interval]" >&2
  exit 2
fi
SOURCE_URL=$1
BINARY=${2:-/opt/homebrew/bin/surge-bridge}
INTERVAL=${3:-5m}
case "$SOURCE_URL" in
  https://*|http://localhost/*|http://127.0.0.1/*|http://localhost:*|http://127.0.0.1:*) ;;
  *) echo "Use HTTPS or a loopback HTTP Sub-Store URL" >&2; exit 2 ;;
esac
case "$INTERVAL" in *[!a-zA-Z0-9]*|'') echo "Invalid refresh interval" >&2; exit 2 ;; esac
if [ ! -x "$BINARY" ]; then
  echo "Executable not found or not executable: $BINARY" >&2
  exit 1
fi

DATA_DIR="$HOME/Library/Application Support/SurgeBridge"
PLIST_DIR="$HOME/Library/LaunchAgents"
PLIST="$PLIST_DIR/dev.surgebridge.engine.plist"
LABEL="dev.surgebridge.engine"
mkdir -p "$DATA_DIR" "$PLIST_DIR"
SOURCE_FILE="$DATA_DIR/substore-url.txt"
# File, not a process argument: subscription tokens stay out of 'ps' output.
printf '%s\n' "$SOURCE_URL" > "$SOURCE_FILE"
chmod 600 "$SOURCE_FILE"

xml_escape() {
  printf '%s' "$1" | sed -e 's/\&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g' -e 's/"/\&quot;/g' -e "s/'/\&apos;/g"
}
E_BIN=$(xml_escape "$BINARY")
E_SOURCE=$(xml_escape "$SOURCE_FILE")
E_STDOUT=$(xml_escape "$DATA_DIR/stdout.log")
E_STDERR=$(xml_escape "$DATA_DIR/stderr.log")
E_INTERVAL=$(xml_escape "$INTERVAL")

cat > "$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>$LABEL</string>
  <key>ProgramArguments</key>
  <array>
    <string>$E_BIN</string>
    <string>-bridge-source-file</string><string>$E_SOURCE</string>
    <string>-bridge-poll</string><string>$E_INTERVAL</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>$E_STDOUT</string>
  <key>StandardErrorPath</key><string>$E_STDERR</string>
</dict></plist>
EOF
chmod 600 "$PLIST"
USER_DOMAIN="gui/$(id -u)"
launchctl bootout "$USER_DOMAIN" "$PLIST" >/dev/null 2>&1 || true
launchctl bootstrap "$USER_DOMAIN" "$PLIST"
echo "Surge Bridge LaunchAgent installed: $PLIST"
echo "Status: launchctl print $USER_DOMAIN/$LABEL"
echo "Logs: $DATA_DIR/stderr.log"
echo "Remember to add PROCESS-NAME,surge-bridge,DIRECT in Surge if Enhanced Mode captures the independent core."
