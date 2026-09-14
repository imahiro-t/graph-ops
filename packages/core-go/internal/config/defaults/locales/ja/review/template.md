<!--
LOCALE OVERRIDE -- this file is used in place of
packages/plugin/defaults/review/template.md (the English default) only when
`language: ja` resolves (see packages/core-go/internal/config/locale.go --
LoadLocaleTemplate/ResolveReviewTemplate). It is paired with that English
default file: when the English default's structure/wording change (beyond
the four fixed headings and three verdict words themselves), update this
file to match.

FIXED STRUCTURE -- do not remove, reorder, or rename these headings, and do
not add headings beyond the four below. Shared by the "review" and
"review_gate" node types (see
packages/core-go/internal/config/defaults/node-types/review.md and
review_gate.md, resolvable via `graph-engine get-node-type-context review` /
`review_gate`). Fill in each REPLACE_WITH_* placeholder with real content.

The 判定 section's value must be exactly one of the three words below --
承認 / 条件付き承認 / 差し戻し -- and nothing else: no translation, no
paraphrasing, no additional wording in that section. Like the report
template's PASS/FAIL badge, these three words (and the four heading words
themselves: 判定, 指摘事項, 判断理由, 条件付き承認の場合の条件) are a fixed
structural marker and stay in Japanese regardless of any language setting, so
a pass/fail can be read off at a glance; only the content written under the
other headings follows the ticket's language.
-->

# 判定

REPLACE_WITH_VERDICT -- 承認 / 条件付き承認 / 差し戻し のいずれか一語のみを記入する。

# 指摘事項

REPLACE_WITH_FINDINGS -- 確認して分かった問題点・懸念点を列挙する。指摘がなければ「特になし」と
明記する。

# 判断理由

REPLACE_WITH_RATIONALE -- 上記の判定に至った理由。判定基準（`get-review-criteria` の内容）との
対応関係を明確にする。

# 条件付き承認の場合の条件

REPLACE_WITH_CONDITIONS -- 判定が「条件付き承認」の場合に満たすべき条件。該当しない場合は
「該当なし」と明記する。
