# Deployment

`compose.prod.yml` runs the whole stack on one host: MySQL, object storage, auth-api, flow-api, flow-worker and an nginx edge container that serves both SPAs and proxies the two APIs. The images are built from this repository rather than pulled, because the SPAs compile their API origins into the bundle. The services and how they connect are described in [Architecture](architecture.md).

One hostname, one certificate, and nothing else published. The edge container is the only way in and the only service that publishes ports (80 and 443). It terminates TLS and routes by path: `/` to flow-web, `/accounts/` to accounts-web, `/api/` to flow-api and `/auth-api/` to auth-api. The two API prefixes are stripped before the request is proxied. The database, object storage, API ports and metrics stay on the compose network.

## Prerequisites

Docker with the Compose plugin, and certbot on the host.

One DNS name that points at the host, for example `flow.example.com`. Everything is served from that origin, so the refresh cookie is first-party.

## Configuration

Put the settings in a `.env` beside `compose.prod.yml`. These have no default, and Compose stops and names whichever is missing:

- `NF_PUBLIC_HOST`
- `NF_DB_ROOT_PASSWORD`, `NF_DB_PASSWORD`
- `NF_SECRET_KEY`
- `NF_S3_ACCESS_KEY`, `NF_S3_SECRET_KEY`
- `NF_FLOW_API_SIGNAL_TOKEN`
- `NF_FLOW_GH_WEBHOOK_SECRET`, `NF_FLOW_SLACK_SIGNING_SECRET`, `NF_FLOW_GOOGLE_CHANNEL_TOKEN`

Generate every secret with `openssl rand -hex 32`. flow-api refuses to start unless all three webhook secrets are set, so give the ones for integrations you do not use a random value too. A random value that nobody knows means no one can forge a request for that integration.

SMTP is optional (`NF_SMTP_HOST`, `NF_SMTP_FROM`, and the credentials beside them). Without a host, magic-link sign-in is not offered and no mail is sent: no invitation or notification emails. `NF_PUBLIC_SCHEME` defaults to `https`. The public URLs are derived from the two: `NF_AUTH_PUBLIC_URL` is `https://<host>/auth-api`, `NF_FLOW_PUBLIC_URL` is `https://<host>/api`, and accounts-web is at `https://<host>/accounts`. Identity provider credentials (`NF_AUTH_GOOGLE_CLIENT_ID` and the like), `NF_REGISTRATION_OPEN` and the remaining tuning variables are optional; each is passed through from `.env` to the service that reads it.

`NF_APP_NAME` sets the product name shown in both web apps, the tab title, the subject of the magic-link mail, the issuer in authenticator apps and the title of the product API reference. It defaults to `nodate-flow` and may be up to 64 characters, with no `:` and no control characters. `NF_APP_LOGO_URL` is a logo that already contains the name; it replaces the name text on the sign-in pages, the sidebar and the public share pages. `NF_APP_ICON_URL` is a square symbol; it replaces the built-in mark, is shown next to the name, and is the favicon. If you set only the icon, the name is still shown; if you set a logo and no icon, the collapsed sidebar falls back to the built-in mark. A square image that was previously set as `NF_APP_LOGO_URL` belongs in `NF_APP_ICON_URL`. `NF_APP_LOGO_DARK_URL` and `NF_APP_ICON_DARK_URL` replace the images in dark themes and require their base variable. Each value is an `https://` URL or a `data:image/png`, `svg+xml`, `webp`, `jpeg` or `gif` URI of up to 32 KiB, written in printable ASCII with no inner spaces; use punycode for an internationalized hostname. Outgoing mail shows only `https://` images, so use a URL if you want the logo or icon in the mail header. The name and the four image variables are compiled into the SPA bundles, so after changing any of them, rebuild the edge as described in [Changing the hostname or the scheme](#changing-the-hostname-or-the-scheme). The sender address of outgoing mail is set separately with `NF_SMTP_FROM`. After a rename, people who re-enrol two-factor authentication may see a second entry in their authenticator app; codes from the existing entry keep working.

## OIDC-only sign-in

To allow sign-in through an identity provider only, with no password accounts and no SMTP, set these in `.env`:

```sh
NF_PASSWORD_LOGIN_ENABLED=false
NF_AUTH_GOOGLE_CLIENT_ID=...
NF_AUTH_GOOGLE_CLIENT_SECRET=...
NF_OAUTH_ALLOWED_DOMAINS=example.com        # and/or NF_OAUTH_ALLOWED_EMAILS=ceo@example.com
NF_REGISTRATION_OPEN=true
```

With `NF_PASSWORD_LOGIN_ENABLED=false`, auth-api does not register `POST /auth/register`, `POST /auth/login`, `POST /me/password` or the TOTP management routes under `/me/totp`, and the web apps show only the provider buttons. The second factor is the identity provider's own. An account that enrolled TOTP before the switch keeps its challenge (`POST /auth/login/totp`) and its recovery codes, but cannot manage TOTP until password login is enabled again. `NF_REGISTRATION_OPEN=true` then opens only OIDC auto-provisioning, which the allowlist restricts to the listed domains and addresses. Without an allowlist, any verified address at the provider can create an account, so set one. Leave `NF_SMTP_HOST` unset and magic-link sign-in stays off too.

## Certificates

The certificate has to exist before the stack starts, because nginx will not load a server block whose certificate is missing. Obtain one first, with port 80 free:

```sh
mkdir -p acme
certbot certonly --standalone -d flow.example.com
```

The edge reads the certificate from `/etc/letsencrypt/live/<NF_TLS_CERT_NAME>/`, which defaults to `NF_PUBLIC_HOST`, the directory certbot names after the `-d` name. `NF_TLS_DIR` (default `/etc/letsencrypt`) and `NF_ACME_WEBROOT` (default `./acme`) move the certificate directory and the challenge directory.

Renewal is answered by the running edge out of the webroot, so nothing has to be stopped. nginx keeps the old certificate until it is told to reload:

```sh
certbot renew --webroot -w /path/to/acme \
  --deploy-hook 'docker compose -f /path/to/compose.prod.yml exec edge nginx -s reload'
```

## First start and the initial administrator

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

On an OIDC-only instance there is no password to register with. Start the whole stack instead, with the allowlist set so that only your own address can sign in. Sign in once through the provider to create the account, then grant it administrator rights. The command is safe to repeat:

```sh
docker compose -f compose.prod.yml up -d --build
docker compose -f compose.prod.yml exec auth-api /grant-admin -email admin@example.com
```

`grant-admin` fails with a non-zero exit code if no user has that address yet.

On a password instance, now start the edge. If the instance is closed to the public, set `NF_REGISTRATION_OPEN=false` in `.env` first:

```sh
docker compose -f compose.prod.yml up -d --build
```

## Identity provider redirect URIs

Register these with each provider, with `<auth>` being `https://` plus `NF_PUBLIC_HOST` plus `/auth-api`.

Sign-in:

- `<auth>/auth/oidc/google/callback`
- `<auth>/auth/oidc/github/callback`
- `<auth>/auth/oidc/microsoft/callback`

Workspace integrations:

- `<auth>/oauth/callback/github`
- `<auth>/oauth/callback/slack`
- `<auth>/oauth/callback/google`
- `<auth>/oauth/callback/discord`

## Behind a proxy on the host

To terminate TLS somewhere else, drop the `tls.conf.template` mount and the two certificate mounts from the `edge` service and publish `127.0.0.1:8081:80` instead of `80:80` and `443:443`. Keep `NF_PUBLIC_SCHEME=https`, since browsers still see HTTPS, and set `NF_TRUSTED_PROXY_HOPS=2` so the client address is read past both proxies. The host proxy must append to `X-Forwarded-For` rather than replace it, must not buffer responses (the workspace stream is server-sent events), and must accept request bodies up to 128 MB.

## Changing the hostname or the scheme

The hostname and the scheme are compiled into the SPA bundles. After changing any of them in `.env`, rebuild the edge:

```sh
docker compose -f compose.prod.yml up -d --build edge
```

## Schema

`sql/schema.sql` is applied automatically only when the `mysql_data` volume is empty. To apply it again, stop the application services and run it by hand. The file begins with `DROP TABLE` statements, so this erases all data:

```sh
docker compose -f compose.prod.yml stop edge auth-api api flow-worker
docker compose -f compose.prod.yml exec -T mysql \
  sh -c 'exec mysql -uroot -p"$MYSQL_ROOT_PASSWORD" "$MYSQL_DATABASE"' < sql/schema.sql
docker compose -f compose.prod.yml up -d
```

`sql/schema.sql` holds all three schema layers: core, the time layer shared with nodate-time, and flow. When nodate-time runs on the same database, re-applying the file erases its data too. To keep the data, upgrade in place instead: stop both applications, apply `sql/core/upgrades/*.sql` in number order, then the time layer's upgrade in `sql/time/upgrades/`, and check the result with `bash sql/core/conformance/run.sh --dsn 'user:pass@host:port/db' --mode schema`. `sql/core/PROTOCOL.md` describes the order.

## Backups

Two volumes hold everything durable, `mysql_data` and `storage_data`. Back them up together, because an attachment is a row in one pointing at an object in the other.

```sh
docker compose -f compose.prod.yml exec -T mysql \
  sh -c 'exec mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" --single-transaction "$MYSQL_DATABASE"' \
  > mysql.sql
docker run --rm -v nodate-flow-prod_storage_data:/data:ro -v "$PWD/storage-backup":/backup \
  alpine sh -c 'apk add --no-cache rsync >/dev/null && rsync -aX /data/ /backup/'
```

The storage gateway keeps each object as a file and its metadata in extended attributes, so the copy has to preserve them (`rsync -aX`).

## Object storage

The bundled S3-compatible gateway answers only inside the compose network. Browsers never reach it: attachments and avatars pass through flow-api's `/blobs` relay. To use an external S3-compatible service instead, remove the `storage` service and its `depends_on` entries, and set `NF_S3_ENDPOINT` and `NF_S3_USE_SSL` on `auth-api` and `api` in `compose.prod.yml` (they are fixed there for the bundled gateway). `NF_S3_ACCESS_KEY`, `NF_S3_SECRET_KEY` and `NF_S3_BUCKET` come from `.env`.

## Discord presence

The Discord presence bot is off by default. Set `NF_DISCORD_BOT_TOKEN` in `.env` and start it with the profile:

```sh
docker compose -f compose.prod.yml --profile presence-discord up -d
```
