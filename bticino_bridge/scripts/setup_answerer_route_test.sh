#!/usr/bin/env bash
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$HERE/setup_answerer_route.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
FIX="$TMP/route_int.conf"

# Fixture: alluser line with user1..5 (flexisip static-record format)
cat > "$FIX" <<'EOF'
<sip:alluser@YOUR_GATEWAY_ID.bs.iotleg.com> <sip:user1@YOUR_GATEWAY_ID.bs.iotleg.com>,<sip:user2@YOUR_GATEWAY_ID.bs.iotleg.com>,<sip:user3@YOUR_GATEWAY_ID.bs.iotleg.com>,<sip:user4@YOUR_GATEWAY_ID.bs.iotleg.com>,<sip:user5@YOUR_GATEWAY_ID.bs.iotleg.com>
EOF

# 1) --add inserts the bridge AOR and makes a backup
"$SCRIPT" --add --file "$FIX"
grep -q 'sip:bridge@YOUR_GATEWAY_ID.bs.iotleg.com' "$FIX" || { echo "FAIL: bridge not added"; exit 1; }
ls "$FIX".bak.* >/dev/null 2>&1 || { echo "FAIL: no backup made"; exit 1; }
grep -q 'sip:user5@YOUR_GATEWAY_ID.bs.iotleg.com' "$FIX" || { echo "FAIL: user5 lost"; exit 1; }

# 2) --add is idempotent (no duplicate)
"$SCRIPT" --add --file "$FIX"
[ "$(grep -o 'sip:bridge@YOUR_GATEWAY_ID' "$FIX" | wc -l)" -eq 1 ] || { echo "FAIL: bridge duplicated"; exit 1; }

# 3) --check returns 0 when present, !=0 when not
"$SCRIPT" --check --file "$FIX" || { echo "FAIL: --check should return 0"; exit 1; }

# 4) --restore brings back the most recent backup (without bridge)
"$SCRIPT" --restore --file "$FIX"
grep -q 'sip:bridge@YOUR_GATEWAY_ID' "$FIX" && { echo "FAIL: restore did not remove bridge"; exit 1; }
grep -q 'sip:user1@YOUR_GATEWAY_ID' "$FIX" || { echo "FAIL: restore corrupted the file"; exit 1; }

echo "OK setup_answerer_route: 4/4"
