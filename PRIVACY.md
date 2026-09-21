# Privacy Policy

[English](#english) | [日本語](#日本語)

## English

This policy covers the GraphOps Claude Code plugin and its `graph-engine` binary.

### Data GraphOps collects

None. GraphOps has no telemetry, analytics, or crash reporting, and it does not send your data to the author or to any third party.

### Where your data is stored

Everything GraphOps creates stays on your machine, or in a database you choose:

- Tickets, execution graphs, and artifacts: the SQLite file `~/.graph-ops/graph.db` by default. If you configure MySQL instead, they are stored in the database you specify.
- Working files for artifacts: `~/.graph-ops/artifacts`.
- Settings: `~/.graph-ops/config.json` and `~/.graph-ops/config.yaml`. These are the only files GraphOps reads settings from (environment variables aside); nothing is read out of the directory you start it in.
- The downloaded `graph-engine` binary: `~/.cache/graph-ops/engine/` (or the directory set in `GRAPH_OPS_ENGINE_DIR`).

### Network access

- **Downloading the binary**: the first time the plugin runs after an install or update, it downloads the `graph-engine` binary and `checksums.txt` for that version from this repository's GitHub Releases over HTTPS. GitHub's [Terms of Service](https://docs.github.com/en/site-policy/github-terms/github-terms-of-service) and [Privacy Statement](https://docs.github.com/en/site-policy/privacy-policies/github-general-privacy-statement) apply to that request.
- **Installing the plugin**: Claude Code fetches the plugin from GitHub with `git`.
- **Web UI**: the local UI server listens on `127.0.0.1` by default, and the UI itself loads no resources from external sites unless you explicitly ask it to. It is reachable from other machines only if you change `host` in your settings (or `GRAPH_HOST`). A remote image inside a Markdown artifact is not fetched just because the artifact is displayed: the viewer shows the image's host and requests it only after you click to load it. An HTML artifact you preview in the UI can still load whatever external resources the artifact itself references.
- **MySQL**: if you configure MySQL, GraphOps connects to the server you specify.

GraphOps makes no other network requests.

### Claude Code and Anthropic

GraphOps runs inside Claude Code. Your conversations with Claude, including ticket content and artifacts that Claude reads or writes, are handled by Claude Code and Anthropic under [Anthropic's Privacy Policy](https://www.anthropic.com/legal/privacy). GraphOps does not send anything to Anthropic on its own.

### Deleting your data

Delete `~/.graph-ops/` to remove tickets, artifacts, and settings, and `~/.cache/graph-ops/` to remove downloaded binaries. If you use MySQL, delete the data from that database. Uninstalling the plugin does not delete these.

### Changes and contact

Changes to this policy are published in this file in the repository. For questions, open an issue at <https://github.com/imahiro-t/graph-ops/issues>.

## 日本語

このポリシーは、Claude Code プラグイン GraphOps と、その `graph-engine` バイナリを対象とします。

### 収集するデータ

ありません。GraphOps にはテレメトリ、アクセス解析、クラッシュレポートの機能はなく、データを作者や第三者に送信することもありません。

### データの保存場所

GraphOps が作るデータは、すべてお使いのマシン上か、ご自身で指定したデータベースに保存されます。

- チケット、実行グラフ、成果物: 既定では SQLite ファイル `~/.graph-ops/graph.db`。MySQL を設定した場合は、指定したデータベース。
- 成果物用の作業ファイル: `~/.graph-ops/artifacts`
- 設定: `~/.graph-ops/config.json`、`~/.graph-ops/config.yaml`（環境変数を除けば、GraphOps が設定を読み込むのはこの 2 ファイルだけです。起動したディレクトリからは何も読み込みません）
- ダウンロードした `graph-engine` バイナリ: `~/.cache/graph-ops/engine/`（`GRAPH_OPS_ENGINE_DIR` を設定した場合はそのディレクトリ）

### ネットワーク通信

- **バイナリのダウンロード**: インストールまたは更新後にプラグインが初めて動くとき、そのバージョンの `graph-engine` バイナリと `checksums.txt` を、このリポジトリの GitHub Releases から HTTPS でダウンロードします。この通信には GitHub の[利用規約](https://docs.github.com/ja/site-policy/github-terms/github-terms-of-service)と[プライバシーステートメント](https://docs.github.com/ja/site-policy/privacy-policies/github-general-privacy-statement)が適用されます。
- **プラグインのインストール**: Claude Code が `git` で GitHub からプラグインを取得します。
- **Web UI**: ローカルの UI サーバーは既定で `127.0.0.1` だけで待ち受け、UI 自体は、利用者が明示的に操作しない限り外部サイトからリソースを読み込みません。設定の `host`（または `GRAPH_HOST`）を変えない限り、他のマシンからは接続できません。Markdown 成果物の中の外部画像は、表示しただけでは読み込まれません。画像のホスト名が表示され、読み込みをクリックしたときに初めて取得されます。ただし、UI でプレビューする HTML 成果物は、その成果物自体が参照している外部リソースを読み込むことがあります。
- **MySQL**: MySQL を設定した場合は、指定したサーバーに接続します。

これ以外のネットワーク通信は行いません。

### Claude Code と Anthropic

GraphOps は Claude Code の中で動きます。Claude との会話（Claude が読み書きするチケットの内容や成果物を含む）は、Claude Code と Anthropic が [Anthropic のプライバシーポリシー](https://www.anthropic.com/legal/privacy)に従って扱います。GraphOps が独自に Anthropic へ送信することはありません。

### データの削除

チケット、成果物、設定を消すには `~/.graph-ops/` を、ダウンロードしたバイナリを消すには `~/.cache/graph-ops/` を削除してください。MySQL を使っている場合は、そのデータベースからデータを削除してください。プラグインをアンインストールしても、これらは削除されません。

### 変更と問い合わせ

このポリシーを変更した場合は、リポジトリ内のこのファイルで公開します。質問は <https://github.com/imahiro-t/graph-ops/issues> に issue を作成してください。
