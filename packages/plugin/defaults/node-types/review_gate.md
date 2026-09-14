Judge the work of the node this gate depends on against the gate's own review
criteria and record a pass/fail verdict. A `review_gate` carries criteria of
its own; a single-artifact checkpoint without them is a `review`.

## Procedure

1. Fetch the criteria with `get-review-criteria "<nodeId>"`. For a
   `review_gate` node this returns the gate-specific criteria plus the
   iteration convergence criteria.
2. Fetch the fixed template by running `get-review-template` (the same
   template used by the `review` node type).
3. Read the artifacts of the node under review, together with the ticket's
   description and the artifacts of earlier nodes.
4. Judge pass or fail against both sets of criteria, and fill in the
   template's four headings -- 判定, 指摘事項, 判断理由, 条件付き承認の場合の条件, in
   that order -- with real content in place of the REPLACE_WITH_* placeholders.
   Do not delete, reorder, rename, or add headings.
5. Under 判定, write exactly one of 承認 / 条件付き承認 / 差し戻し and nothing
   else. These three words are a fixed structural marker (like the report
   template's PASS/FAIL badge): stay in Japanese, unparaphrased, regardless
   of any language setting, so pass/fail can be read off at a glance without
   parsing prose. Map your judgment onto them -- 差し戻し for fail; 承認 or
   条件付き承認 for pass, depending on whether you are attaching conditions
   (and if 条件付き承認, fill in those conditions under the last heading
   rather than leaving it as "該当なし").

## Artifacts

Save the verdict and the reasoning behind it -- findings, any issues raised,
and why the verdict is pass or fail -- as one `text` artifact via
`add-artifact`. Give it a name that describes its content.

## Notes

- Save that artifact even when there is nothing to flag. It is the only
  channel that makes the review result and its rationale previewable to others
  later, so it must never be skipped.
