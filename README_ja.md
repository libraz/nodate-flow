# nodate-flow

> **タスクを「管理する」ためのツールではなく、仕事の流れが勝手に前へ進んでいくシステム。**

LLM と MCP を後付けではなく実行層そのものとして組み込んだ、OSS のタスク基盤。タスクの状態はボタンで変えるのではなく、制約とイベントから導出されます。

ひとつのモノレポから 2 つのプロダクトを提供しています:

- **nodate-flow** — 制約・イベント・AI エージェント駆動のタスク管理。
- **nodate-time** — TimeTree の共有カレンダーの手軽さと Google カレンダーの権限管理を両立させたカレンダーアプリ。

> **名前について:** _"no date"_（締切に振り回されない働き方）と **野点（のだて）**（野外で気軽に茶を点てる所作）の掛け言葉です。

**ステータス:** alpha | **ライセンス:** [AGPL-3.0](./LICENSE) | **スタック:** Go · MySQL 9.6 · React 19 · OpenAPI 3.1

---

## nodate-flow

| 機能               | 詳細                                                                 |
| ------------------ | -------------------------------------------------------------------- |
| **ビュー**         | ボード、タイムライン、ガントチャート、ダッシュボード、カスタムレンズ |
| **制約エンジン**   | 依存関係、自動評価、状態の導出 — 手動でのステータス切り替え不要      |
| **AI**             | 優先度・状態の提案、スマート作成、埋め込みベースの重複検出           |
| **MCP**            | ビルトインサーバー — GitHub、Slack、外部ツールが同一ワークスペースに |
| **ページ**         | 軽量 Wiki / ドキュメント                                             |
| **通知**           | 受信トレイ + 週次ダイジェスト                                        |
| **Webhook**        | アウトバウンドのイベント配信                                         |
| **認証**           | パスワード (Argon2id) · OIDC · TOTP 2FA · リカバリーコード           |
| **マルチテナント** | ワークスペース + ロールベースのアクセス制御                          |

## nodate-time

| 機能           | 詳細                                                 |
| -------------- | ---------------------------------------------------- |
| **カレンダー** | 共有 & 個人、メンバーごとの表示権限制御              |
| **イベント**   | 参加者、招待、チェックリスト、コメント、添付ファイル |
| **同期**       | nodate-flow とのタスク双方向同期                     |
| **祝日**       | ロケール対応の祝日スケジューリング                   |

## Asana / Plane / Linear と何が違うのか

多くのツールはタスクを「クリックで状態を変える DB の行」として扱います。nodate-flow はタスクを**プロセス**として扱い、状態は制約とイベントログから導出します。LLM はビルトインの実行アクターとして動き、外部サービスは個別の連携コードではなく MCP 経由でワークスペースに統合されます。

一番近いのは [Plane](https://plane.so)（OSS、AGPL、自前ホスト前提）ですが、Plane は行+ステータスの枠組みに留まっています。

## クイックスタート

```sh
git clone <repo> && cd nodate-flow
make dev          # MySQL (Docker) + auth-api + flow-api + flow-web
make seed-flow    # デモ管理ユーザー + ワークスペース
```

```
make test-unit    # コンテナ不要の Go + TS テスト
make test         # Go + TS テスト
make gen          # codegen (sqlc + errors + SDK)
make help         # 全ターゲット一覧
```

開発用のオブジェクトストレージは以前のチェックアウトとは別のサービスに変わったため、古い `minio_data` ボリュームは引き継がれません。`make dev-reset` で初期データを入れ直せます。

## リポジトリ構成

```
apps/
  flow-api/       # Go バックエンド — タスク、カレンダー、AI、MCP、公開共有 (Huma + chi + sqlc)
  flow-web/       # React 19 フロントエンド — タスク、カレンダー、/share/cal、/invites/accept、/setup
  auth-api/       # Go — 認証・セッション (JWT, OIDC, TOTP)
  accounts-web/   # React 19 — ログイン / サインアップ / アカウント UI
  cli/            # CLI (バイナリ名: tnk)
packages/
  sdk/            # flow-api 用 TS SDK (OpenAPI から生成)
  ui/             # デザインシステム (4 テーマ)
  go-shared/      # 共有 Go パッケージ
  holidays/       # 祝日データ
  fixtures/       # テスト用フィクスチャ
errors/           # エラーコード (YAML → Go + TS codegen)
sql/              # テーブル / ビュー / sqlc クエリ
infra/            # Docker, Prometheus, Grafana, OTel, edge (nginx)
```

## デプロイ

`compose.prod.yml` は、MySQL、オブジェクトストレージ、auth-api、flow-api、flow-worker、そして 2 つの SPA を配信して 2 つの API へプロキシする nginx の edge コンテナを、1 台のホストでまとめて動かします。SPA は API のオリジンをバンドルに埋め込むため、イメージは pull せずこのリポジトリからビルドします。ポートを公開するのは edge コンテナ（80 と 443）だけです。データベース、オブジェクトストレージ、API のポート、メトリクスは compose ネットワークの内側にとどまります。

### 前提

Compose プラグイン付きの Docker と、ホストにインストールした certbot が必要です。

ホストを指す DNS 名が 4 つ必要です。4 つは同じ registrable domain 配下に置いてください。例えば `accounts.example.com`、`app.example.com`、`auth.example.com`、`api.example.com` です。リフレッシュ用 cookie は auth ホストが発行して app ホストから送られるため、別の registrable domain にまたがると、サードパーティ cookie を遮断するブラウザでサインインできなくなります。

### 設定

設定は `compose.prod.yml` と同じ場所の `.env` に書きます。次の値には既定値がなく、欠けていると Compose が名前を挙げて停止します。

- `NF_ACCOUNTS_HOST`、`NF_APP_HOST`、`NF_AUTH_HOST`、`NF_API_HOST`
- `NF_DB_ROOT_PASSWORD`、`NF_DB_PASSWORD`
- `NF_SECRET_KEY`
- `NF_S3_ACCESS_KEY`、`NF_S3_SECRET_KEY`
- `NF_FLOW_API_SIGNAL_TOKEN`
- `NF_SMTP_HOST`、`NF_SMTP_FROM`
- `NF_FLOW_GH_WEBHOOK_SECRET`、`NF_FLOW_SLACK_SIGNING_SECRET`、`NF_FLOW_GOOGLE_CHANNEL_TOKEN`

秘密の値はすべて `openssl rand -hex 32` で生成してください。flow-api は 3 つの Webhook 秘密がそろっていないと起動しないため、使わない連携の分にも乱数を入れます。誰も知らない乱数であれば、その連携に対するリクエストを偽造できる人はいません。

`NF_SMTP_HOST` が必須なのは、未設定だとマジックリンクの要求が成功を返すだけでメールが送られないためです。`NF_PUBLIC_SCHEME` の既定は `https` です。IdP の認証情報（`NF_AUTH_GOOGLE_CLIENT_ID` など）、`NF_REGISTRATION_OPEN`、その他の調整用変数は任意で、`.env` の値がそれを読むサービスへそのまま渡されます。

### 証明書

nginx は証明書のない server ブロックを読み込めないため、スタックを起動する前に証明書を取得しておく必要があります。80 番ポートを空けた状態で、4 つの名前をまとめた SAN 証明書を取得します。

```sh
mkdir -p acme
certbot certonly --standalone \
  -d accounts.example.com -d app.example.com -d auth.example.com -d api.example.com
```

edge は最初の `-d` の名前のディレクトリから証明書を読みます。別の名前を先頭にした場合は `NF_TLS_CERT_NAME` にその名前を指定してください。既定は `NF_ACCOUNTS_HOST` です。証明書ディレクトリは `NF_TLS_DIR`（既定 `/etc/letsencrypt`）、チャレンジ用ディレクトリは `NF_ACME_WEBROOT`（既定 `./acme`）で変えられます。

更新は、動作中の edge が webroot から応答するので、何も止める必要はありません。nginx は再読み込みを指示されるまで古い証明書を使い続けます。

```sh
certbot renew --webroot -w /path/to/acme \
  --deploy-hook 'docker compose -f /path/to/compose.prod.yml exec edge nginx -s reload'
```

### 初回起動と初期管理者

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

続けて edge を起動します。公開しないインスタンスなら、先に `.env` で `NF_REGISTRATION_OPEN=false` にしておいてください。

```sh
docker compose -f compose.prod.yml up -d --build
```

### IdP に登録するリダイレクト URI

各プロバイダーに次を登録します。`<auth>` は `https://` に `NF_AUTH_HOST` を続けたものです。

サインイン用:

- `<auth>/auth/oidc/google/callback`
- `<auth>/auth/oidc/github/callback`
- `<auth>/auth/oidc/microsoft/callback`

ワークスペース連携用:

- `<auth>/oauth/callback/github`
- `<auth>/oauth/callback/slack`
- `<auth>/oauth/callback/google`
- `<auth>/oauth/callback/discord`

### ホスト側のプロキシを使う場合

TLS を別の場所で終端するときは、`edge` サービスから `tls.conf.template` のマウントと 2 つの証明書マウントを外し、`80:80` と `443:443` の代わりに `127.0.0.1:8081:80` を publish します。ブラウザから見れば HTTPS のままなので `NF_PUBLIC_SCHEME=https` は変えず、`NF_TRUSTED_PROXY_HOPS=2` にして 2 段のプロキシを越えたクライアントアドレスを読ませます。ホスト側のプロキシは `X-Forwarded-For` を上書きせず追記し、レスポンスをバッファせず（ワークスペースのストリームは SSE です）、128 MB までのリクエストボディを通す必要があります。

### ホスト名やスキームを変える場合

ホスト名とスキームは SPA のバンドルに埋め込まれます。`.env` で変えたら edge を再ビルドしてください。

```sh
docker compose -f compose.prod.yml up -d --build edge
```

### スキーマ

`sql/schema.sql` が自動で適用されるのは、`mysql_data` ボリュームが空のときだけです。もう一度適用するには、アプリケーション側のサービスを止めて手で流します。このファイルは `DROP TABLE` から始まるので、データはすべて消えます。

```sh
docker compose -f compose.prod.yml stop edge auth-api api flow-worker
docker compose -f compose.prod.yml exec -T mysql \
  sh -c 'exec mysql -uroot -p"$MYSQL_ROOT_PASSWORD" "$MYSQL_DATABASE"' < sql/schema.sql
docker compose -f compose.prod.yml up -d
```

### バックアップ

永続データは `mysql_data` と `storage_data` の 2 つのボリュームにあります。添付ファイルは片方の行がもう片方のオブジェクトを指す関係なので、必ず揃えて取得してください。

```sh
docker compose -f compose.prod.yml exec -T mysql \
  sh -c 'exec mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" --single-transaction "$MYSQL_DATABASE"' \
  > mysql.sql
docker run --rm -v nodate-flow-prod_storage_data:/data:ro -v "$PWD/storage-backup":/backup \
  alpine sh -c 'apk add --no-cache rsync >/dev/null && rsync -aX /data/ /backup/'
```

ストレージゲートウェイはオブジェクトをファイルとして保存し、メタデータを拡張属性に持つため、コピーでも拡張属性を保つ必要があります（`rsync -aX`）。

### オブジェクトストレージ

同梱の S3 互換ゲートウェイは compose ネットワークの内側でだけ応答します。ブラウザが直接アクセスすることはなく、添付ファイルやアバターは flow-api の `/blobs` 中継を通ります。外部の S3 互換サービスを使うときは、`storage` サービスとその `depends_on` を削除し、`compose.prod.yml` の `auth-api` と `api` にある `NF_S3_ENDPOINT` と `NF_S3_USE_SSL` を書き換えてください（同梱ゲートウェイ向けの固定値になっています）。`NF_S3_ACCESS_KEY`、`NF_S3_SECRET_KEY`、`NF_S3_BUCKET` は `.env` から渡されます。

### Discord presence

Discord presence ボットは既定では起動しません。`.env` に `NF_DISCORD_BOT_TOKEN` を設定し、profile を指定して起動します。

```sh
docker compose -f compose.prod.yml --profile presence-discord up -d
```

## ライセンス

[AGPL-3.0](./LICENSE) — 自前ホスト、改変、再配布は自由です。ネットワーク越しに提供する場合はソースの公開が必要です（Plane や Vikunja と同じモデル）。

---

English version: [`README.md`](./README.md)
