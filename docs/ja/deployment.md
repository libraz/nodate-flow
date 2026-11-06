# デプロイ

`compose.prod.yml` は、MySQL、オブジェクトストレージ、auth-api、flow-api、flow-worker、そして 2 つの SPA を配信して 2 つの API へプロキシする nginx の edge コンテナを、1 台のホストでまとめて動かします。SPA は API のオリジンをバンドルに埋め込むため、イメージは pull せずこのリポジトリからビルドします。各サービスの関係は [アーキテクチャ](architecture.md) にまとめています。

ホスト名は 1 つ、証明書も 1 つで、公開するものは他にありません。入口は edge コンテナだけで、公開するポートも edge（80 と 443）だけです。edge は TLS を終端し、パスで振り分けます。`/` は flow-web、`/accounts/` は accounts-web、`/api/` は flow-api、`/auth-api/` は auth-api です。2 つの API のプレフィックスは、プロキシの前に取り除かれます。データベース、オブジェクトストレージ、API のポート、メトリクスは compose ネットワークの内側にとどまります。

## 前提

Compose プラグイン付きの Docker と、ホストにインストールした certbot が必要です。

ホストを指す DNS 名が 1 つ必要です。例えば `flow.example.com` です。すべてを同じオリジンから配信するので、リフレッシュ用 cookie はファーストパーティになります。

## 設定

設定は `compose.prod.yml` と同じ場所の `.env` に書きます。次の値には既定値がなく、欠けていると Compose が名前を挙げて停止します。

- `NF_PUBLIC_HOST`
- `NF_DB_ROOT_PASSWORD`、`NF_DB_PASSWORD`
- `NF_SECRET_KEY`
- `NF_S3_ACCESS_KEY`、`NF_S3_SECRET_KEY`
- `NF_FLOW_API_SIGNAL_TOKEN`
- `NF_FLOW_GH_WEBHOOK_SECRET`、`NF_FLOW_SLACK_SIGNING_SECRET`、`NF_FLOW_GOOGLE_CHANNEL_TOKEN`

秘密の値はすべて `openssl rand -hex 32` で生成してください。flow-api は 3 つの Webhook 秘密がそろっていないと起動しないため、使わない連携の分にも乱数を入れます。誰も知らない乱数であれば、その連携に対するリクエストを偽造できる人はいません。

SMTP は任意です（`NF_SMTP_HOST`、`NF_SMTP_FROM` と、それに付随する認証情報）。ホストを設定しない場合、マジックリンクでのサインインは提供されず、招待や通知のメールも送られません。`NF_PUBLIC_SCHEME` の既定は `https` です。公開 URL はこの 2 つから導出されます。`NF_AUTH_PUBLIC_URL` は `https://<host>/auth-api`、`NF_FLOW_PUBLIC_URL` は `https://<host>/api`、accounts-web は `https://<host>/accounts` です。IdP の認証情報（`NF_AUTH_GOOGLE_CLIENT_ID` など）、`NF_REGISTRATION_OPEN`、その他の調整用変数は任意で、`.env` の値がそれを読むサービスへそのまま渡されます。

## OIDC のみのサインイン

パスワードアカウントも SMTP も使わず、IdP 経由のサインインだけを許可するには、`.env` に次を書きます。

```sh
NF_PASSWORD_LOGIN_ENABLED=false
NF_AUTH_GOOGLE_CLIENT_ID=...
NF_AUTH_GOOGLE_CLIENT_SECRET=...
NF_OAUTH_ALLOWED_DOMAINS=example.com        # または NF_OAUTH_ALLOWED_EMAILS=ceo@example.com
NF_REGISTRATION_OPEN=true
```

`NF_PASSWORD_LOGIN_ENABLED=false` にすると、auth-api は `POST /auth/register`、`POST /auth/login`、`POST /me/password` と `/me/totp` 配下の TOTP 管理ルートを登録せず、Web アプリにはプロバイダーのボタンだけが表示されます。二要素目は IdP 側のものを使います。切り替え前に TOTP を登録していたアカウントは、チャレンジ（`POST /auth/login/totp`）とリカバリーコードをそのまま使えますが、パスワードログインを再び有効にするまで TOTP を管理できません。このとき `NF_REGISTRATION_OPEN=true` が開くのは OIDC の自動プロビジョニングだけで、許可リストに載ったドメインとアドレスに限られます。許可リストがないと、プロバイダー上で検証済みのアドレスなら誰でもアカウントを作れるため、必ず設定してください。`NF_SMTP_HOST` を未設定のままにすれば、マジックリンクでのサインインも無効のままです。

## 証明書

nginx は証明書のない server ブロックを読み込めないため、スタックを起動する前に証明書を取得しておく必要があります。80 番ポートを空けた状態で取得します。

```sh
mkdir -p acme
certbot certonly --standalone -d flow.example.com
```

edge は `/etc/letsencrypt/live/<NF_TLS_CERT_NAME>/` から証明書を読みます。既定は `NF_PUBLIC_HOST` で、certbot が `-d` の名前で作るディレクトリと一致します。証明書ディレクトリは `NF_TLS_DIR`（既定 `/etc/letsencrypt`）、チャレンジ用ディレクトリは `NF_ACME_WEBROOT`（既定 `./acme`）で変えられます。

更新は、動作中の edge が webroot から応答するので、何も止める必要はありません。nginx は再読み込みを指示されるまで古い証明書を使い続けます。

```sh
certbot renew --webroot -w /path/to/acme \
  --deploy-hook 'docker compose -f /path/to/compose.prod.yml exec edge nginx -s reload'
```

## 初回起動と初期管理者

管理者がいない間は外から到達できないよう、edge 以外を先に起動します。

```sh
docker compose -f compose.prod.yml up -d --build mysql storage auth-api api flow-worker
```

次に、最初のユーザーを登録して管理者にします。コマンドは compose ネットワーク上のコンテナから実行します。ネットワーク名はプロジェクト名 `nodate-flow-prod` に `_default` を付けたものです。`POST /auth/register` が返すアクセストークンを、`POST /admin/setup` に Bearer トークンとして渡します。`/admin/setup` は 1 回目だけ `{"ok":true}` を返し、以後は 409 になります。

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

OIDC のみのインスタンスには、登録に使うパスワードがありません。許可リストに自分のアドレスだけを設定したうえで、スタック全体を起動します。プロバイダー経由で一度サインインしてアカウントを作り、そのアカウントに管理者権限を付与します。このコマンドは繰り返し実行しても安全です。

```sh
docker compose -f compose.prod.yml up -d --build
docker compose -f compose.prod.yml exec auth-api /grant-admin -email admin@example.com
```

そのアドレスのユーザーがまだいない場合、`grant-admin` は 0 以外の終了コードで失敗します。

パスワード方式のインスタンスでは、続けて edge を起動します。公開しないインスタンスなら、先に `.env` で `NF_REGISTRATION_OPEN=false` にしておいてください。

```sh
docker compose -f compose.prod.yml up -d --build
```

## IdP に登録するリダイレクト URI

各プロバイダーに次を登録します。`<auth>` は `https://` に `NF_PUBLIC_HOST`、`/auth-api` を続けたものです。

サインイン用:

- `<auth>/auth/oidc/google/callback`
- `<auth>/auth/oidc/github/callback`
- `<auth>/auth/oidc/microsoft/callback`

ワークスペース連携用:

- `<auth>/oauth/callback/github`
- `<auth>/oauth/callback/slack`
- `<auth>/oauth/callback/google`
- `<auth>/oauth/callback/discord`

## ホスト側のプロキシを使う場合

TLS を別の場所で終端するときは、`edge` サービスから `tls.conf.template` のマウントと 2 つの証明書マウントを外し、`80:80` と `443:443` の代わりに `127.0.0.1:8081:80` を publish します。ブラウザから見れば HTTPS のままなので `NF_PUBLIC_SCHEME=https` は変えず、`NF_TRUSTED_PROXY_HOPS=2` にして 2 段のプロキシを越えたクライアントアドレスを読ませます。ホスト側のプロキシは `X-Forwarded-For` を上書きせず追記し、レスポンスをバッファせず（ワークスペースのストリームは SSE です）、128 MB までのリクエストボディを通す必要があります。

## ホスト名やスキームを変える場合

ホスト名とスキームは SPA のバンドルに埋め込まれます。`.env` で変えたら edge を再ビルドしてください。

```sh
docker compose -f compose.prod.yml up -d --build edge
```

## スキーマ

`sql/schema.sql` が自動で適用されるのは、`mysql_data` ボリュームが空のときだけです。もう一度適用するには、アプリケーション側のサービスを止めて手で流します。このファイルは `DROP TABLE` から始まるので、データはすべて消えます。

```sh
docker compose -f compose.prod.yml stop edge auth-api api flow-worker
docker compose -f compose.prod.yml exec -T mysql \
  sh -c 'exec mysql -uroot -p"$MYSQL_ROOT_PASSWORD" "$MYSQL_DATABASE"' < sql/schema.sql
docker compose -f compose.prod.yml up -d
```

`sql/schema.sql` には 3 つの層がすべて入っています。core、nodate-time と共有する time 層、flow 層です。同じデータベースで nodate-time も動いている場合、このファイルを流し直すと nodate-time のデータも消えます。データを残すには、その場で upgrade します。両方のアプリケーションを止め、`sql/core/upgrades/*.sql` を番号順に流し、続けて `sql/time/upgrades/` の time 層の upgrade を流します。最後に `bash sql/core/conformance/run.sh --dsn 'user:pass@host:port/db' --mode schema` で結果を確かめます。手順は `sql/core/PROTOCOL.md` にあります。

## バックアップ

永続データは `mysql_data` と `storage_data` の 2 つのボリュームにあります。添付ファイルは片方の行がもう片方のオブジェクトを指す関係なので、必ず揃えて取得してください。

```sh
docker compose -f compose.prod.yml exec -T mysql \
  sh -c 'exec mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" --single-transaction "$MYSQL_DATABASE"' \
  > mysql.sql
docker run --rm -v nodate-flow-prod_storage_data:/data:ro -v "$PWD/storage-backup":/backup \
  alpine sh -c 'apk add --no-cache rsync >/dev/null && rsync -aX /data/ /backup/'
```

ストレージゲートウェイはオブジェクトをファイルとして保存し、メタデータを拡張属性に持つため、コピーでも拡張属性を保つ必要があります（`rsync -aX`）。

## オブジェクトストレージ

同梱の S3 互換ゲートウェイは compose ネットワークの内側でだけ応答します。ブラウザが直接アクセスすることはなく、添付ファイルやアバターは flow-api の `/blobs` 中継を通ります。外部の S3 互換サービスを使うときは、`storage` サービスとその `depends_on` を削除し、`compose.prod.yml` の `auth-api` と `api` にある `NF_S3_ENDPOINT` と `NF_S3_USE_SSL` を書き換えてください（同梱ゲートウェイ向けの固定値になっています）。`NF_S3_ACCESS_KEY`、`NF_S3_SECRET_KEY`、`NF_S3_BUCKET` は `.env` から渡されます。

## Discord presence

Discord presence ボットは既定では起動しません。`.env` に `NF_DISCORD_BOT_TOKEN` を設定し、profile を指定して起動します。

```sh
docker compose -f compose.prod.yml --profile presence-discord up -d
```
