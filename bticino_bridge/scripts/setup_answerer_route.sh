#!/usr/bin/env bash
# Adds/removes the bridge AOR on the `alluser` line of route_ext.conf (ring fork; the ACTIVE file via the route.conf symlink).
# Reversible: --add makes a backup; --restore brings back the most recent backup.
set -euo pipefail

AOR='<sip:bridge@YOUR_GATEWAY_ID.bs.iotleg.com>'
DEFAULT_REMOTE='/etc/flexisip/users/route_ext.conf'
MODE=""
FILE=""

while [ $# -gt 0 ]; do
  case "$1" in
    --add|--restore|--check) MODE="${1#--}";;
    --file) FILE="$2"; shift;;
    *) echo "usage: $0 [--add|--restore|--check] [--file <path>]" >&2; exit 2;;
  esac
  shift
done
[ -n "$MODE" ] || { echo "missing mode" >&2; exit 2; }
FILE="${FILE:-$DEFAULT_REMOTE}"
[ -f "$FILE" ] || { echo "not found: $FILE" >&2; exit 1; }

case "$MODE" in
  check)
    grep -q 'sip:bridge@YOUR_GATEWAY_ID.bs.iotleg.com' "$FILE"
    ;;
  add)
    if grep -q 'sip:bridge@YOUR_GATEWAY_ID.bs.iotleg.com' "$FILE"; then
      echo "already present, nothing to do"; exit 0
    fi
    cp -a "$FILE" "$FILE.bak.$(date +%Y%m%d-%H%M%S)"
    # Append the AOR to the contact list of the alluser line
    sed -i -E "/<sip:alluser@YOUR_GATEWAY_ID\.bs\.iotleg\.com>/ s#\$#,${AOR}#" "$FILE"
    grep -q 'sip:bridge@YOUR_GATEWAY_ID.bs.iotleg.com' "$FILE" || { echo "insert failed" >&2; exit 1; }
    echo "added bridge to alluser"
    ;;
  restore)
    LAST="$(ls -1t "$FILE".bak.* 2>/dev/null | head -1 || true)"
    [ -n "$LAST" ] || { echo "no backup found" >&2; exit 1; }
    cp -a "$LAST" "$FILE"
    echo "restored from $LAST"
    ;;
esac
