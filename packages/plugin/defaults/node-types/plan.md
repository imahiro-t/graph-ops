Write the execution plan for this ticket. This node produces the plan only --
carrying it out is the job of the nodes that come after it.

## Procedure

1. Read the ticket's description and any artifacts earlier nodes have already
   saved.
2. Fetch the fixed template by running `get-plan-template`.
3. Work out the steps needed to satisfy what the ticket asks for, in the
   order they must happen.
4. Fill in the template's four headings -- 目的, 手順, 影響範囲, リスク・留意事項, in
   that order -- with real content in place of the REPLACE_WITH_* placeholders.
   Do not delete, reorder, rename, or add headings: those four words are a
   fixed structural marker (like the report template's section names) and
   stay in Japanese regardless of any language setting -- only the content
   you write under them follows the ticket's language.
5. Before saving, drop the template's leading `<!-- ... -->` comment block
   entirely. It is a meta-instruction to you, not one of the four headings
   referred to in step 4, and it is not part of the plan's content -- do not
   copy it into the saved artifact. The saved plan begins at the first
   heading, `# 目的`.

## Artifacts

Save the execution plan as one `text` artifact via `add-artifact`. Give it a
name that describes its content.

## Notes

- When the ticket's completion criterion demands exhaustive coverage -- "the
  whole repository", or a disposition for every item the description lists --
  enumerate that population mechanically (`go list ./...`, a count of the
  description's own headings) and build the plan against that enumeration
  rather than against your reading of the text.
- Verify a plan of that kind mechanically before you save it: every count in
  a summary must add up to the stated total, every category you assign must
  agree item by item with the markers the description puts on those items (or
  record why one differs), and every item must land in exactly one phase.
  Arithmetic that does not add up, a category set that silently departs from
  the description, and an item assigned to two phases are what a plan review
  finds first, and each one costs a full review iteration.
