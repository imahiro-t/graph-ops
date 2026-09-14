<!--
FIXED STRUCTURE -- do not remove, reorder, or rename these headings, and do
not add headings beyond the four below. Shared by the "review" and
"review_gate" node types (see
packages/core-go/internal/config/defaults/node-types/review.md and
review_gate.md, resolvable via `graph-engine get-node-type-context review` /
`review_gate`). Fill in each REPLACE_WITH_* placeholder with real content.

The Verdict section's value must be exactly one of the three words below --
Approved / Conditionally Approved / Rejected -- and nothing else: no
translation, no paraphrasing, no additional wording in that section. Like the
report template's PASS/FAIL badge, these three words (and the four heading
words themselves: Verdict, Findings, Rationale, Conditions (if Conditionally
Approved)) are this file's own fixed structural marker, so a pass/fail can be
read off at a glance. This file is the English default; when `language: ja`
resolves, packages/plugin/defaults/locales/ja/review/template.md is used in
its place instead (see packages/core-go/internal/config/locale.go --
LoadLocaleTemplate/ResolveReviewTemplate), with the same four headings and
three verdict words translated to Japanese. Whichever file resolves, its
headings and verdict words are fixed for that language and stay as they are
regardless of the ticket's own language; only the content written under the
other headings follows the ticket's language.
-->

# Verdict

REPLACE_WITH_VERDICT -- exactly one of: Approved / Conditionally Approved /
Rejected.

# Findings

REPLACE_WITH_FINDINGS -- list the issues/concerns found during review. If none,
say so explicitly ("None").

# Rationale

REPLACE_WITH_RATIONALE -- why the verdict above was reached. Make the connection
to the review criteria (the `get-review-criteria` content) explicit.

# Conditions (if Conditionally Approved)

REPLACE_WITH_CONDITIONS -- the conditions that must be met when the verdict is
"Conditionally Approved". If not applicable, say so explicitly ("N/A").
