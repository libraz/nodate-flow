# デプロイ

`compose.prod.yml` は、MySQL、オブジェクトストレージ、auth-api、flow-api、flow-worker、そして 2 つの SPA を配信して 2 つの API へプロキシする nginx の edge コンテナを、1 台のホストでまとめて動かします。SPA は API のオリジンをバンドルに埋め込むため、イメージは pull せずこのリポジトリからビルドします。各サービスの関係は [アーキテクチャ](architecture.md) にまとめています。

ホスト名は 1 つ、証明書も 1 つで、公開するものは他にありません。入口は edge コンテナだけで、公開するポートも edge（80 と 443）だけです。edge は TLS を終端し、パスで振り分けます。`/` は flow-web、`/accounts/` は accounts-web、`/api/` は flow-api、`/auth-api/` は auth-api です。2 つの API のプレフィックスは、プロキシの前に取り除かれます。データベース、オブジェクトストレージ、API のポート、メトリクスは compose ネットワークの内側にとどまります。

## 前提

Compose プラグイン付きの Docker と、ホストにインストールした certbot が必要です。

同梱のデータベースは MySQL Community 26.10.0 です。

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

`NF_APP_NAME` は、2 つの Web アプリに表示される製品名、タブのタイトル、マジックリンクのメールの件名、認証アプリに表示される発行者名、製品 API リファレンスのタイトルを設定します。既定は `nodate-flow` で、64 文字までです。`:` と制御文字は使えません。`NF_APP_LOGO_URL` は、名前を含んだロゴ画像です。サインイン画面、サイドバー、公開共有ページで名前の文字の代わりに表示されます。`NF_APP_ICON_URL` は正方形のシンボル画像です。組み込みのマークの代わりに名前の横に表示され、favicon にも使われます。アイコンだけを設定した場合は名前も表示されます。ロゴだけを設定した場合、畳んだサイドバーでは組み込みのマークが表示されます。以前 `NF_APP_LOGO_URL` に正方形の画像を設定していた場合は、`NF_APP_ICON_URL` に移してください。`NF_APP_LOGO_DARK_URL` と `NF_APP_ICON_DARK_URL` はダークテーマ用の画像で、対応する基本の変数が必要です。値は `https://` の URL か、32 KiB までの `data:image/png`、`svg+xml`、`webp`、`jpeg`、`gif` の URI で、内部に空白を含まない印字可能な ASCII で書きます。国際化ドメイン名は punycode で指定してください。送信メールのヘッダーに表示されるのは `https://` の画像だけなので、メールにロゴやアイコンを出したい場合は URL を指定してください。名前と 4 つの画像変数は SPA のバンドルに組み込まれるため、変更したら [ホスト名やスキームを変える場合](#ホスト名やスキームを変える場合) の手順で edge を再ビルドしてください。送信メールの差出人アドレスは、別に `NF_SMTP_FROM` で設定します。名前を変えたあとに 2 要素認証を再登録した人は、認証アプリに項目が 2 つ並ぶことがあります。既存の項目のコードはそのまま使えます。

`NF_FLOW_EMBED_OPENAI_KEY` は、サーバー全体でタスクの埋め込みを有効にします。本番では、キーの実値がコンテナの環境変数に入らないファイル方式を使います。

```sh
# .env
NF_FLOW_EMBED_OPENAI_KEY_FILE=/run/secrets/embedding-openai-key
```

`compose.embedding-secret.yml` などの Compose オーバーライドを作り、ホスト上のキーのファイルを API コンテナ内へ読み取り専用でマウントします。

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

キーのファイルはシークレット管理ツールを使って作成します。所有者を API コンテナのユーザーである数値の UID と GID `65532` にし、権限を `0400` にします。親ディレクトリは root だけがアクセスできるようにしてください。2 つの Compose ファイルを指定して API を起動します。

```sh
docker compose -f compose.prod.yml -f compose.embedding-secret.yml up -d --build --force-recreate api
```

サービスを起動または再作成する Compose コマンドには毎回 `-f compose.embedding-secret.yml` を追加します。後に続く初回起動のコマンドでも同じです。キーの設定元はどちらか一方にしてください。ファイルには空でないキーが必要で、末尾の改行は 1 つまで許容します。キーは起動時に読み込むため、ファイルを更新したら `api` コンテナを再作成します。ワークスペースは既定でサーバー共通の埋め込み設定を引き継ぎ、AI プロバイダー設定から無効にできます。このキーは各ワークスペースに登録する LLM のキーとは別です。`NF_FLOW_EMBED_OPENAI_KEY` で直接渡すと、Docker のコンテナ環境情報からキーの実値を読めます。

既定のモデルは `text-embedding-3-small` です。`NF_FLOW_EMBED_MODEL` と `NF_FLOW_EMBED_BASE_URL` で、モデルと OpenAI 互換の接続先を指定できます。本番では接続先に HTTPS を使ってください。

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

`sql/schema.sql` には 3 つの層がすべて入っています。core、nodate-time と共有する time 層、flow 層です。同じデータベースで nodate-time も動いている場合、このファイルを流し直すと nodate-time のデータも消えます。

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

## Asana からの移行

`importasana` は、Asana のプロジェクトをワークスペースにコピーします。移行の途中は、Asana の変更に追従させることもできます。コマンドラインツールで、flow-api のイメージに `/importasana` として入っています。データベースとオブジェクトストレージの設定が済んでいる `api` コンテナの中で実行します。

### 取り込まれるもの

- プロジェクト。指定した人がメンバーとして加わり、実行者（importer）がプロジェクトの lead になります。
- タスクとサブタスク。親子関係、担当者、フォロワー（ウォッチャーとして）、開始日・期日、完了状態も入ります。マイルストーンと承認は通常のタスクになります。
- コメント。作成者と元の投稿時刻も入ります。タスクの作成時刻も元のままです。
- タグはワークスペースのラベルに、セクションはプロジェクト限定のラベルになります。
- 取り込んだタスク同士の「blocks」依存関係。
- Asana に保存されている添付ファイル。実体はオブジェクトストレージにコピーされるため、`NF_S3_ENDPOINT` が必要です。同梱の compose ファイルは設定済みです。

取り込まれないもの:

- カスタムフィールド、ポートフォリオ、ゴール、プロジェクトのステータス更新、承認の結果、いいね、担当変更などの活動履歴。dry-run で件数だけ報告します。
- 他のホストにあるファイル（Google Drive、Dropbox、Box、OneDrive、Vimeo）。タスクの説明の末尾にリンクとして並びます。
- 逆方向の反映。Asana には何も書き戻しません。

元の値のままにならないもの:

- `completed_at` は、flow で完了にした時刻です。移行前に完了していたタスクでは取り込んだ時刻になるため、移行前に終えた作業のリードタイム集計は意味を持ちません。
- 500 文字を超えるタイトルは切り詰め、全文を説明の先頭に置きます。説明とコメントは 50000 文字で切ります。
- リッチテキストは Markdown に変換します。メンションされた人は通常のメンションではなく `@名前` の文字列になるため、通知は飛びません。
- 期日より後の開始日は捨てます。

### 始める前に

次の 3 つが必要です。

1. Asana の個人用アクセストークン。Asana の開発者コンソールで作成します。トークンは作成した本人として動き、その人に見えるものしか見えません。コピーしたいプロジェクトすべてに参加しているアカウントで作ってください。環境変数 `ASANA_TOKEN` で渡します。フラグでは渡せないので、プロセス一覧にもシェルの履歴にも残りません。
2. 取り込み先のワークスペース。マッピングの `importer` に書くアカウントは、そのワークスペースの有効な owner か admin である必要があります。
3. 次に説明するマッピングファイル。

`NF_OAUTH_ALLOWED_DOMAINS` には、auth-api が使っているのと同じ値を渡します。[アカウントとサインイン](#アカウントとサインイン)を参照してください。

### マッピングファイル

```yaml
workspace: acme # flow のワークスペース slug（必須。存在すること）
source_workspace: "1200000000" # Asana ワークスペースの gid か一意な名前。トークンから 1 つしか見えなければ省略可
importer: ops@example.com # 取り込みを実行するアカウント。作成するプロジェクトの lead になる
users: # 作成するアカウント
  - { email: hanako@example.com, name: Hanako Yamada }
source_users: # Asana のユーザー gid -> メール。メールでの自動照合より優先
  "1201234567890": hanako@example.com
projects:
  - source: "1209876543210" # Asana プロジェクトの gid か、1 つだけに一致する名前
    name: Marketing # 省略すると Asana の名前
    slug: marketing # 省略すると asana-<プロジェクトの gid>。プロジェクトを作るときだけ使う
    identifier: MKT # 任意。英数字 5 文字まで。プロジェクトを作るときだけ使う
    members:
      - { email: hanako@example.com, role: editor } # lead / editor / commenter / viewer
    sections: # Asana のセクション名 -> ラベル名
      "Untitled section": "" # 空にするとラベルを付けない。書かなければセクション名のまま
```

- gid は引用符で囲んだ文字列で書きます。
- `source` の名前は 1 つのプロジェクトにだけ一致する必要があります。gid にも一致する場合や、複数のプロジェクトに一致する場合は拒否されます。
- `importer` は `members` に書きません。
- 同じ名前のプロジェクトが既にあり、importer がその lead であれば、それを再利用し、`slug` と `identifier` は無視します。そうでなければプロジェクトを作ります。
- 未知のキー、空のファイル、重複は、何も書く前に拒否されます。エラーは値のパス付きで出ます。例: `projects[0].members[1].role: "owner" is not one of lead, editor, commenter, viewer`

### アカウントとサインイン

Asana の各ユーザーは、次の順でアカウントに対応づけます。`source_users` にその gid のエントリがあればそれ、なければ Asana が返すメールアドレスです。対応するアカウントがそのプロジェクトのメンバーでなければ、そのプロジェクトでは未対応として扱います。

- 未対応の担当者とフォロワーは外します。
- 未対応の人が書いたコメントは importer の名義で書き、本文の先頭に `**<Asana の表示名>:**` を付けます。
- dry-run は、Asana のユーザー全員について、どう対応したか（`explicit` / `email` / `not-member` / `unmatched`）と、関わるタスクとコメントの数を出します。`unmatched` は `source_users` か `users` で直し、dry-run をやり直してください。

`users` に書いたアカウントは、パスワードなしで作られ、ワークスペースに加わります。本人が最初に ID プロバイダ経由でサインインしたときに、そのアカウントを引き継ぎます。

サインインが制限されている場合は、そのメールアドレスがサインインを許可されている必要があるため、importer はメモ `created by importasana` を付けてサインイン許可リストに追加します。サインインが開いている場合は何も追加しません。許可リストに行を足すと制限が有効になってしまうためです。どちらかは、許可リストのテーブルと `NF_OAUTH_ALLOWED_DOMAINS` から判断します。この変数は `api` サービスではなく auth-api に設定されているので、取り込みのコマンドにも同じ値を渡してください（下の例では渡しています）。未設定だと、ドメインで制限しているデプロイが開いているものとして報告され、作られた人はサインインできません。dry-run は `sign-in restriction: open` か `sign-in restriction: domains=<n> allowlist=<n>` を出すので、本番の実行の前に期待どおりか確かめてください。

### 手順

`compose.prod.yml` があるディレクトリで、トークンとドメインをシェルに設定して実行します。マッピングは標準入力から読みます。

```sh
export ASANA_TOKEN=...                    # 個人用アクセストークン
export NF_OAUTH_ALLOWED_DOMAINS=example.com   # auth-api と同じ値。なければ空

imp() {
  docker compose -f compose.prod.yml exec -T \
    -e ASANA_TOKEN -e NF_OAUTH_ALLOWED_DOMAINS \
    api /importasana -mapping - "$@" < mapping.yaml
}

imp -dry-run          # 1. 計画を読む。何も書かない
imp                   # 2. 取り込む
imp -sync -dry-run    # 3. 移行中: sync で何が変わるかを読む
imp -sync             # 4. Asana に追従する
```

1. **dry-run。** Asana とデータベースからすべて読み、計画を出力し、何も書きません。報告された問題を直し、`validation errors: 0` になるまで繰り返します。
2. **取り込み。** 足りないものを作ります。`-sync` なしで 2 回目を実行すると、まだ足りないもの（最初に失敗したコメントや添付など）だけを作ります。取り込み済みのタスクは変更も削除もしないので、繰り返しても安全です。
3. **sync。** `-sync` を付けると Asana が優先されます。以前に取り込んだタスクとコメントについて、次のようになります。
   - タイトル、説明、日付、完了状態は上書きされます。flow で編集した分は失われます。
   - 担当者とウォッチャーは、Asana のユーザーに対応する人についてだけ比べます。それ以外の人、reviewer、approver には触れません。
   - Asana のタグとセクション由来のラベルを比べます。flow で付けたラベルは残りますが、Asana のタグと同じ名前なら、そのタグとして扱われます。
   - 取り込み済みタスク同士の「blocks」依存は Asana に合わせます。flow で引いた依存は消えます。
   - コメントの本文は上書きされます。作成者は変わりません。
   - Asana から消えたタスクとコメントは、アプリで削除したときと同じ経路で flow でも削除され、イベントログから追えます。
   - 完了は境界をまたぐときだけ追従します。Asana で完了になれば完了にし、Asana で未完了に戻れば、完了済みのタスクを waiting にします。cancelled のタスクはそのままにし、`review_required` ポリシーに拒否された完了は飛ばします。どちらも notes 行に数えます。

   sync は毎回、先に dry-run で確認してください。sync しないもの: 親の付け替え、添付の削除、プロジェクトメンバーの削除、プロジェクトの改名、ラベルの改名と削除。notes 行に件数が出ます。

sync の前に、importer は対象のタスクを Asana に 1 件ずつ問い合わせて確かめます。権限エラー、サーバエラー、タイムアウトになったタスクは、削除せずに残します。

マッピング内の別のプロジェクトに移ったタスクは、元のプロジェクトで削除され、新しいプロジェクトで作られます。`moved` として数えます。flow で足したものは失われます。コメント、ラベル、reviewer と approver、waiting や review の状態、自分で引いた依存、タスク番号です。

取り込みはイベントを書きますが、通知、メール、webhook、エージェントの実行は起こしません。importer は別プロセスで動くためです。ただし 1 つだけ動くものがあります。5 分ごとに走る auto_action ルールは、取り込んだタスクを通常のタスクと同じに扱うので、期限切れのタスクが取り込みの数分後にエスカレーションされることがあります。避けたい場合は、移行のあいだ、ワークスペースの AI 設定で自動アクションを止めてください。すでに開いているブラウザは、取り込みの書き込みで一覧を取り直すことがありますが、通知は出ません。

実行できるのは、ワークスペースごとに 1 つだけです。2 つ目は、すぐに終了コード 2 で終わります。

### 終了コード

| コード | 意味                                                                                                                                                                                                                                           |
| ------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 0      | 成功。                                                                                                                                                                                                                                         |
| 1      | 実行は始まったが、1 件以上の書き込みが失敗した。それ以外の書き込みは残る。何が失敗したかは `failure:` 行に出る。もう一度実行すると、欠けた分を埋める。                                                                                         |
| 2      | 何も書いていない。`ASANA_TOKEN` がない、マッピングが不正、または Asana やデータベースと合わない、Asana への要求が失敗し続けた、sync が拒否された、別の実行がロックを持っている、のいずれか。理由は標準エラー出力の `error:` で始まる行に出る。 |

### レポートの読み方

dry-run は次の順に出力します。

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

`-sync` を付けると、プロジェクトごとに、最後の 3 行の前に項目の種類ごとの行が加わります。

```
project "Marketing": sync plan tasks create=3 update=5 unchanged=204 delete=1 moved=0 kept=0 synced=205 orphaned=0 repeated=0
```

`create`、`update`、`unchanged`、`delete`、`moved`、`kept` は、実際の実行で imported、updated、duplicates、deleted、moved、kept に数えられる件数です。`synced` は、そのプロジェクトにある、取り込み済みで有効なタスクの数です。`orphaned` は、この importer が書いたのではない取り込み元 ID を持つ行の数で、変更も削除もしません。

実際の実行は、プロジェクトごとに 2 行、`failure:` 行（あれば）、最後に合計を出力します。

```
project "Marketing" (created): members added=2 existing=0; tasks imported=210 updated=0 duplicates=0 deleted=0 kept=0 moved=0 skipped=2 failed=0 rejected=0; comments ...; attachments ...; dependencies ...
project "Marketing": notes truncated=0 start_dropped=1 parent_changed=0 attachments_removed_in_asana=0 labels_left=0 completion_gate=0 cancelled_kept=0 slug_ignored=0 identifier_ignored=0
total: tasks imported=210 ...; comments ...; attachments ...; dependencies ...; projects_created=1 users_created=1
```

Asana の各項目は、次のどれか 1 つに 1 回だけ数えます。

| カウンタ     | 項目の扱い                                                                                                                                                               |
| ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `imported`   | 作成した。別のプロジェクトから移ってきたタスクは、こちらでは imported に数える。                                                                                         |
| `updated`    | sync で変更した。                                                                                                                                                        |
| `duplicates` | 取り込み済みで、変更しなかった。                                                                                                                                         |
| `deleted`    | sync で削除した。                                                                                                                                                        |
| `kept`       | そのまま残した。変換できなかった、確認に失敗した、またはこの importer が書いた行ではなかった。                                                                           |
| `moved`      | マッピング内の別のプロジェクトに移した。                                                                                                                                 |
| `skipped`    | 意図して取り込まなかった。例: タイトルが空、コメントが空、ファイルが大きすぎる、許可されない種類のファイル、親が取り込まれていないサブタスク。終了コードには影響しない。 |
| `failed`     | 書き込みに失敗した。終了コードは 1 になる。                                                                                                                              |

notes 行は、エラーではないが起きたことを数えます。`truncated` は切り詰めたテキスト、`start_dropped` は捨てた開始日、`parent_changed`、`attachments_removed_in_asana`、`labels_left`（Asana で改名されたタグのラベル）、`completion_gate`、`cancelled_kept`、再利用したプロジェクトでの `slug_ignored` と `identifier_ignored` です。

#### 拒否

sync は、失うものが多すぎるときは実行を拒否します。Asana の障害、権限の喪失、マッピングの誤ったプロジェクトで、ワークスペースが空になるのを防ぐためです。何かを書く前に、dry-run でも、すべてのプロジェクトを確かめ、`refusing:` 行を出して終了コード 2 で終わります。

```
refusing: project "Marketing" would delete 40 of 60 synced tasks
refusing: Asana returned no tasks for project "Marketing" but 60 are synced
```

1 つ目は、Asana から消えたタスクが 10 件を超え、かつ、そのプロジェクトの同期済みタスクの半数を超えるときに出ます。2 つ目は、同期済みのタスクがあるプロジェクトについて Asana が 1 件もタスクを返さず、1 件以上が削除されることになるときに出ます。マッピング内の別のプロジェクトに移ったタスクは、失われたものに数えません。よくある原因は、トークンの持ち主がプロジェクトへのアクセスを失った場合と、`source` が別のプロジェクトを指すようになった場合です。拒否を上書きするフラグはありません。原因を直して、dry-run からやり直してください。

### トラブルシューティング

- `ASANA_TOKEN must be set`: 変数がコンテナに届いていません。例のとおり `-e ASANA_TOKEN` で渡してください。
- `another importasana run holds the lock for this workspace`: 別の実行が進行中か、前の実行がまだ終わっていません。終わるまで待ってください。
- 全員が `unmatched`: このトークンでは Asana がメールアドレスを返していません。`source_users` に人を書いてください。
- `matches more than one Asana project`: `source` に gid を書くか、プロジェクトのどちらかを改名してください。
- `is not a member of workspace` または `is not in users and is not an existing account`: その人を `users` に書くか、先にワークスペースに加えてください。
- `has <n> attachments but storage is not configured (NF_S3_ENDPOINT)`: コンテナにオブジェクトストレージが設定されていません。`compose.prod.yml` の `api` サービスで実行するか、`NF_S3_*` を設定してください。
- 終了コード 1 と `failure:` 行: `-sync` なしでもう一度取り込みます。ダウンロードに失敗した添付のような、足りない行だけが作られます。
- 終了コード 2 と取得エラー: 再試行の後も Asana への要求が失敗し続けました。Asana から速度を落とすよう求められたときは importer が自分で待つので、時間を置いてやり直してください。
- 作られた人がサインインできない: `sign-in restriction:` を実際の設定と見比べてください。制限しているデプロイで `open` と出ていた場合は `NF_OAUTH_ALLOWED_DOMAINS` が欠けていたので、サインイン許可リストにアドレスを追加してください。

## Discord presence

Discord presence ボットは既定では起動しません。`.env` に `NF_DISCORD_BOT_TOKEN` を設定し、profile を指定して起動します。

```sh
docker compose -f compose.prod.yml --profile presence-discord up -d
```
