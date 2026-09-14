Produce this ticket's report by filling in the fixed report template. Every
report keeps the same structure and style run over run, so that reports stay
diffable and comparable across tickets.

## Procedure

1. Fetch the fixed template by running `get-report-template`.
2. Read this ticket's implementation, test, and review outcomes from the
   artifacts earlier nodes saved.
3. Fill in the template's `header` (ticket id, title, date), `summary`,
   `results`, and `footer` sections from those outcomes.
4. Keep the template's structural markers intact -- `data-report-template`,
   `data-report-version`, and the `data-report-section` values `header`,
   `summary`, `results`, and `footer`.

## Artifacts

Save the completed report as one `html` artifact via `add-artifact`. Give it a
name that describes its content.

## Notes

- Do not invent your own HTML structure and do not drop the template's
  markers. A `report` node's `html` artifact is validated against the fixed
  template's required markers, so a free-formed report is rejected at save
  time.
