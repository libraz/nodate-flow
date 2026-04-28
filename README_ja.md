# nodate-flow

> **タスクを「管理する」ためのツールではなく、仕事の流れが勝手に前へ進んでいくシステム。**

LLM と MCP を後付けではなく実行層そのものとして組み込んだ、OSS のタスク基盤。タスクの状態はボタンで変えるのではなく、制約とイベントから導出されます。

ひとつのモノレポから 2 つのプロダクトを提供しています:

- **nodate-flow** — 制約・イベント・AI エージェント駆動のタスク管理。
- **nodate-time** — TimeTree の共有カレンダーの手軽さと Google カレンダーの権限管理を両立させたカレンダーアプリ。

> **名前について:** *"no date"*（締切に振り回されない働き方）と **野点（のだて）**（野外で気軽に茶を点てる所作）の掛け言葉です。

**ステータス:** alpha | **ライセンス:** [AGPL-3.0](./LICENSE) | **スタック:** Go · MySQL 9.6 · React 19 · OpenAPI 3.1

---

## nodate-flow

| 機能 | 詳細 |
|---|---|
| **ビュー** | ボード、タイムライン、ガントチャート、ダッシュボード、カスタムレンズ |
| **制約エンジン** | 依存関係、自動評価、状態の導出 — 手動でのステータス切り替え不要 |
| **AI** | 優先度・状態の提案、スマート作成、埋め込みベースの重複検出 |
| **MCP** | ビルトインサーバー — GitHub、Slack、外部ツールが同一ワークスペースに |
| **ページ** | 軽量 Wiki / ドキュメント |
| **通知** | 受信トレイ + 週次ダイジェスト |
| **Webhook** | アウトバウンドのイベント配信 |
| **認証** | パスワード (Argon2id) · OIDC · TOTP 2FA · リカバリーコード |
| **マルチテナント** | ワークスペース + ロールベースのアクセス制御 |

## nodate-time

| 機能 | 詳細 |
|---|---|
| **カレンダー** | 共有 & 個人、メンバーごとの表示権限制御 |
| **イベント** | 参加者、招待、チェックリスト、コメント、添付ファイル |
| **同期** | nodate-flow とのタスク双方向同期 |
| **祝日** | ロケール対応の祝日スケジューリング |

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
make dev-time     # カレンダースタックを起動
make test         # Go + TS テスト
make gen          # codegen (sqlc + errors + SDK)
make help         # 全ターゲット一覧
```

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
infra/            # Docker, Prometheus, Grafana, OTel, Caddy
```

## ライセンス

[AGPL-3.0](./LICENSE) — 自前ホスト、改変、再配布は自由です。ネットワーク越しに提供する場合はソースの公開が必要です（Plane や Vikunja と同じモデル）。

---

English version: [`README.md`](./README.md)
