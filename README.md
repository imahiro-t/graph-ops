# GraphOps

[English](#english) | [日本語](#japanese)

<a id="english"></a>
## English

A ticket management and execution platform for AI-driven development, built around execution graphs (DAG / parallel / loops). Install it as a Claude Code plugin and drive it entirely through slash commands and the Web UI -- no local build required.

### What GraphOps Does

- **Execution-graph-driven ticket management**: every ticket gets its own execution graph (a DAG of nodes such as `plan`, `review`, `implementation`, `review_gate`, `approval_gate`, `release`, ...). Nodes run sequentially or in parallel according to their dependencies, and a failing review can loop a node back for rework automatically.
- **Review gates and approval gates**: `review_gate` nodes judge pass/fail automatically against configurable criteria until the graph converges. `approval_gate` nodes always wait for an explicit decision from you -- approve with one click, or reject with a required reason.
- **Web UI visualization**: an interactive view of each ticket's execution graph (parallel nodes, loop-back edges, a ring around review/approval-gate nodes, live progress), a searchable/filterable/paginated ticket list, formatted previews for every artifact type (Gherkin, Markdown/text, HTML), and one-click approve/reject on pending approval gates.
- **Config-file driven and extensible**: what each review gate checks for, and how nodes/skills behave, can be customized per user or per team via config files, without touching the plugin itself. See the Web UI's Settings screen for what's editable.

### Installation & Getting Started

**Add and install the plugin**, from inside Claude Code:
```
/plugin marketplace add imahiro-t/graph-ops
/plugin install graph-ops@graph-ops
```
The first run fetches the plugin (and a matching `graph-engine` binary for your OS/architecture) into a per-user cache, so it needs network access and `git` on your `PATH` the first time; later runs reuse the cache.

**Updating to a new release**, from a regular terminal (not the `/plugin` menu):
```sh
claude plugin update graph-ops@graph-ops
```
Each release changes the plugin's fetch command (it pins the release tag), and Claude Code only runs a fetch command you have approved. The update prints the new command and asks you to approve it -- once approved, you're on the new release. No uninstall/reinstall is needed.
- Updating from the `/plugin` menu, or waiting for Claude Code's automatic background refresh, **cannot** approve a new command, so it leaves you on the old release (the `/plugin` Errors tab shows the new command waiting for approval).
- In a non-interactive shell (scripts, CI, provisioning), add `--yes` to accept the printed command: `claude plugin update graph-ops@graph-ops --yes`.

**First steps after installing:**
1. Run `/onboarding` once to choose the language the plugin should use for node names and everything it generates (plans, Gherkin specs/tests, implementation notes, review findings, reports). This persists as a `language:` setting (editable later by re-running `/onboarding`, or in config.yaml directly -- there is no Web UI control for it); for a language with a built-in language file (currently Japanese), the fixed workflow skeleton's node names and the default review gates' names switch automatically -- no per-gate translation to maintain yourself. Other languages still get the review gates translated individually, same as before. Re-run `/onboarding` any time to change the language.
2. Register your project: `cd` into your project's working directory and run `/ui` -- if the directory isn't registered yet, the Web UI opens straight to a "new project" dialog with that directory pre-filled.
3. Run `/create-ticket` and describe what you want; it talks the request through with you (scope, affected area, edge cases) before registering the ticket, rather than creating one from your first message verbatim.
4. Optionally, run `/refine-ticket` to pin down the ticket's completion criteria and the "why" before work starts; it rewrites the ticket's description around them.
5. Run `/process-ticket` to build and drive the ticket's execution graph: it decides the graph's shape from the ticket's content, dispatches a subagent per node (in parallel where the graph allows), and manages review convergence and artifact storage.
6. Use `/ui` any time to open the local Web UI for whichever project matches your current directory -- it starts the UI server automatically if it isn't already running.

**What each command does:**
- `/onboarding` -- first-time setup; asks which language the plugin should work in and persists the choice.
- `/create-ticket` -- creates a new ticket in the DB from a request that has been talked through first. Does not create an execution graph.
- `/refine-ticket` -- nails down completion criteria and the "why", then rewrites the ticket's description around them. Does not create an execution graph.
- `/process-ticket` -- decides the execution graph's shape, then drives it via parallel subagent execution, handling artifact storage and review convergence.
- `/ui` -- opens the local Web UI in your browser for the project matching your current directory, auto-starting the UI server if needed.

### Using the Web UI

- **Projects**: the top of the page has a project switcher for moving between registered projects, and a "new project" dialog (name, optional ID prefix, working-directory path) for registering another one.
- **Ticket list**: paginated, searchable, and filterable by status/assignee/priority. Each ticket shows its current status (`TODO` / `IN PROGRESS` / `IN REVIEW` / `IN RELEASE` / `DONE` / `CLOSED`), derived automatically from its execution graph. Tickets can be deleted (behind a confirmation dialog), withdrawn without doing the work, and reopened later.
- **Execution graph view**: open a ticket to see its graph -- parallel branches, loop-back edges for rework, a ring marking review/approval-gate nodes, and live progress as nodes complete. Every node's artifact (plan, Gherkin spec, implementation notes, review findings, report, ...) has a formatted preview.
- **Approving/rejecting**: a pending `approval_gate` can be approved directly from the ticket list with one click; rejecting it requires a free-text reason, entered from that node in the graph view. A rejection blocks the ticket until you act on it.
- **Launching Claude Code**: "Create", "Launch Claude", "Run", and "Refine" buttons each open an external, interactive terminal running `claude` for you to drive -- Cmd+Enter (Mac) / Ctrl+Enter (Windows) submits a prompt without leaving the keyboard.
- **Settings**: a Settings screen lets you edit node types, review-gate criteria, skill instructions, and the plan / review / report templates (under its "Templates" tab) -- scoped globally or per project. App-wide runtime settings (data storage location, MySQL connection, etc.) live under its "App Settings" tab; the defaults work out of the box, so most users won't need to touch this.
- **Theme**: toggle between light, dark, and system theme from the header.

### Notes for Normal Use

- The Web UI is a local, single-user tool with no login -- it only listens on your own machine unless you deliberately widen that in App Settings.
- Data (tickets, execution graphs, artifacts) is stored locally by default, with no setup required. Everything above works with the defaults; the App Settings tab exists for cases that need something different (a shared database, a custom port, etc.) and can be left alone otherwise. Artifacts land under `$HOME/.graph-ops/artifacts` (or wherever App Settings points), never in a repo-root `artifacts/` directory -- if one shows up there, it's stray output and safe to delete.

### Troubleshooting

- **Plugin update fails, or stays on the old release, saying the command changed since install**: expected on every release -- the new release's fetch command has to be approved. Run `claude plugin update graph-ops@graph-ops` from a regular terminal and approve it (see "Updating to a new release" above); no uninstall/reinstall needed.
- **"Launch Claude" / "Create" / "Run" / "Refine" doesn't seem to open a terminal**: these buttons open an external terminal by auto-detecting your environment (an active `tmux` session, or `open -a Terminal` on macOS). If neither applies -- notably **on Windows, which has no built-in launcher yet** (tracked as DFLT-00065) -- the launch fails and the Web UI shows a brief error toast that auto-clears after a few seconds, which can look like nothing happened. Set `terminalCommand` in `graph-config.json` (found in your project directory or `$HOME/.graph-ops/graph-config.json`; or set the `TERMINAL_COMMAND` env var) to a shell template using `{cwd}` and `{command}` placeholders. For example, on Windows with Windows Terminal:
  ```json
  { "terminalCommand": "wt.exe -d {cwd} cmd /k {command}" }
  ```
  or with `cmd.exe` directly:
  ```json
  { "terminalCommand": "cmd /c start cmd /k \"cd /d {cwd} && {command}\"" }
  ```
  The same setting also covers terminal emulators auto-detection doesn't try on macOS/Linux, such as iTerm, wezterm, kitty, or a VS Code integrated terminal.

---

<a id="japanese"></a>
## 日本語

実行グラフ（DAG／並列／ループ）を軸にした、AI駆動開発向けのチケット管理・実行基盤です。Claude Code のプラグインとしてインストールし、スラッシュコマンドと Web UI だけで操作します。手元でのビルドは不要です。

### GraphOpsでできること

- **実行グラフによるチケット管理**: チケットごとに専用の実行グラフ（`plan`／`review`／`implementation`／`review_gate`／`approval_gate`／`release` などのノードから成るDAG）を持ちます。ノードは依存関係に従って直列・並列に実行され、レビューに落ちたノードは自動的に差し戻し（ループ）で再実行されます。
- **レビューゲート・承認ゲート**: `review_gate` ノードは設定可能な観点に基づき、グラフが収束するまで自動的に合否判定します。`approval_gate` ノードは常に人間による明示的な判断を待ちます -- ワンクリックで承認するか、理由を添えて却下できます。
- **Web UIでの可視化**: 各チケットの実行グラフのインタラクティブな表示（並列ノード・差し戻しループ・レビュー/承認ゲートを示すリング・進捗のリアルタイム表示）、検索・フィルタ・ページングに対応したチケット一覧、Gherkin／Markdown・テキスト／HTMLいずれの成果物も整形済みで見られるプレビュー、承認待ちゲートのワンクリック承認/却下を提供します。
- **設定ファイル駆動で拡張可能**: 各レビューゲートの審査観点や、ノード・スキルの挙動は、プラグイン本体に手を入れずユーザーごと・チームごとにカスタマイズできます。何が編集できるかはWeb UIの設定画面から確認できます。

### インストールと使い始め方

**プラグインの追加とインストール**（Claude Code内で実行）:
```
/plugin marketplace add imahiro-t/graph-ops
/plugin install graph-ops@graph-ops
```
初回実行時に、プラグイン本体（とお使いのOS/アーキテクチャ向けの `graph-engine` バイナリ）がユーザーごとのキャッシュへ取得されるため、初回のみネットワークアクセスと `PATH` 上の `git` が必要です。以降の実行はこのキャッシュを再利用します。

**新しいバージョンへの更新**（`/plugin` メニューではなく、通常のターミナルで実行）:
```sh
claude plugin update graph-ops@graph-ops
```
リリースごとにプラグインの取得コマンドが変わり（リリースタグを固定しているため）、Claude Code はユーザーが承認した取得コマンドしか実行しません。更新時に新しいコマンドが表示されるので、承認すると新しいバージョンに切り替わります。アンインストール・再インストールは不要です。
- `/plugin` メニューからの更新や、Claude Code による自動のバックグラウンド更新では新しいコマンドを承認できないため、古いバージョンのまま据え置かれます（`/plugin` の Errors タブに承認待ちの新しいコマンドが表示されます）。
- 対話できないシェル（スクリプト・CI・プロビジョニングなど）では、`--yes` を付けて表示されたコマンドを承認します: `claude plugin update graph-ops@graph-ops --yes`

**インストール後、最初にやること:**
1. `/onboarding` を一度実行し、プラグインが使う言語（ノード名や、生成される成果物の言語）を選択します。選択内容は `language:` 設定として保存され（後から変更する場合は `/onboarding` を再実行するか、config.yaml を直接編集してください。Web UIの設定画面には言語の編集項目はありません）、言語ファイルが用意されている言語（現時点では日本語）であれば固定のワークフロー骨格ノード名・標準レビューゲート名が自動的にその言語になり、レビューゲートごとに個別翻訳を用意する必要はありません。言語ファイルが無い言語では、従来どおりレビューゲート名のみ個別に翻訳されます。いつでも再実行して変更できます。
2. プロジェクトを登録します。対象プロジェクトの作業ディレクトリで `cd` してから `/ui` を実行してください。未登録のディレクトリであれば、Web UIがそのディレクトリを初期値にした「新規プロジェクト作成」ダイアログを開きます。
3. `/create-ticket` を実行し、やりたいことを伝えます。最初のメッセージをそのままチケットにするのではなく、範囲・対象箇所・エッジケースなどを対話で詰めてから登録します。
4. 必要に応じて `/refine-ticket` を実行し、作業開始前に完了条件と「なぜやるか」を固めます。その内容に沿ってチケットの説明欄が書き換わります。
5. `/process-ticket` を実行し、チケットの実行グラフを構築・実行します。チケットの内容からグラフの形を決め、ノードごとにサブエージェントを（グラフが許す範囲で並列に）走らせ、レビューの収束制御と成果物の保存を管理します。
6. いつでも `/ui` を実行すれば、カレントディレクトリに対応するプロジェクトのローカルWeb UIが開きます（UIサーバーが未起動なら自動起動します）。

**各コマンドの役割:**
- `/onboarding` -- 初回セットアップ。プラグインが使う言語を確認し、設定として保存します。
- `/create-ticket` -- 対話で内容を詰めたうえで新規チケットをDBに登録します。実行グラフは作りません。
- `/refine-ticket` -- 完了条件と「なぜやるか」を詰め、その内容でチケットの説明欄を書き換えます。実行グラフは作りません。
- `/process-ticket` -- 実行グラフの形を決めたうえで、サブエージェントによる並列実行・成果物保存・レビュー収束制御まで一貫して行います。
- `/ui` -- カレントディレクトリに対応するプロジェクトのローカルWeb UIをブラウザで開きます（必要ならUIサーバーを自動起動します）。

### Web UIでできること

- **プロジェクト**: ページ上部のプロジェクト切替メニューで登録済みプロジェクトを切り替えられます。「新規プロジェクト作成」ダイアログ（名前・任意のIDプレフィックス・作業ディレクトリのパス）から別のプロジェクトを登録できます。
- **チケット一覧**: ページング、検索・フィルタ（ステータス／担当者／優先度）に対応します。各チケットのステータス（`TODO`／`IN PROGRESS`／`IN REVIEW`／`IN RELEASE`／`DONE`／`CLOSED`）は実行グラフから自動的に導出されます。チケットは削除（確認ダイアログあり）、対応せずに取り下げ、後からの再オープンができます。
- **実行グラフ表示**: チケットを開くとグラフが表示されます -- 並列分岐、差し戻しのループ辺、レビュー/承認ゲートを示すリング、ノード完了に応じたリアルタイムの進捗表示。各ノードの成果物（計画、Gherkin仕様、実装メモ、レビュー結果、レポートなど）は整形済みプレビューで確認できます。
- **承認・却下**: 承認待ちの `approval_gate` はチケット一覧からワンクリックで承認できます。却下には自由記述の理由が必須で、グラフ表示内の当該ノードから入力します。却下するとチケットは対応するまでブロックされます。
- **Claude Codeの起動**: 「作成」「Claude 起動」「実行」「リファイン」ボタンはいずれも外部の対話的ターミナルで `claude` を起動し、人間が操作する方式です。Cmd+Enter（Mac）/ Ctrl+Enter（Windows）でキーボードから離れずプロンプトを送信できます。
- **設定**: 設定画面からノードタイプ、レビューゲートの審査観点、スキルへの追加指示、レポートテンプレートを編集できます（全体設定／プロジェクト単位設定を切り替え可能）。データ保存先やMySQL接続などアプリ全体の実行時設定は「App Settings」タブにまとまっており、既定値のままで問題なく動作するため、通常はほとんど触る必要はありません。
- **テーマ**: ヘッダーからライト／ダーク／システム設定のテーマを切り替えられます。

### 通常利用にあたっての補足

- Web UIはログイン不要のローカル・単一ユーザー向けツールで、App Settingsで明示的に範囲を広げない限り自分のマシンからのみアクセスできます。
- チケット・実行グラフ・成果物などのデータは、特別な設定なしに既定でローカルに保存されます。ここまでの内容はすべて既定設定のまま動作します。App Settingsタブは共有データベースやポート変更など特別な要件がある場合のためのもので、それ以外では触らなくて構いません。成果物の保存先は既定で `$HOME/.graph-ops/artifacts`（またはApp Settingsで指定した場所）であり、リポジトリ直下の `artifacts/` ディレクトリではありません。もし直下に生成されていた場合は混入した不要なファイルなので削除して構いません。

### トラブルシューティング

- **プラグインの更新が「インストール時とコマンドが変わった」として失敗する／古いバージョンのままになる**: リリースのたびに起きる想定内の挙動で、新しいリリースの取得コマンドを承認する必要があります。通常のターミナルで `claude plugin update graph-ops@graph-ops` を実行して承認してください（上記「新しいバージョンへの更新」を参照）。アンインストール・再インストールは不要です。
- **「Claude 起動」「作成」「実行」「リファイン」ボタンを押してもターミナルが開かないように見える**: これらのボタンは、実行環境を自動判定して外部ターミナルを開きます（`tmux` セッション内で実行中の場合はそのウィンドウ、macOSでは `open -a Terminal`）。どちらにも該当しない場合 -- 特に **Windowsには現時点で組み込みの起動方法が用意されていません**（DFLT-00065 で追跡中）-- 起動に失敗し、Web UIには数秒で自動的に消えるエラートーストが表示されるだけなので、何も起きていないように見えることがあります。`graph-config.json`（プロジェクトディレクトリ、または `$HOME/.graph-ops/graph-config.json` に置く。もしくは環境変数 `TERMINAL_COMMAND`）に `terminalCommand` を設定してください。値は `{cwd}` と `{command}` のプレースホルダーを使ったシェルコマンドのテンプレートです。例えば、Windows Terminal を使う場合:
  ```json
  { "terminalCommand": "wt.exe -d {cwd} cmd /k {command}" }
  ```
  `cmd.exe` を直接使う場合:
  ```json
  { "terminalCommand": "cmd /c start cmd /k \"cd /d {cwd} && {command}\"" }
  ```
  同じ設定で、macOS/Linuxで自動判定の対象外のターミナル（iTerm、wezterm、kitty、VS Codeの統合ターミナルなど）を使いたい場合にも対応できます。

---

## License

MIT License. See [LICENSE](./LICENSE).

Copyright (c) 2026 Takashi Imahiro

## ライセンス

MIT ライセンスです。全文は [LICENSE](./LICENSE) を参照してください。

Copyright (c) 2026 Takashi Imahiro
