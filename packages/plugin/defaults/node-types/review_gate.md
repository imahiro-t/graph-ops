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
   template's headings, using each one exactly as the fetched template spells
   it -- same wording, same order, same number of headings. The template can
   come back in a language other than English depending on the language
   setting; whichever language it comes back in, its headings are that
   template's fixed structural marker, so do not translate, paraphrase,
   delete, reorder, rename, or add headings. Replace each `REPLACE_WITH_*`
   placeholder with real content, following the guide text that comes right
   after it; neither the placeholder name nor that guide text belongs in the
   saved review. Only the content you write under the headings follows the
   ticket's language.
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
   instead of leaving it with the template's "not applicable" answer. If a
   user or team override of the template words or orders its verdicts
   differently, match by role, not by position.
6. The template is the shape of the output itself: start the saved review at
   the template's first heading, and add no preamble or meta information that
   the template does not have.

## Artifacts

Save the verdict and the reasoning behind it -- findings, any issues raised,
and why the verdict is pass or fail -- as one `text` artifact via
`add-artifact`. Give it a name that describes its content.

## Notes

- Save that artifact even when there is nothing to flag. It is the only
  channel that makes the review result and its rationale previewable to others
  later, so it must never be skipped.
