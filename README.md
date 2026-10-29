# nodate-flow

> **Not a tool to manage tasks — a system where work flows forward on its own.**

An OSS task platform where LLMs and MCP are not add-ons but the execution
layer. Tasks are processes whose state is derived from constraints and
events, not rows updated by button clicks.

A single monorepo ships two products served by **one combined backend** (`flow-api`):

- **nodate-flow** — task management driven by constraints, events, and AI agents.
- **nodate-time** — a calendar that blends TimeTree's shared-calendar simplicity with Google Calendar's permission model.

> **About the name:** a pun on _"no date"_ (work free from deadline pressure) and Japanese **野点 (nodate)** — preparing tea outdoors, unhurried.

**Status:** alpha | **License:** [AGPL-3.0](./LICENSE) | **Stack:** Go · MySQL 9.6 · React 19 · OpenAPI 3.1

---

## nodate-flow

| Feature           | Detail                                                                          |
| ----------------- | ------------------------------------------------------------------------------- |
| **Views**         | Board, timeline, Gantt, dashboard, custom lenses                                |
| **Constraints**   | Dependencies, auto-evaluation, derived state — no manual status toggling        |
| **AI**            | Priority & state suggestions, smart create, embedding-based duplicate detection |
| **MCP**           | Built-in server — GitHub, Slack, and external tools live in the same workspace  |
| **Pages**         | Lightweight wiki / docs                                                         |
| **Notifications** | Inbox with weekly digest                                                        |
| **Webhooks**      | Outbound event delivery                                                         |
| **Auth**          | Password (Argon2id) · OIDC · TOTP 2FA · recovery codes                          |
| **Multi-tenant**  | Workspaces with role-based access control                                       |

## nodate-time

| Feature       | Detail                                                |
| ------------- | ----------------------------------------------------- |
| **Calendars** | Shared & personal, per-member visibility control      |
| **Events**    | Attendees, invites, checklists, comments, attachments |
| **Sync**      | Two-way task sync with nodate-flow                    |
| **Holidays**  | Locale-aware holiday scheduling                       |

## Why not Asana / Plane / Linear?

Most tools treat a task as a database row you click through.
nodate-flow treats it as a **process** — state is derived from constraints
and an event log, LLMs act as built-in execution actors, and external
services land in the workspace via MCP rather than bespoke integrations.

Closest peer is [Plane](https://plane.so) (OSS, AGPL, self-host-first),
but Plane stays in the row-and-status paradigm.

## Quick start

```sh
git clone <repo> && cd nodate-flow
make dev          # MySQL (Docker) + auth-api + flow-api + flow-web
make seed-flow    # demo admin user + workspace
```

```
make reload       # stop-dev (kill by port) + dev, for a clean restart
make test-unit    # Go + TS suites that need no container
make test         # Go + TS tests
make gen          # codegen (sqlc + errors + SDK)
make help         # all targets
```

The dev object storage is a different service from earlier checkouts, so an old `minio_data` volume is not carried over. `make dev-reset` seeds fresh data.

## Repo structure

```
apps/
  flow-api/       # Go backend — tasks, calendar, AI, MCP, public shares (Huma + chi + sqlc)
  flow-web/       # React 19 frontend — tasks, calendar, /share/cal, /invites/accept, /setup
  auth-api/       # Go — auth & sessions (JWT, OIDC, TOTP)
  accounts-web/   # React 19 — login / signup / account UI
  cli/            # CLI (binary: tnk)
packages/
  sdk/            # TS SDK for flow-api (generated from OpenAPI)
  ui/             # Design system (4 themes)
  go-shared/      # Shared Go packages
  holidays/       # Holiday data
  fixtures/       # Test fixtures
errors/           # Error codes (YAML → Go + TS codegen)
sql/              # Tables, views, sqlc queries
infra/            # Docker, Prometheus, Grafana, OTel, edge (nginx)
```

## Deployment

`compose.prod.yml` runs the whole stack on one host: MySQL, object storage, auth-api, flow-api, flow-worker and an nginx edge container that serves both SPAs and proxies the two APIs. The images are built from this repository rather than pulled, because the SPAs compile their API origins into the bundle. The edge container is the only service that publishes ports (80 and 443). The database, object storage, API ports and metrics stay on the compose network.

### Prerequisites

Docker with the Compose plugin, and certbot on the host.

Four DNS names that point at the host and share one registrable domain, for example `accounts.example.com`, `app.example.com`, `auth.example.com` and `api.example.com`. The refresh cookie is set by the auth host and read from the app host, so hosts on different registrable domains break sign-in in browsers that block third-party cookies.

### Configuration

Put the settings in a `.env` beside `compose.prod.yml`. These have no default, and Compose stops and names whichever is missing:

- `NF_ACCOUNTS_HOST`, `NF_APP_HOST`, `NF_AUTH_HOST`, `NF_API_HOST`
- `NF_DB_ROOT_PASSWORD`, `NF_DB_PASSWORD`
- `NF_SECRET_KEY`
- `NF_S3_ACCESS_KEY`, `NF_S3_SECRET_KEY`
- `NF_FLOW_API_SIGNAL_TOKEN`
- `NF_SMTP_HOST`, `NF_SMTP_FROM`
- `NF_FLOW_GH_WEBHOOK_SECRET`, `NF_FLOW_SLACK_SIGNING_SECRET`, `NF_FLOW_GOOGLE_CHANNEL_TOKEN`

Generate every secret with `openssl rand -hex 32`. flow-api refuses to start unless all three webhook secrets are set, so give the ones for integrations you do not use a random value too. A random value that nobody knows means no one can forge a request for that integration.

`NF_SMTP_HOST` is required because without it a magic-link request succeeds and sends nothing. `NF_PUBLIC_SCHEME` defaults to `https`. Identity provider credentials (`NF_AUTH_GOOGLE_CLIENT_ID` and the like), `NF_REGISTRATION_OPEN` and the remaining tuning variables are optional; each is passed through from `.env` to the service that reads it.

### Certificates

The certificate has to exist before the stack starts, because nginx will not load a server block whose certificate is missing. Obtain one SAN certificate for all four names first, with port 80 free:

```sh
mkdir -p acme
certbot certonly --standalone \
  -d accounts.example.com -d app.example.com -d auth.example.com -d api.example.com
```

The edge reads the certificate from the directory named after the first `-d` name. If you put another name first, set `NF_TLS_CERT_NAME` to it; the default is `NF_ACCOUNTS_HOST`. `NF_TLS_DIR` (default `/etc/letsencrypt`) and `NF_ACME_WEBROOT` (default `./acme`) move the certificate directory and the challenge directory.

Renewal is answered by the running edge out of the webroot, so nothing has to be stopped. nginx keeps the old certificate until it is told to reload:

```sh
certbot renew --webroot -w /path/to/acme \
  --deploy-hook 'docker compose -f /path/to/compose.prod.yml exec edge nginx -s reload'
```

### First start and the initial administrator

Start everything except the edge, so the instance is not reachable while it has no administrator:

```sh
docker compose -f compose.prod.yml up -d --build mysql storage auth-api api flow-worker
```

Then register the first user and promote it. The commands run from a container on the compose network, whose name is the project name `nodate-flow-prod` plus `_default`. `POST /auth/register` returns the access token that `POST /admin/setup` needs as a bearer token, and `/admin/setup` answers `{"ok":true}` once and 409 afterwards.

```sh
NET=nodate-flow-prod_default
resp=$(docker run --rm --network $NET curlimages/curl -fsS \
  -X POST http://auth-api:8082/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.com","password":"choose-a-long-one","displayName":"Admin"}')
token=$(printf '%s' "$resp" | sed -n 's/.*"accessToken":"\([^"]*\)".*/\1/p')
docker run --rm --network $NET curlimages/curl -fsS \
  -X POST http://auth-api:8082/admin/setup \
  -H "Authorization: Bearer $token"
```

Now start the edge. If the instance is closed to the public, set `NF_REGISTRATION_OPEN=false` in `.env` first:

```sh
docker compose -f compose.prod.yml up -d --build
```

### Identity provider redirect URIs

Register these with each provider, with `<auth>` being `https://` plus `NF_AUTH_HOST`.

Sign-in:

- `<auth>/auth/oidc/google/callback`
- `<auth>/auth/oidc/github/callback`
- `<auth>/auth/oidc/microsoft/callback`

Workspace integrations:

- `<auth>/oauth/callback/github`
- `<auth>/oauth/callback/slack`
- `<auth>/oauth/callback/google`
- `<auth>/oauth/callback/discord`

### Behind a proxy on the host

To terminate TLS somewhere else, drop the `tls.conf.template` mount and the two certificate mounts from the `edge` service and publish `127.0.0.1:8081:80` instead of `80:80` and `443:443`. Keep `NF_PUBLIC_SCHEME=https`, since browsers still see HTTPS, and set `NF_TRUSTED_PROXY_HOPS=2` so the client address is read past both proxies. The host proxy must append to `X-Forwarded-For` rather than replace it, must not buffer responses (the workspace stream is server-sent events), and must accept request bodies up to 128 MB.

### Changing hostnames or the scheme

The hostnames and the scheme are compiled into the SPA bundles. After changing any of them in `.env`, rebuild the edge:

```sh
docker compose -f compose.prod.yml up -d --build edge
```

### Schema

`sql/schema.sql` is applied automatically only when the `mysql_data` volume is empty. To apply it again, stop the application services and run it by hand. The file begins with `DROP TABLE` statements, so this erases all data:

```sh
docker compose -f compose.prod.yml stop edge auth-api api flow-worker
docker compose -f compose.prod.yml exec -T mysql \
  sh -c 'exec mysql -uroot -p"$MYSQL_ROOT_PASSWORD" "$MYSQL_DATABASE"' < sql/schema.sql
docker compose -f compose.prod.yml up -d
```

### Backups

Two volumes hold everything durable, `mysql_data` and `storage_data`. Back them up together, because an attachment is a row in one pointing at an object in the other.

```sh
docker compose -f compose.prod.yml exec -T mysql \
  sh -c 'exec mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" --single-transaction "$MYSQL_DATABASE"' \
  > mysql.sql
docker run --rm -v nodate-flow-prod_storage_data:/data:ro -v "$PWD/storage-backup":/backup \
  alpine sh -c 'apk add --no-cache rsync >/dev/null && rsync -aX /data/ /backup/'
```

The storage gateway keeps each object as a file and its metadata in extended attributes, so the copy has to preserve them (`rsync -aX`).

### Object storage

The bundled S3-compatible gateway answers only inside the compose network. Browsers never reach it: attachments and avatars pass through flow-api's `/blobs` relay. To use an external S3-compatible service instead, remove the `storage` service and its `depends_on` entries, and set `NF_S3_ENDPOINT` and `NF_S3_USE_SSL` on `auth-api` and `api` in `compose.prod.yml` (they are fixed there for the bundled gateway). `NF_S3_ACCESS_KEY`, `NF_S3_SECRET_KEY` and `NF_S3_BUCKET` come from `.env`.

### Discord presence

The Discord presence bot is off by default. Set `NF_DISCORD_BOT_TOKEN` in `.env` and start it with the profile:

```sh
docker compose -f compose.prod.yml --profile presence-discord up -d
```

## License

[AGPL-3.0](./LICENSE) — self-host, modify, redistribute freely. Network
use requires source availability (same model as Plane and Vikunja).
