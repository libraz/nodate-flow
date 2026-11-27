# Deployment

`compose.prod.yml` runs the whole stack on one host: MySQL, object storage, auth-api, flow-api, flow-worker and an nginx edge container that serves both SPAs and proxies the two APIs. The images are built from this repository rather than pulled, because the SPAs compile their API origins into the bundle. The services and how they connect are described in [Architecture](architecture.md).

One hostname, one certificate, and nothing else published. The edge container is the only way in and the only service that publishes ports (80 and 443). It terminates TLS and routes by path: `/` to flow-web, `/accounts/` to accounts-web, `/api/` to flow-api and `/auth-api/` to auth-api. The two API prefixes are stripped before the request is proxied. The database, object storage, API ports and metrics stay on the compose network.

## Prerequisites

Docker with the Compose plugin, and certbot on the host.

The bundled database uses MySQL Community 26.10.0.

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

`NF_FLOW_EMBED_OPENAI_KEY` enables server-wide task embeddings. Use the file source in production to keep the key value out of the container environment:

```sh
# .env
NF_FLOW_EMBED_OPENAI_KEY_FILE=/run/secrets/embedding-openai-key
```

Create a separate Compose override such as `compose.embedding-secret.yml` and mount the host file read-only into the API container:

```yaml
services:
  api:
    volumes:
      - type: bind
        source: /etc/nodate-flow/secrets/embedding-openai-key
        target: /run/secrets/embedding-openai-key
        read_only: true
        bind:
          create_host_path: false
```

Create the host file through your secret manager. Set its owner to numeric UID and GID `65532`, the API container user, and its mode to `0400`. Keep the containing directory accessible only to root. Start the API with both Compose files:

```sh
docker compose -f compose.prod.yml -f compose.embedding-secret.yml up -d --build --force-recreate api
```

Include `-f compose.embedding-secret.yml` in every Compose command that starts or recreates services, including the first-start commands below. Configure exactly one key source. The file must contain a non-empty key. A single final line ending is accepted. The process reads the key at startup, so recreate `api` after rotating the file. Workspaces inherit this server-wide embedding provider by default and can disable it under Settings → AI → Connection. This key is separate from the LLM keys registered for each workspace. When the key is set directly through `NF_FLOW_EMBED_OPENAI_KEY`, Docker exposes its value in container environment metadata.

The default model is `text-embedding-3-small`. Use `NF_FLOW_EMBED_MODEL` and `NF_FLOW_EMBED_BASE_URL` to select a model and an OpenAI-compatible endpoint. The base URL must use HTTPS in production.

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

`sql/schema.sql` holds all three schema layers: core, the time layer shared with nodate-time, and flow. When nodate-time runs on the same database, re-applying the file erases its data too.

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

## Migrating from Asana

`importasana` copies Asana projects into a workspace and, while you are still moving over, keeps them in step with Asana. It is a command-line tool that ships in the flow-api image as `/importasana`, so it runs inside the `api` container, where the database and object storage settings are already in place.

### What is imported

- Projects, with the people you list added as members. The importer becomes the project lead.
- Tasks and subtasks, with their parent links, assignee, followers (as watchers), start and due dates, and completed state. Milestones and approvals become ordinary tasks.
- Comments, with their author and their original time. Task creation times are also kept.
- Tags as workspace labels, and sections as labels limited to the project.
- "Blocks" dependencies between imported tasks.
- Attachments stored in Asana. The file is copied into object storage, so `NF_S3_ENDPOINT` must be set; the bundled compose file already sets it.

What is not imported:

- Custom fields, portfolios, goals, project status updates, approval outcomes, likes and activity history such as reassignments. A dry-run counts them.
- Files kept on other hosts (Google Drive, Dropbox, Box, OneDrive, Vimeo). They are listed as links at the end of the task description.
- Anything in the other direction. Nothing is written back to Asana.

A few values are not carried over as they were:

- `completed_at` is the time the task was marked done in flow, which is the import time for tasks that were already done. Lead-time figures for work finished before the move are therefore not meaningful.
- Titles longer than 500 characters are cut, and the full title goes at the top of the description. Descriptions and comments are cut at 50000 characters.
- Rich text is converted to Markdown. People mentioned in it appear as plain `@Name`, not as mentions, so nobody is notified.
- A start date later than the due date is dropped.

### Before you start

You need three things.

1. An Asana personal access token. Create one in the Asana developer console. It acts as the person who created it and sees only what that person sees, so use an account that is a member of every project you want to copy. Pass it in the environment variable `ASANA_TOKEN`. There is no flag for it, so it stays out of the process list and the shell history.
2. A workspace to import into. The account named as `importer` in the mapping must be an enabled owner or admin of it.
3. A mapping file, described next.

`NF_OAUTH_ALLOWED_DOMAINS` has to be set to the value auth-api runs with; see [Accounts and sign-in](#accounts-and-sign-in).

### The mapping file

```yaml
workspace: acme # flow workspace slug (required, must exist)
source_workspace: "1200000000" # Asana workspace gid or unique name; omit if the token sees exactly one
importer: ops@example.com # runs the import; leads the projects it creates
users: # accounts to create
  - { email: hanako@example.com, name: Hanako Yamada }
source_users: # Asana user gid -> email; wins over matching by email
  "1201234567890": hanako@example.com
projects:
  - source: "1209876543210" # Asana project gid, or a name that matches exactly one project
    name: Marketing # defaults to the Asana name
    slug: marketing # defaults to asana-<project gid>; used only when the project is created
    identifier: MKT # optional, up to 5 letters or digits; used only when the project is created
    members:
      - { email: hanako@example.com, role: editor } # lead, editor, commenter or viewer
    sections: # Asana section name -> label name
      "Untitled section": "" # an empty value attaches no label; unlisted sections keep their name
```

- Write gids as quoted strings.
- A name in `source` must match one project only. If it also matches a gid, or two projects, the mapping is refused.
- `importer` is not listed under `members`.
- If a project with the same name already exists and the importer leads it, the importer reuses it and ignores `slug` and `identifier`. Otherwise it creates the project.
- An unknown key, an empty file or a duplicate is refused before anything is written. Each error is printed with the path of the value, for example `projects[0].members[1].role: "owner" is not one of lead, editor, commenter, viewer`.

### Accounts and sign-in

Each person in Asana is matched to an account in this order: the `source_users` entry for their gid, then the email address Asana reports for them. A person who matches an account that is not a member of the project is treated as unmatched for that project.

- An unmatched assignee or follower is dropped.
- A comment by an unmatched person is written under the importer's name, with `**<Asana name>:**` at the start of the text.
- The dry-run lists every Asana person with how they matched (`explicit`, `email`, `not-member` or `unmatched`) and how many tasks and comments they touch. Fix unmatched people with `source_users` or `users` and run the dry-run again.

Accounts listed under `users` are created without a password and added to the workspace. The person takes the account over the first time they sign in through the identity provider.

When sign-in is restricted, the address also has to be allowed to sign in, so the importer adds it to the sign-in allowlist with the note `created by importasana`. When sign-in is open it adds nothing, because an allowlist row would switch the restriction on. The tool decides which case applies from the allowlist table and from `NF_OAUTH_ALLOWED_DOMAINS`. That variable is set on auth-api, not on the `api` service, so pass the same value to the import command (the example below does). If it is missing, a deployment restricted by domain is reported as open, and the people created cannot sign in. The dry-run prints `sign-in restriction: open` or `sign-in restriction: domains=<n> allowlist=<n>`; check it matches what you expect before the real run.

### Workflow

Run the tool from the directory that holds `compose.prod.yml`, with the token and the domains in your shell. The mapping is read from standard input:

```sh
export ASANA_TOKEN=...                    # personal access token
export NF_OAUTH_ALLOWED_DOMAINS=example.com   # same value as auth-api; empty if none

imp() {
  docker compose -f compose.prod.yml exec -T \
    -e ASANA_TOKEN -e NF_OAUTH_ALLOWED_DOMAINS \
    api /importasana -mapping - "$@" < mapping.yaml
}

imp -dry-run          # 1. read the plan, write nothing
imp                   # 2. import
imp -sync -dry-run    # 3. while migrating: read what a sync would change
imp -sync             # 4. follow Asana
```

1. **Dry-run.** It reads everything from Asana and the database, prints the plan and writes nothing. Fix whatever it reports and repeat until `validation errors: 0`.
2. **Import.** Creates what is missing. A second run without `-sync` creates only what is still missing, such as a comment or an attachment that failed the first time. It never changes or deletes a task it already imported, so it is safe to repeat.
3. **Sync.** With `-sync`, Asana wins. For tasks and comments that were imported earlier:
   - Title, description, dates and completed state are overwritten, so a local edit to them is lost.
   - Assignees and watchers are compared for people who match an Asana person. Other people on the task, and reviewers and approvers, are left alone.
   - Labels that come from Asana tags and sections are compared. A label you attach in flow is kept, unless it has the same name as an Asana tag, in which case it counts as that tag.
   - "Blocks" dependencies between imported tasks are made to match Asana. One you draw in flow between two of them is removed.
   - Comment text is overwritten. The author is not.
   - A task or comment that is gone from Asana is deleted in flow the way a deletion in the app is, so it can be traced in the event log.
   - Completion follows Asana only when it crosses a boundary: done in Asana completes the task, and reopened in Asana moves a done task to waiting. A cancelled task is left as it is, and a completion refused by a `review_required` policy is skipped. Both are counted in the notes line.

   Run the sync dry-run first, every time. Not synced: a change of parent, a deleted attachment, a removed project member, a renamed project, and a renamed or deleted label. The notes line counts them.

Before a sync, the importer checks each listed task with Asana individually. A task whose check fails with a permission error, a server error or a timeout is kept, not deleted.

A task moved to another project in the mapping is deleted from the old project and created in the new one, and counted as `moved`. What was added to it in flow is lost: comments, labels, reviewers and approvers, a waiting or review state, dependencies you drew and the task number.

The import writes events but does not trigger notifications, emails, webhooks or agent runs, because the importer runs as its own process. One thing does run: the scheduled auto-action rules (every five minutes) treat imported tasks like any other, so an overdue task can be escalated within minutes of arriving. To avoid that, turn the auto-actions off in the workspace's AI settings for the duration of the move. A browser that is already open may refetch its lists when the import writes, but it shows no notification.

Only one run per workspace at a time. A second one exits at once with code 2.

### Exit codes

| Code | Meaning                                                                                                                                                                                                                                                             |
| ---- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 0    | Success.                                                                                                                                                                                                                                                            |
| 1    | The run started and at least one write failed. Everything else it wrote stays, and the `failure:` lines say what failed. Run it again to fill the gaps.                                                                                                             |
| 2    | Nothing was written: no `ASANA_TOKEN`, an invalid mapping or one that does not match Asana or the database, an Asana request that kept failing, a sync that was refused, or another run holding the lock. The reason is a line starting `error:` on standard error. |

### Reading the report

A dry-run prints, in this order:

```
Asana workspace "Acme" (1200000000): projects=14 mapped=2 users=31 tags=18
Asana project 1209876543210 name="Marketing" archived=false
  mapping: mapped (source: 1209876543210)
  tasks: 212 (top-level 180, subtasks 32; default 209, milestone 3, approval 0; completed 150)
  by section: "To do"=40, "Doing"=22, "Done"=150
  tags: "launch"=12, "press"=4
  comments: 388; attachments: asana=41 external=3 too_large=0 type_not_allowed=1
  dependencies: blocks=9 outside_the_mapping=0 cycle=0
  person "Hanako Yamada" (1201234567890) explicit -> hanako@example.com: assignee=60 creator=80 commenter=120 follower=15
  skipped: empty title=0, parent not imported=0, empty body=2, too large=0, type not allowed=1, outside the mapping=0, cycle=0
  not imported: custom_fields=212 likes=17 system_stories=903
  project "Marketing": create (slug "marketing")
sign-in restriction: domains=1 allowlist=0
users to create: hanako@example.com
validation errors: 0
```

With `-sync`, each project also gets a line per kind of item, before the last three:

```
project "Marketing": sync plan tasks create=3 update=5 unchanged=204 delete=1 moved=0 kept=0 synced=205 orphaned=0 repeated=0
```

`create`, `update`, `unchanged`, `delete`, `moved` and `kept` are what the real run would count as imported, updated, duplicates, deleted, moved and kept. `synced` is how many live imported tasks the project has. `orphaned` counts rows that carry a source id the importer did not write; they are never changed or deleted.

A real run prints one pair of lines per project, any `failure:` lines, and a total:

```
project "Marketing" (created): members added=2 existing=0; tasks imported=210 updated=0 duplicates=0 deleted=0 kept=0 moved=0 skipped=2 failed=0 rejected=0; comments ...; attachments ...; dependencies ...
project "Marketing": notes truncated=0 start_dropped=1 parent_changed=0 attachments_removed_in_asana=0 labels_left=0 completion_gate=0 cancelled_kept=0 slug_ignored=0 identifier_ignored=0
total: tasks imported=210 ...; comments ...; attachments ...; dependencies ...; projects_created=1 users_created=1
```

Each Asana item is counted once, in one of these:

| Counter      | The item was                                                                                                                                                                                                   |
| ------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `imported`   | Created. A task that moved here from another project counts as imported here.                                                                                                                                  |
| `updated`    | Changed by a sync.                                                                                                                                                                                             |
| `duplicates` | Already imported and left as it was.                                                                                                                                                                           |
| `deleted`    | Deleted by a sync.                                                                                                                                                                                             |
| `kept`       | Left in place: it could not be converted, its check failed, or its row was not written by this importer.                                                                                                       |
| `moved`      | Moved to another project in the mapping.                                                                                                                                                                       |
| `skipped`    | Deliberately not imported, for example an empty title, an empty comment, a file that is too large or of a type that is not allowed, or a subtask whose parent was not imported. Does not affect the exit code. |
| `failed`     | A write failed. The run exits with 1.                                                                                                                                                                          |

The notes line counts things that happened without being an error: `truncated` for cut text, `start_dropped` for a dropped start date, `parent_changed`, `attachments_removed_in_asana`, `labels_left` for a label of a tag that was renamed in Asana, `completion_gate`, `cancelled_kept`, and `slug_ignored` and `identifier_ignored` for a project that was reused.

#### Refusals

A sync refuses to run when it would lose too much, so that an Asana outage, a lost permission or a wrong project in the mapping cannot empty a workspace. It checks every project before it writes anything, on a dry-run too, prints a `refusing:` line and exits with 2:

```
refusing: project "Marketing" would delete 40 of 60 synced tasks
refusing: Asana returned no tasks for project "Marketing" but 60 are synced
```

The first appears when more than 10 tasks, and more than half of the project's synced tasks, are gone from Asana. The second appears when Asana lists no tasks at all for a project that has synced tasks and at least one would be deleted. Tasks that moved to another mapped project are not counted as lost. The usual causes are a token whose owner lost access to the project, or a `source` that now points at another project. There is no flag to override a refusal: fix the cause and run the dry-run again.

### Troubleshooting

- `ASANA_TOKEN must be set`: the variable did not reach the container. Pass it with `-e ASANA_TOKEN`, as in the example.
- `another importasana run holds the lock for this workspace`: another run is in progress, or a previous one is still ending. Wait for it.
- Every person is `unmatched`: Asana did not return email addresses for this token. List the people in `source_users`.
- `matches more than one Asana project`: give the gid in `source`, or rename one of the projects.
- `is not a member of workspace` or `is not in users and is not an existing account`: add the person under `users`, or add them to the workspace first.
- `has <n> attachments but storage is not configured (NF_S3_ENDPOINT)`: the container has no object storage configured. Use the `api` service of `compose.prod.yml`, or set the `NF_S3_*` variables.
- Exit code 1 with `failure:` lines: run the import again, without `-sync`. It creates only the missing rows, such as an attachment whose download failed.
- Exit code 2 with a fetch error: Asana kept failing after retries. The importer already waits when Asana asks it to slow down, so run it again later.
- The people created cannot sign in: compare `sign-in restriction:` with the real setting. If it said `open` on a restricted deployment, `NF_OAUTH_ALLOWED_DOMAINS` was missing; add the addresses to the sign-in allowlist.

## Discord presence

The Discord presence bot is off by default. Set `NF_DISCORD_BOT_TOKEN` in `.env` and start it with the profile:

```sh
docker compose -f compose.prod.yml --profile presence-discord up -d
```
