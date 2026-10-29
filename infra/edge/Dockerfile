# syntax=docker/dockerfile:1.7
# Edge image: serves both SPAs and fronts the two APIs with nginx.
# The build context is the repository root: the lockfile, the shared
# TypeScript configuration and the workspace packages live there.

ARG BUN_VERSION=1.4.4

FROM oven/bun:${BUN_VERSION}-alpine AS build
WORKDIR /src

COPY package.json bun.lock bunfig.toml tsconfig.base.json tsconfig.json ./
COPY apps/accounts-web/package.json ./apps/accounts-web/
COPY apps/cli/package.json ./apps/cli/
COPY apps/flow-web/package.json ./apps/flow-web/
COPY packages/holidays/package.json ./packages/holidays/
COPY packages/i18n-shared/package.json ./packages/i18n-shared/
COPY packages/sdk/package.json ./packages/sdk/
COPY packages/ui/package.json ./packages/ui/
# The root postinstall installs git hooks, which needs git and a work tree.
RUN bun install --frozen-lockfile --ignore-scripts

# tests/ comes along: the flow-web `tsc -b` references tsconfig.tests.json.
COPY packages/ui ./packages/ui
COPY packages/sdk ./packages/sdk
COPY packages/holidays ./packages/holidays
COPY packages/i18n-shared ./packages/i18n-shared
COPY apps/accounts-web ./apps/accounts-web
COPY apps/flow-web ./apps/flow-web

# Origins are compiled into the bundles by Vite, so moving a deployment to
# other hostnames means rebuilding. Compose always passes all of them.
ARG VITE_API_BASE_URL=""
ARG VITE_AUTH_API_BASE_URL=""
ARG VITE_ACCOUNTS_WEB_URL=""
ARG VITE_PUBLIC_BASE_URL=""
ARG VITE_FLOW_WEB_URL=""
ARG VITE_NF_ALLOWED_REDIRECT_ORIGINS=""

# Each app is built with only the variables it reads.
RUN cd apps/accounts-web \
 && VITE_AUTH_API_BASE_URL="$VITE_AUTH_API_BASE_URL" \
    VITE_FLOW_WEB_URL="$VITE_FLOW_WEB_URL" \
    VITE_NF_ALLOWED_REDIRECT_ORIGINS="$VITE_NF_ALLOWED_REDIRECT_ORIGINS" \
    bun run build
RUN cd apps/flow-web \
 && VITE_API_BASE_URL="$VITE_API_BASE_URL" \
    VITE_AUTH_API_BASE_URL="$VITE_AUTH_API_BASE_URL" \
    VITE_ACCOUNTS_WEB_URL="$VITE_ACCOUNTS_WEB_URL" \
    VITE_PUBLIC_BASE_URL="$VITE_PUBLIC_BASE_URL" \
    bun run build

# The CSP names the hash of each index.html's inline script, so it is derived
# from the build rather than copied into the config where it would go stale.
COPY infra/edge/nginx/csp-script-hash.mjs /tmp/csp-script-hash.mjs
RUN mkdir /csp \
 && bun /tmp/csp-script-hash.mjs apps/accounts-web/dist/index.html > /csp/csp-accounts.conf \
 && bun /tmp/csp-script-hash.mjs apps/flow-web/dist/index.html > /csp/csp-flow.conf

FROM nginx:1.30-alpine

# The nginx image runs envsubst over /etc/nginx/templates/**/*.template at
# startup and writes the results under /etc/nginx/conf.d/. Only NF_-prefixed
# variables are substituted, so nginx's own $host and friends survive.
ENV NGINX_ENVSUBST_FILTER=^NF_

# The default is plain HTTP, for a host where something else terminates TLS.
# compose.prod.yml mounts tls.conf.template over default.conf.template.
COPY infra/edge/nginx/templates/default.conf.template /etc/nginx/templates/default.conf.template
COPY infra/edge/nginx/conf.d/00-maps.conf /etc/nginx/templates/00-maps.conf.template

# Snippets that name origins or hostnames are templates; the rest are copied
# as they are. Both end up in conf.d/snippets/.
COPY infra/edge/nginx/snippets/security-headers-accounts.conf /etc/nginx/templates/snippets/security-headers-accounts.conf.template
COPY infra/edge/nginx/snippets/security-headers-flow.conf /etc/nginx/templates/snippets/security-headers-flow.conf.template
COPY infra/edge/nginx/snippets/proxy-common.conf infra/edge/nginx/snippets/api.conf infra/edge/nginx/snippets/auth.conf /etc/nginx/conf.d/snippets/
# spa.conf includes its app's security headers from every location, which
# add_header inheritance forces; one copy per app names the right file.
COPY infra/edge/nginx/snippets/spa.conf /tmp/spa.conf
RUN for app in accounts flow; do \
      sed "s/@APP@/$app/g" /tmp/spa.conf > /etc/nginx/conf.d/snippets/spa-$app.conf; \
    done \
 && rm /tmp/spa.conf
COPY --from=build /csp/ /etc/nginx/conf.d/snippets/

COPY --from=build /src/apps/accounts-web/dist /usr/share/nginx/accounts
COPY --from=build /src/apps/flow-web/dist /usr/share/nginx/flow
EXPOSE 80 443
