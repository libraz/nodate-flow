#!/usr/bin/env bash
# Contract test: run Schemathesis against the live API to detect spec drift.
#
# Prerequisites:
#   - flow-api running at NF_API_URL (default: http://localhost:8080)
#   - auth-api running at NF_AUTH_API_URL (default: http://localhost:8082)
#   - Docker available (schemathesis runs in a container)
#
# unreachable-by-design: fuzzes two servers that have to already be
# running and seeded, which no gate can arrange for itself. Invoked by
# hand through `make test-contract`. The drift this would catch from a
# cold start is covered by `make test-openapi-diff`, which regenerates
# the spec from the Go sources and needs no live service.
#
# Usage:
#   ./scripts/contract-test.sh              # full run
#   ./scripts/contract-test.sh --dry-run    # show what would run
#
# Environment variables:
#   NF_API_URL          Base URL of flow-api (default: http://localhost:8080)
#   NF_AUTH_API_URL     Base URL of auth-api (default: http://localhost:8082)
#   NF_CONTRACT_EMAIL   Email for test user (default: auto-generated)
#   NF_CONTRACT_PASS    Password for test user (default: ContractTest1!)
#   SCHEMATHESIS_IMAGE  Docker image (default: schemathesis/schemathesis:stable)
#   SCHEMATHESIS_ARGS   Extra args passed to schemathesis (default: empty)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

API_URL="${NF_API_URL:-http://localhost:8080}"
AUTH_API_URL="${NF_AUTH_API_URL:-http://localhost:8082}"
SCHEMATHESIS_IMAGE="${SCHEMATHESIS_IMAGE:-schemathesis/schemathesis:stable}"
DRY_RUN=false

if [[ "${1:-}" == "--dry-run" ]]; then
  DRY_RUN=true
fi

# ---------- Preflight checks ----------

if ! command -v docker &>/dev/null; then
  echo "ERROR: docker is required but not found."
  exit 1
fi

if ! command -v curl &>/dev/null; then
  echo "ERROR: curl is required but not found."
  exit 1
fi

# Check APIs are reachable.
echo "Checking flow-api at $API_URL/health ..."
if ! curl -sf "$API_URL/health" >/dev/null 2>&1; then
  echo "ERROR: flow-api is not responding at $API_URL/health"
  echo "       Start flow-api first (make dev-api)."
  exit 1
fi
echo "flow-api is up."

echo "Checking auth-api at $AUTH_API_URL/health ..."
if ! curl -sf "$AUTH_API_URL/health" >/dev/null 2>&1; then
  echo "ERROR: auth-api is not responding at $AUTH_API_URL/health"
  echo "       Start auth-api first (make dev-auth-api)."
  exit 1
fi
echo "auth-api is up."

# Build per-service specs. The committed SDK spec is merged for clients, but
# contract tests must target each service at its own base URL.
FLOW_SPEC_FILE="$(mktemp "${TMPDIR:-/tmp}/nodate-flow-openapi.XXXXXX")"
AUTH_SPEC_FILE="$(mktemp "${TMPDIR:-/tmp}/nodate-auth-openapi.XXXXXX")"
cleanup() {
  rm -f "$FLOW_SPEC_FILE" "$AUTH_SPEC_FILE"
}
trap cleanup EXIT

echo "Dumping flow-api OpenAPI spec ..."
(
  cd "$ROOT_DIR/apps/flow-api"
  go run ./cmd/dump-openapi -o "$FLOW_SPEC_FILE"
) >/dev/null

echo "Dumping auth-api OpenAPI spec ..."
(
  cd "$ROOT_DIR/apps/auth-api"
  go run ./cmd/dump-openapi -o "$AUTH_SPEC_FILE"
) >/dev/null

# ---------- Create test user and obtain token ----------

CONTRACT_EMAIL="${NF_CONTRACT_EMAIL:-contract-test-$(date +%s)@test.nodate.local}"
CONTRACT_PASS="${NF_CONTRACT_PASS:-ContractTest1!}"
CONTRACT_NAME="Contract Test User"

echo "Registering test user: $CONTRACT_EMAIL ..."
REGISTER_RESP=$(curl -sf -X POST "$AUTH_API_URL/auth/register" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"$CONTRACT_EMAIL\",\"password\":\"$CONTRACT_PASS\",\"displayName\":\"$CONTRACT_NAME\"}" \
  2>&1) || {
    echo "WARNING: Registration failed (user may already exist). Trying login..."
    REGISTER_RESP=""
  }

if [[ -n "$REGISTER_RESP" ]]; then
  ACCESS_TOKEN=$(echo "$REGISTER_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('accessToken',''))" 2>/dev/null || true)
fi

if [[ -z "${ACCESS_TOKEN:-}" ]]; then
  echo "Logging in as $CONTRACT_EMAIL ..."
  LOGIN_RESP=$(curl -sf -X POST "$AUTH_API_URL/auth/login" \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"$CONTRACT_EMAIL\",\"password\":\"$CONTRACT_PASS\"}")
  ACCESS_TOKEN=$(echo "$LOGIN_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('accessToken',''))" 2>/dev/null || true)
fi

if [[ -z "${ACCESS_TOKEN:-}" ]]; then
  echo "ERROR: Could not obtain an access token."
  exit 1
fi

echo "Obtained access token (${#ACCESS_TOKEN} chars)."

# ---------- Determine host address for Docker ----------

# When the API runs on the host, Docker containers need host.docker.internal (macOS/Windows)
# or the host gateway (Linux).
container_url() {
  local url="$1"
  if [[ "$url" == *"localhost"* ]] || [[ "$url" == *"127.0.0.1"* ]]; then
    echo "$url" | sed -E 's/(localhost|127\.0\.0\.1)/host.docker.internal/g'
  else
    echo "$url"
  fi
}

if [[ "$API_URL" == *"localhost"* ]] || [[ "$API_URL" == *"127.0.0.1"* ]] || [[ "$AUTH_API_URL" == *"localhost"* ]] || [[ "$AUTH_API_URL" == *"127.0.0.1"* ]]; then
  DOCKER_HOST_FLAG="--add-host=host.docker.internal:host-gateway"
else
  DOCKER_HOST_FLAG=""
fi

# On macOS, host.docker.internal works without --add-host.
if [[ "$(uname -s)" == "Darwin" ]]; then
  DOCKER_HOST_FLAG=""
fi

CONTAINER_API_URL="$(container_url "$API_URL")"
CONTAINER_AUTH_API_URL="$(container_url "$AUTH_API_URL")"

# ---------- Build schemathesis command ----------

# Endpoints that are public (no auth needed). Test these separately without a token.
FLOW_PUBLIC_ENDPOINTS="/health,/share/cal/{token},/public/lenses/{token},/public/invites/accept,/invites/{token}/info,/invites/{token}/accept"
AUTH_PUBLIC_ENDPOINTS="/health,/auth/capabilities,/avatars/{userId},/auth/register,/auth/refresh,/auth/magic-link/request,/auth/magic-link/verify,/invites/{token}/info"

# Endpoints that require specific preconditions or complex multi-step flows
# that schemathesis fuzzing cannot satisfy. Exclude to avoid noise.
FLOW_EXCLUDED_ENDPOINTS="/health,/share/cal/{token},/public/lenses/{token},/public/invites/accept,/invites/{token}/info,/invites/{token}/accept"
AUTH_EXCLUDED_ENDPOINTS="/health,/auth/capabilities,/avatars/{userId},/auth/register,/auth/login,/auth/refresh,/auth/logout,/auth/login/totp,/auth/magic-link/request,/auth/magic-link/verify,/auth/oidc/google/start,/auth/oidc/google/callback,/auth/oidc/github/start,/auth/oidc/github/callback,/auth/oidc/microsoft/start,/auth/oidc/microsoft/callback,/oauth/callback/{provider},/invites/{token}/info,/me/avatar,/me/password,/me/totp,/me/totp/enroll,/me/totp/confirm,/me/totp/recovery-codes,/me/integrations/{provider}/connect"

# Common schemathesis options:
#   --checks all           Run all built-in checks (status codes, content type, schema conformance)
#   --phases fuzzing      Generate request cases without stateful scenario chaining
#   --no-shrink           Do not spend extra time minimizing failures
#   --max-response-time 5 Fail if any response takes > 5s
COMMON_OPTS=(
  run
  --checks all
  --phases fuzzing
  --no-shrink
  --max-response-time 5
  --request-timeout 5
  --no-color
  ${SCHEMATHESIS_ARGS:-}
)

# ---------- Dry run ----------

if $DRY_RUN; then
  echo ""
  echo "=== DRY RUN ==="
  echo "Flow spec file: $FLOW_SPEC_FILE"
  echo "Auth spec file: $AUTH_SPEC_FILE"
  echo "flow-api URL (container): $CONTAINER_API_URL"
  echo "auth-api URL (container): $CONTAINER_AUTH_API_URL"
  echo "Token: ${ACCESS_TOKEN:0:20}..."
  echo "flow public endpoints: $FLOW_PUBLIC_ENDPOINTS"
  echo "flow excluded endpoints: $FLOW_EXCLUDED_ENDPOINTS"
  echo "auth public endpoints: $AUTH_PUBLIC_ENDPOINTS"
  echo "auth excluded endpoints: $AUTH_EXCLUDED_ENDPOINTS"
  echo ""
  echo "Would run:"
  echo "  docker run --rm -v $FLOW_SPEC_FILE:/spec.json $DOCKER_HOST_FLAG $SCHEMATHESIS_IMAGE ${COMMON_OPTS[*]} --url $CONTAINER_API_URL /spec.json"
  echo "  docker run --rm -v $AUTH_SPEC_FILE:/spec.json $DOCKER_HOST_FLAG $SCHEMATHESIS_IMAGE ${COMMON_OPTS[*]} --url $CONTAINER_AUTH_API_URL /spec.json"
  exit 0
fi

# ---------- Pull schemathesis image ----------

echo "Pulling schemathesis image ($SCHEMATHESIS_IMAGE) ..."
docker pull "$SCHEMATHESIS_IMAGE" --quiet || {
  echo "WARNING: Could not pull image, using local cache."
}

# ---------- Run: authenticated endpoints ----------

EXIT_CODE=0

contract_run() {
  local label="$1"
  local spec_file="$2"
  local base_url="$3"
  local auth_header="$4"
  local paths_csv="$5"
  local path_mode="$6"

  echo ""
  echo "=== Running contract tests ($label) ==="
  echo ""

  IFS=',' read -ra PATH_ARRAY <<< "$paths_csv"
  PATH_ARGS=()
  for ep in "${PATH_ARRAY[@]}"; do
    ep=$(echo "$ep" | xargs)  # trim whitespace
    if [[ -n "$ep" ]]; then
      PATH_ARGS+=("$path_mode" "$ep")
    fi
  done

  AUTH_ARGS=()
  if [[ -n "$auth_header" ]]; then
    AUTH_ARGS+=(--header "$auth_header")
  fi

  docker run --rm \
    -v "$spec_file":/spec.json:ro \
    $DOCKER_HOST_FLAG \
    "$SCHEMATHESIS_IMAGE" \
    "${COMMON_OPTS[@]}" \
    --url "$base_url" \
    "${AUTH_ARGS[@]}" \
    "${PATH_ARGS[@]}" \
    /spec.json || {
      echo "FAIL: Contract violations detected for $label."
      EXIT_CODE=1
    }
}

contract_run "flow-api authenticated endpoints" "$FLOW_SPEC_FILE" "$CONTAINER_API_URL" "Authorization: Bearer $ACCESS_TOKEN" "$FLOW_EXCLUDED_ENDPOINTS" "--exclude-path"
contract_run "flow-api public endpoints" "$FLOW_SPEC_FILE" "$CONTAINER_API_URL" "" "$FLOW_PUBLIC_ENDPOINTS" "--include-path"
contract_run "auth-api authenticated endpoints" "$AUTH_SPEC_FILE" "$CONTAINER_AUTH_API_URL" "Authorization: Bearer $ACCESS_TOKEN" "$AUTH_EXCLUDED_ENDPOINTS" "--exclude-path"
contract_run "auth-api public endpoints" "$AUTH_SPEC_FILE" "$CONTAINER_AUTH_API_URL" "" "$AUTH_PUBLIC_ENDPOINTS" "--include-path"

# ---------- Summary ----------

echo ""
if [[ $EXIT_CODE -eq 0 ]]; then
  echo "All contract tests passed."
else
  echo "Contract test failures detected. See output above."
fi

exit $EXIT_CODE
