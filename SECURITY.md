# Security Policy

## Reporting a vulnerability

If you discover a security vulnerability in nodate-flow, please report it
responsibly. **Do not open a public GitHub issue.**

Email **security@nodate-flow.dev** with:

- A description of the vulnerability.
- Steps to reproduce or a proof-of-concept.
- The version(s) affected, if known.
- Your assessment of severity (critical / high / medium / low).

You will receive an acknowledgment within **48 hours** and a substantive
response (fix timeline or questions) within **7 business days**.

We will coordinate disclosure with you. If you follow responsible disclosure,
we will credit you in the release notes (unless you prefer anonymity).

## Supported versions

nodate-flow has not yet reached a stable release. Security fixes are applied
to the development branch (`main`) as they are discovered.

| Version | Supported |
|---|---|
| `main` (development) | Yes |
| Pre-release tags | Best effort |

Once v1.0.0 ships, this table will be updated with a formal support window.

## Scope

The following are **in scope** for security reports:

- Authentication and authorization bypasses.
- SQL injection, XSS, CSRF, SSRF.
- Secrets or credentials leaked in responses, logs, or error messages.
- Privilege escalation (cross-tenant data access, role bypass).
- Insecure cryptographic usage (JWT, AES-256-GCM, Argon2 parameters).
- Dependency vulnerabilities with a demonstrable exploit path.
- MCP tool invocations that bypass access control.

The following are **out of scope**:

- Denial-of-service attacks that require excessive resources to execute.
- Vulnerabilities in third-party services that nodate-flow integrates with
  (report those to the respective maintainers).
- Social engineering attacks against project maintainers.
- Issues in development tooling (docker compose, Makefile) that do not
  affect production deployments.
- Missing security headers on `localhost` development servers.

## Disclosure timeline

We aim to follow this timeline after receiving a report:

1. **48 hours** -- acknowledge receipt.
2. **7 business days** -- provide initial assessment and expected fix timeline.
3. **30 days** -- release a fix (or agree on an extended timeline for complex issues).
4. **After fix** -- coordinate public disclosure with the reporter.

We may request an extension for complex issues. We will keep reporters
informed throughout the process.

## Security measures in the codebase

- Passwords are hashed with Argon2id.
- JWTs use EdDSA (Ed25519) signatures.
- Encryption at rest uses AES-256-GCM.
- All database queries use parameterized statements (sqlc, no string
  concatenation).
- Secrets are never logged (slog redaction middleware).
- Rate limiting is applied to authentication and API endpoints.
- Tenant isolation is enforced at the query level.
