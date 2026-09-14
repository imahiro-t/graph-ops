Write the execution plan for this ticket. This node produces the plan only --
carrying it out is the job of the nodes that come after it.

## Procedure

1. Read the ticket's description and any artifacts earlier nodes have already
   saved.
2. Fetch the fixed template by running `get-plan-template`.
3. Work out the steps needed to satisfy what the ticket asks for, in the
   order they must happen.
4. Fill in the template's headings, using each one exactly as the fetched
   template spells it -- same wording, same order, same number of headings.
   The template can come back in a language other than English depending on
   the language setting; whichever language it comes back in, its headings
   are that template's fixed structural marker (like the report template's
   section names), so do not translate, paraphrase, delete, reorder, rename,
   or add headings.
5. Replace each `REPLACE_WITH_*` placeholder with real content, following the
   guide text that comes right after it. Neither the placeholder name nor that
   guide text belongs in the saved plan. Only the content you write under the
   headings follows the ticket's language.
6. The template is the shape of the output itself: start the saved plan at
   the template's first heading, and add no preamble or meta information that
   the template does not have.

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
