#!/usr/bin/env bash
# Contract test: run Schemathesis against the live API to detect spec drift.
#
# Prerequisites:
#   - API running at NF_API_URL (default: http://localhost:8080)
#   - Docker available (schemathesis runs in a container)
#   - packages/sdk/openapi.json up to date (run dump-openapi first)
#
# Usage:
#   ./scripts/contract-test.sh              # full run
#   ./scripts/contract-test.sh --dry-run    # show what would run
#
# Environment variables:
#   NF_API_URL          Base URL of the API (default: http://localhost:8080)
#   NF_CONTRACT_EMAIL   Email for test user (default: auto-generated)
#   NF_CONTRACT_PASS    Password for test user (default: ContractTest1!)
#   SCHEMATHESIS_IMAGE  Docker image (default: schemathesis/schemathesis:stable)
#   SCHEMATHESIS_ARGS   Extra args passed to schemathesis (default: empty)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

API_URL="${NF_API_URL:-http://localhost:8080}"
SPEC_FILE="$ROOT_DIR/packages/sdk/openapi.json"
SCHEMATHESIS_IMAGE="${SCHEMATHESIS_IMAGE:-schemathesis/schemathesis:stable}"
DRY_RUN=false

if [[ "${1:-}" == "--dry-run" ]]; then
  DRY_RUN=true
fi

# ---------- Preflight checks ----------

if [[ ! -f "$SPEC_FILE" ]]; then
  echo "ERROR: OpenAPI spec not found at $SPEC_FILE"
  echo "       Run 'go run ./apps/flow-api/cmd/dump-openapi' first."
  exit 1
fi

if ! command -v docker &>/dev/null; then
  echo "ERROR: docker is required but not found."
  exit 1
fi

if ! command -v curl &>/dev/null; then
  echo "ERROR: curl is required but not found."
  exit 1
fi

# Check API is reachable.
echo "Checking API at $API_URL/health ..."
if ! curl -sf "$API_URL/health" >/dev/null 2>&1; then
  echo "ERROR: API is not responding at $API_URL/health"
  echo "       Start the API first (compose up / go run)."
  exit 1
fi
echo "API is up."

# ---------- Create test user and obtain token ----------

CONTRACT_EMAIL="${NF_CONTRACT_EMAIL:-contract-test-$(date +%s)@test.nodate.local}"
CONTRACT_PASS="${NF_CONTRACT_PASS:-ContractTest1!}"
CONTRACT_NAME="Contract Test User"

echo "Registering test user: $CONTRACT_EMAIL ..."
REGISTER_RESP=$(curl -sf -X POST "$API_URL/auth/register" \
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
  LOGIN_RESP=$(curl -sf -X POST "$API_URL/auth/login" \
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
if [[ "$API_URL" == *"localhost"* ]] || [[ "$API_URL" == *"127.0.0.1"* ]]; then
  DOCKER_HOST_FLAG="--add-host=host.docker.internal:host-gateway"
  # Replace localhost/127.0.0.1 with host.docker.internal for the container.
  CONTAINER_API_URL=$(echo "$API_URL" | sed -E 's/(localhost|127\.0\.0\.1)/host.docker.internal/g')
else
  DOCKER_HOST_FLAG=""
  CONTAINER_API_URL="$API_URL"
fi

# On macOS, host.docker.internal works without --add-host.
if [[ "$(uname -s)" == "Darwin" ]]; then
  DOCKER_HOST_FLAG=""
fi

# ---------- Build schemathesis command ----------

# Endpoints that are public (no auth needed). We test these separately without a token.
PUBLIC_ENDPOINTS="/health,/auth/register,/auth/login,/auth/refresh"

# Endpoints that require specific preconditions or complex multi-step flows
# that schemathesis fuzzing cannot satisfy. Exclude to avoid noise.
EXCLUDED_ENDPOINTS="/auth/oidc/google/callback,/auth/oidc/google/start,/oauth/callback/{provider},/auth/login/totp"

# Common schemathesis options:
#   --checks all           Run all built-in checks (status codes, content type, schema conformance)
#   --validate-schema false  Skip spec validation (Huma generates $schema fields that confuse validators)
#   --hypothesis-phases generate  Only generate, don't shrink (faster)
#   --stateful none        No stateful testing (simpler, faster)
#   --max-response-time 5000  Fail if any response takes > 5s
COMMON_OPTS=(
  run
  --checks all
  --validate-schema false
  --hypothesis-phases generate
  --stateful none
  --max-response-time 5000
  --show-errors-tracebacks
  --cassette-path /tmp/schemathesis-cassette.yaml
  ${SCHEMATHESIS_ARGS:-}
)

# ---------- Dry run ----------

if $DRY_RUN; then
  echo ""
  echo "=== DRY RUN ==="
  echo "Spec file: $SPEC_FILE"
  echo "API URL (container): $CONTAINER_API_URL"
  echo "Token: ${ACCESS_TOKEN:0:20}..."
  echo "Public endpoints: $PUBLIC_ENDPOINTS"
  echo "Excluded endpoints: $EXCLUDED_ENDPOINTS"
  echo ""
  echo "Would run:"
  echo "  docker run --rm -v $SPEC_FILE:/spec.json $DOCKER_HOST_FLAG $SCHEMATHESIS_IMAGE ${COMMON_OPTS[*]} --base-url $CONTAINER_API_URL /spec.json"
  exit 0
fi

# ---------- Pull schemathesis image ----------

echo "Pulling schemathesis image ($SCHEMATHESIS_IMAGE) ..."
docker pull "$SCHEMATHESIS_IMAGE" --quiet || {
  echo "WARNING: Could not pull image, using local cache."
}

# ---------- Run: authenticated endpoints ----------

EXIT_CODE=0

echo ""
echo "=== Running contract tests (authenticated endpoints) ==="
echo ""

IFS=',' read -ra EXCL_ARRAY <<< "$EXCLUDED_ENDPOINTS,$PUBLIC_ENDPOINTS"
EXCLUDE_ARGS=()
for ep in "${EXCL_ARRAY[@]}"; do
  ep=$(echo "$ep" | xargs)  # trim whitespace
  if [[ -n "$ep" ]]; then
    EXCLUDE_ARGS+=(--exclude-path "$ep")
  fi
done

docker run --rm \
  -v "$SPEC_FILE":/spec.json:ro \
  $DOCKER_HOST_FLAG \
  "$SCHEMATHESIS_IMAGE" \
  "${COMMON_OPTS[@]}" \
  --base-url "$CONTAINER_API_URL" \
  --header "Authorization: Bearer $ACCESS_TOKEN" \
  "${EXCLUDE_ARGS[@]}" \
  /spec.json || {
    echo "FAIL: Authenticated endpoint contract violations detected."
    EXIT_CODE=1
  }

# ---------- Run: public endpoints ----------

echo ""
echo "=== Running contract tests (public endpoints) ==="
echo ""

IFS=',' read -ra PUB_ARRAY <<< "$PUBLIC_ENDPOINTS"
INCLUDE_ARGS=()
for ep in "${PUB_ARRAY[@]}"; do
  ep=$(echo "$ep" | xargs)
  if [[ -n "$ep" ]]; then
    INCLUDE_ARGS+=(--include-path "$ep")
  fi
done

docker run --rm \
  -v "$SPEC_FILE":/spec.json:ro \
  $DOCKER_HOST_FLAG \
  "$SCHEMATHESIS_IMAGE" \
  "${COMMON_OPTS[@]}" \
  --base-url "$CONTAINER_API_URL" \
  "${INCLUDE_ARGS[@]}" \
  /spec.json || {
    echo "FAIL: Public endpoint contract violations detected."
    EXIT_CODE=1
  }

# ---------- Summary ----------

echo ""
if [[ $EXIT_CODE -eq 0 ]]; then
  echo "All contract tests passed."
else
  echo "Contract test failures detected. See output above."
fi

exit $EXIT_CODE
