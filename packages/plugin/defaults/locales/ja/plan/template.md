<!--
LOCALE OVERRIDE -- this file is used in place of
packages/plugin/defaults/plan/template.md (the English default) only when
`language: ja` resolves (see packages/core-go/internal/config/locale.go --
LoadLocaleTemplate/ResolvePlanTemplate). It is paired with that English
default file: when the English default's steps/structure change (beyond the
four fixed headings themselves), update this file to match.

FIXED STRUCTURE -- do not remove, reorder, or rename these headings, and do
not add headings beyond the four below. Fill in each REPLACE_WITH_*
placeholder with real content. These four heading words (目的, 手順, 影響範囲,
リスク・留意事項) are a fixed structural marker -- like the report template's
section names -- and stay in Japanese regardless of any language setting;
only the content written under them follows the ticket's language. See
packages/core-go/internal/config/defaults/node-types/plan.md (the "plan" node
type's default instructions, resolvable via
`graph-engine get-node-type-context plan`) for how this gets filled in.
-->

# 目的

REPLACE_WITH_PURPOSE -- このチケットで何を達成するのか、完了条件との対応関係を簡潔に書く。

# 手順

REPLACE_WITH_STEPS -- 実行順に並んだ手順。必要に応じて番号付きの小見出しやチェックリストにする。

# 影響範囲

REPLACE_WITH_SCOPE -- 新規作成・変更・削除するファイルやコンポーネントの一覧。スコープ外と判断した
事項があれば、その理由とあわせて明記する。

# リスク・留意事項

REPLACE_WITH_RISKS -- 実装時に注意すべき点、既知のリスクやトレードオフ。該当がなければ「特になし」と
明記する。
