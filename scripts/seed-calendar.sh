#!/usr/bin/env bash
# Seed calendar demo data into a running dev stack through the REST API.
#
# unreachable-by-design: writes demo data into a developer's own
# database through a running API. It is a convenience for looking at a
# populated calendar, not an assertion about the code, so there is
# nothing for a gate to learn from running it. Invoked by hand through
# `make seed-calendar`.

set -euo pipefail

API="${TC_API_URL:-http://localhost:8080}"
LOCALE="${NF_SEED_LOCALE:-en}"

echo "=== Seeding calendar demo data (locale: $LOCALE) ==="
echo "API: $API"
echo ""

# Helper: register a user and print the access token.
# Falls back to login if registration fails (user already exists).
register_or_login() {
  local email="$1" password="$2" name="$3"
  local res
  res=$(curl -sf -X POST "$API/auth/register" \
    -H 'Content-Type: application/json' \
    -d "{\"email\":\"$email\",\"password\":\"$password\",\"displayName\":\"$name\"}" 2>/dev/null) || true

  local token
  token=$(echo "$res" | jq -r '.accessToken // empty' 2>/dev/null || true)
  if [ -n "$token" ]; then
    echo "$token"
    return
  fi

  # Registration failed — try login instead.
  res=$(curl -sf -X POST "$API/auth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"email\":\"$email\",\"password\":\"$password\"}")
  echo "$res" | jq -r '.accessToken'
}

# Helper: create an event via the API.
create_event() {
  local token="$1" ws_id="$2" cal_id="$3" payload="$4"
  local title
  title=$(echo "$payload" | jq -r '.title')
  curl -sf -X POST "$API/workspaces/$ws_id/calendars/$cal_id/events" \
    -H 'Content-Type: application/json' \
    -H "Authorization: Bearer $token" \
    -d "$payload" > /dev/null
  echo "  + $title"
}

# ---------- Locale-dependent strings ----------

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
LOCALE_FILE="$SCRIPT_DIR/locales/seed-calendar/${LOCALE}.sh"
if [ ! -f "$LOCALE_FILE" ]; then
  echo "ERROR: unsupported locale '$LOCALE' (add $LOCALE_FILE)" >&2
  exit 1
fi
# shellcheck source=locales/seed-calendar/en.sh
source "$LOCALE_FILE"

# ---------- 1. Register users ----------

echo "Registering users..."
MANAGER_TOKEN=$(register_or_login "manager@demo.test" "Password123!" "$MANAGER_NAME")
echo "  manager@demo.test  OK"

TALENT_A_TOKEN=$(register_or_login "talent-a@demo.test" "Password123!" "$TALENT_A_NAME")
echo "  talent-a@demo.test OK"

TALENT_B_TOKEN=$(register_or_login "talent-b@demo.test" "Password123!" "$TALENT_B_NAME")
echo "  talent-b@demo.test OK"
echo ""

# ---------- 2. Create workspace ----------

echo "Creating workspace..."
WS_RES=$(curl -sf -X POST "$API/workspaces" \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $MANAGER_TOKEN" \
  -d "{\"name\":\"$WS_NAME\",\"slug\":\"demo-office\"}" 2>/dev/null) || true

WS_ID=$(echo "$WS_RES" | jq -r '.id // empty' 2>/dev/null || true)

if [ -z "$WS_ID" ]; then
  # Workspace may already exist — list and find it.
  WS_LIST=$(curl -sf "$API/workspaces" \
    -H "Authorization: Bearer $MANAGER_TOKEN")
  WS_ID=$(echo "$WS_LIST" | jq -r '.items[] | select(.slug == "demo-office") | .id')
fi

echo "  Workspace ID: $WS_ID"
echo ""

# ---------- 3. Create shared calendar ----------

echo "Creating shared calendar..."
CAL_RES=$(curl -sf -X POST "$API/workspaces/$WS_ID/calendars" \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $MANAGER_TOKEN" \
  -d "{\"name\":\"$CAL_NAME\",\"kind\":\"shared\",\"color\":\"#2ecc87\"}" 2>/dev/null) || true

CAL_ID=$(echo "$CAL_RES" | jq -r '.id // empty' 2>/dev/null || true)

if [ -z "$CAL_ID" ]; then
  # Calendar may already exist — list and find the shared one.
  CALS_LIST=$(curl -sf "$API/workspaces/$WS_ID/calendars" \
    -H "Authorization: Bearer $MANAGER_TOKEN")
  CAL_ID=$(echo "$CALS_LIST" | jq -r '[.calendars[] | select(.kind == "shared")][0].id')
fi

echo "  Calendar ID: $CAL_ID"
echo ""

# ---------- 4. Create events for April 2026 ----------

echo "Creating events..."

# All-day event helper (UTC midnight-to-midnight).
allday() {
  local title="$1" date="$2" kind="${3:-event}" showAs="${4:-busy}"
  cat <<JSONEOF
{"title":"$title","kind":"$kind","allDay":true,"startAt":"${date}T00:00:00+09:00","endAt":"${date}T23:59:59+09:00","timezone":"Asia/Tokyo","showAs":"$showAs"}
JSONEOF
}

# Timed event helper (JST times).
timed() {
  local title="$1" date="$2" start_time="$3" end_time="$4" kind="${5:-event}" showAs="${6:-busy}"
  cat <<JSONEOF
{"title":"$title","kind":"$kind","allDay":false,"startAt":"${date}T${start_time}:00+09:00","endAt":"${date}T${end_time}:00+09:00","timezone":"Asia/Tokyo","showAs":"$showAs"}
JSONEOF
}

# Event metadata: "type|date|start|end|kind|showAs"
# Titles come from EVENT_TITLES[] in the locale file (same index order).
EVENTS_META=(
  "allday|2026-04-01|||event|busy"
  "timed|2026-04-02|10:00|12:00|event|busy"
  "timed|2026-04-03|14:00|17:00|event|busy"
  "allday|2026-04-05|||block|free"
  "timed|2026-04-07|09:30|10:30|event|busy"
  "allday|2026-04-08|||event|busy"
  "timed|2026-04-09|20:00|21:00|event|busy"
  "timed|2026-04-10|15:00|16:00|event|busy"
  "allday|2026-04-12|||block|free"
  "timed|2026-04-14|13:00|15:00|event|busy"
  "timed|2026-04-15|10:00|12:00|event|busy"
  "allday|2026-04-16|||event|busy"
  "timed|2026-04-17|11:00|12:00|event|busy"
  "timed|2026-04-18|09:00|17:00|event|busy"
  "timed|2026-04-19|13:00|18:00|event|busy"
  "timed|2026-04-20|17:00|21:00|event|busy"
  "timed|2026-04-21|10:00|11:00|event|busy"
  "timed|2026-04-22|14:00|18:00|event|busy"
  "timed|2026-04-23|10:00|11:30|event|busy"
  "allday|2026-04-24|||event|busy"
  "timed|2026-04-25|16:00|17:00|event|busy"
  "allday|2026-04-28|||block|busy"
  "timed|2026-04-30|13:00|17:00|event|busy"
)

if [ "${#EVENT_TITLES[@]}" -ne "${#EVENTS_META[@]}" ]; then
  echo "ERROR: EVENT_TITLES (${#EVENT_TITLES[@]}) and EVENTS_META (${#EVENTS_META[@]}) count mismatch" >&2
  exit 1
fi

for i in "${!EVENTS_META[@]}"; do
  IFS='|' read -r etype date start_time end_time kind showAs <<< "${EVENTS_META[$i]}"
  title="${EVENT_TITLES[$i]}"
  if [ "$etype" = "allday" ]; then
    create_event "$MANAGER_TOKEN" "$WS_ID" "$CAL_ID" "$(allday "$title" "$date" "$kind" "$showAs")"
  else
    create_event "$MANAGER_TOKEN" "$WS_ID" "$CAL_ID" "$(timed "$title" "$date" "$start_time" "$end_time" "$kind" "$showAs")"
  fi
done

echo ""
echo "=== Done! ==="
echo ""
echo "Created 23 events in April 2026."
echo ""
echo "Login credentials:"
echo "  Manager:  manager@demo.test   / Password123!"
echo "  Talent A: talent-a@demo.test  / Password123!"
echo "  Talent B: talent-b@demo.test  / Password123!"
