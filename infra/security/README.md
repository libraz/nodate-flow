# Security Scanning - nodate-flow

Pre-release security tooling and checklists. These tools run in CI
(see `.github/workflows/ci.yml`, security job) and can also be invoked
locally for faster feedback.

---

## Tools Overview

| Tool | Purpose | Covers (OWASP 2021) |
|---|---|---|
| **Semgrep** | Static analysis (SAST) on Go and TypeScript | A03 Injection, A07 Auth Failures, A02 Crypto Failures |
| **Trivy** | Dependency vulnerability scanning (SCA) | A06 Vulnerable Components |
| **govulncheck** | Go-specific vulnerability database check | A06 Vulnerable Components |
| **ZAP** | Dynamic baseline scan (DAST, passive) | A01 Broken Access Control, A05 Security Misconfiguration |
| **gitleaks** (planned) | Secret detection in git history | A07 Auth Failures |
| **npm-audit / bun audit** | JS dependency audit | A06 Vulnerable Components |

---

## Running Scans Locally

### Semgrep (SAST)

```bash
# Install
pip install semgrep

# Run with nodate-flow custom rules
semgrep --config infra/security/.semgrep.yml .

# Run with default auto config (same as CI)
semgrep --config auto .
```

### Trivy (SCA)

```bash
# Install (macOS)
brew install trivy

# Filesystem scan (same as CI)
trivy fs --severity CRITICAL,HIGH --exit-code 1 .

# Scan a built Docker image
trivy image nodate-flow-api:latest
trivy image nodate-flow-web:latest
```

### govulncheck (Go vulnerabilities)

```bash
# Install
go install golang.org/x/vuln/cmd/govulncheck@latest

# Run from the Go module root
cd apps/flow-api && govulncheck ./...
```

### ZAP Baseline Scan (DAST)

```bash
# Start the API server first
docker compose up -d api

# Run ZAP baseline scan (passive only, no active attacks)
docker run --rm --network host \
  -v $(pwd)/infra/security:/zap/wrk:ro \
  ghcr.io/zaproxy/zaproxy:stable \
  zap-baseline.py \
    -t http://localhost:8787 \
    -c zap-baseline.conf \
    -J zap-report.json

# Report is written to the current directory
```

### JS Dependency Audit

```bash
bun install --frozen-lockfile
bunx npm-audit --production
```

---

## Pre-Release Security Checklist (OWASP Top 10)

### A01: Broken Access Control

- [ ] All API endpoints enforce authentication (middleware check)
- [ ] Role-based access control (ACL) tested for each role
- [ ] Public endpoints are explicitly allowlisted
- [ ] CORS configuration restricts allowed origins
- [ ] Rate limiting is active on authentication endpoints
- [ ] MCP token scopes are enforced per-endpoint
- [ ] ZAP baseline scan passes without HIGH findings

### A02: Cryptographic Failures

- [ ] All secrets encrypted at rest (AES-256-GCM via `internal/crypto`)
- [ ] TLS enforced in production (Caddy auto-HTTPS)
- [ ] JWT uses EdDSA (Ed25519), not HMAC
- [ ] Password hashing uses Argon2id with recommended parameters
- [ ] Semgrep crypto error-check rules pass
- [ ] No secrets in logs (redaction handler verified)

### A03: Injection

- [ ] All SQL queries use parameterized statements (sqlc enforces this)
- [ ] Semgrep SQL injection rules pass
- [ ] No template string interpolation in SQL
- [ ] Input validation via Huma schema validation on all endpoints

### A04: Insecure Design

- [ ] Threat model documented for auth, MCP, and AI provider flows
- [ ] Rate limiting on all public-facing endpoints
- [ ] Account lockout after N failed login attempts

### A05: Security Misconfiguration

- [ ] Production compose file does not expose debug ports
- [ ] MySQL `max_connections` and charset configured correctly
- [ ] No default credentials in any configuration
- [ ] Health/metrics endpoints not exposed publicly
- [ ] ZAP baseline scan checks security headers

### A06: Vulnerable and Outdated Components

- [ ] Trivy filesystem scan: zero CRITICAL/HIGH
- [ ] govulncheck: zero known vulnerabilities
- [ ] JS dependency audit: zero HIGH/CRITICAL
- [ ] Base Docker images use pinned versions (distroless/alpine)
- [ ] Go and Bun versions pinned in CI

### A07: Identification and Authentication Failures

- [ ] PATs stored as SHA-256 hashes, never reversible
- [ ] API keys stored as AES-256-GCM ciphertext
- [ ] No hardcoded keys (Semgrep custom rules enforce this)
- [ ] Token expiry enforced for JWT
- [ ] Refresh token rotation implemented
- [ ] gitleaks or equivalent checks git history

### A08: Software and Data Integrity Failures

- [ ] CI uses pinned action versions (`@v4`, `@v5`)
- [ ] `bun install --frozen-lockfile` enforces lockfile integrity
- [ ] Docker images built from verified base images
- [ ] Changesets enforce versioning discipline

### A09: Security Logging and Monitoring Failures

- [ ] All authentication events logged (login, logout, failure)
- [ ] Secret operations logged to audit_logs (create, delete, rotate)
- [ ] Sensitive values redacted in logs (RedactingHandler)
- [ ] OpenTelemetry traces include security-relevant spans
- [ ] Prometheus alerts configured for anomalous auth failure rates

### A10: Server-Side Request Forgery (SSRF)

- [ ] Webhook URLs validated against allowlist
- [ ] AI provider base URLs restricted to known domains
- [ ] MCP server connections validate target addresses
- [ ] No user-controlled URLs passed to server-side HTTP clients without validation

---

## Configuration Files

| File | Purpose |
|---|---|
| `infra/security/zap-baseline.conf` | ZAP passive scan rules and exclusions |
| `infra/security/.semgrep.yml` | Custom Semgrep rules for nodate-flow patterns |
| `.trivyignore` | Accepted CVE exceptions with documented reasons |
| `.github/workflows/ci.yml` (security job) | CI integration for Trivy, Semgrep, govulncheck |
