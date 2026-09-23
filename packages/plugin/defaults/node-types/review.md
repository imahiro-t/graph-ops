Judge the work of the node this review depends on and record a pass/fail
verdict. A `review` is a single-artifact checkpoint; a multi-perspective gate
carrying criteria of its own is a `review_gate`.

## Procedure

1. Fetch the criteria with `get-review-criteria "<nodeId>"`. For a `review`
   node this returns the iteration convergence criteria.
   Judge by the review round, limit and tier it reports -- see "Review rounds
   and convergence tiers" below. If the round is 2 or more, fetch your
   previous review result and the changes made since it before going on.
2. Fetch the fixed template by running `get-review-template` (the same
   template used by the `review_gate` node type).
3. Read the artifacts of the node under review, together with the ticket's
   description and the artifacts of earlier nodes.
4. Judge pass or fail against those criteria, and fill in the template's
   headings, using each one exactly as the fetched template spells it -- same
   wording, same order, same number of headings. The template can differ
   from the plugin default when a user or team has overridden it (for
   example, in another language); whatever it comes back as, its headings
   are that template's fixed structural marker, so do not translate, paraphrase, delete, reorder,
   rename, or add headings. Replace each `REPLACE_WITH_*` placeholder with
   real content, following the guide text that comes right after it; neither
   the placeholder name nor that guide text belongs in the saved review. Only
   the content you write under the headings follows the ticket's language.
5. Under the first heading (the verdict section), write exactly one of the
   three verdict words that the template's verdict placeholder lists, spelled
   exactly as the template spells it, and nothing else -- no translation, no
   paraphrasing, no extra wording in that section. Like the report template's
   PASS/FAIL badge, these words are a fixed structural marker, so pass/fail
   can be read off at a glance without parsing prose. The template lists them
   by role in this order: unconditional approval, conditional approval,
   send-back. Map your judgment onto those roles -- the send-back word for
   fail; the unconditional-approval or conditional-approval word for pass,
   depending on whether you are attaching conditions. If you choose the
   conditional-approval word, write those conditions under the last heading
   instead of leaving it with the template's "not applicable" answer; carry-over
   items go there too (see below). If a
   user or team override of the template words or orders its verdicts
   differently, match by role, not by position.
6. The template is the shape of the output itself: start the saved review at
   the template's first heading, and add no preamble or meta information that
   the template does not have.

## Review rounds and convergence tiers

`get-review-criteria` tells you where this review stands in its loop: a line
`Review round: <r> / <limit> — tier: <Normal|Important|Final>`, the definition
of that tier, and the rules no tier relaxes. The round counts the first review
(round 1) and comes from the loop target's `iteration_count`; the limit is the
workflow-wide review iteration limit (3, 4 or 5) the graph was built with, plus
any rounds granted with `grant-iterations`. A review that fails in the last
round blocks the ticket instead of sending the work back, so judge by the tier
you are given:

- **Normal**: fail for anything that needs fixing, minor points included.
- **Important**: fail only for correctness bugs, unmet completion criteria,
  security problems, or regressions. Record minor and stylistic points as
  carry-over items instead of failing for them.
- **Final**: fail only for critical bugs, security vulnerabilities, data
  corruption, or unmet completion criteria. Every round granted past the
  original limit is Final.

Never relaxed, at any tier:

- A bug, regression, or security problem newly introduced by the changes made
  since the previous round is judged as strictly as at the Normal tier.
- A serious issue flagged in a previous round that is still not fixed fails
  the review.

From round 2 on, before judging, fetch both of these:

1. Your own previous review result: the latest review artifact on this node
   in `get-ticket`'s output.
2. The changes made since that review. For code, `git log` / `git diff` over
   the commits made after that review artifact was written. For a document
   (a plan, a Gherkin spec, a report), the difference between the loop
   target's latest artifact and the one you reviewed last time.

Check the changes against the never-relaxed rules, and check that every serious
issue from your previous review is fixed.

Carry-over items -- points you record without failing the review, at the
Important or Final tier -- go under the template's last heading. The verdict is
then the unconditional-approval word, not the conditional-approval word with
required code changes as its conditions: nothing sends the work back to the
implementation node from an approval, so such conditions would never be acted
on.

## Artifacts

Save the verdict and the reasoning behind it -- findings, any issues raised,
and why the verdict is pass or fail -- as one `text` artifact via
`add-artifact`. Give it a name that describes its content.

## Notes

- Save that artifact even when there is nothing to flag. It is the only
  channel that makes the review result and its rationale previewable to others
  later, so it must never be skipped.
