Canonical default content for the GraphOps plugin.

- `node-types/<type>.md` -- default agent instructions for each built-in node
  type (`plan`, `investigation`, `gherkin_spec`, `implementation`, `review`,
  `review_gate`, `gherkin_test`, `documentation`, `approval_gate`, `release`;
  the `report` type's default lives in `deliverable.md` -- see the mapping
  note in `packages/core-go/internal/config/extensions.go`).
  These files, along with `skills/*/SKILL.md`, follow a fixed authoring
  format (heading structure, voice, length) -- see
  `packages/plugin/docs/authoring-format.md` before editing one.
- `report/template.html` -- the fixed report template `report` nodes fill in.
- `workflow.yaml` -- the default node/review-gate catalog.

A user or team never edits these files. They customize by adding files under
their own extension directory instead (`~/.graph-ops/extensions/...`
or `<project>/.graph-ops/extensions/...`), which the engine resolves
on top of this content at runtime -- see `README.md` at the repo root
("Extension context for skills/agents/reports") for the full merge model,
and `graph-engine get-node-type-context <type>` /
`get-skill-context <skill>` / `get-report-template` to see the merged result
for a given install.

`packages/core-go/internal/config/defaults/` is a generated mirror of this
directory (`go:embed` needs the files inside its own package tree) -- edit
here, then run `npm run sync:defaults` (or any `npm run build`/`build:go`/
`test`, which do it automatically) to refresh that mirror.

Only the three kinds of file listed above are copied. This `README.md` is
**not** a sync target, and the mirror's own README says something different on
purpose (it warns that the directory is generated). Verify the mirror with

```bash
diff -rq packages/plugin/defaults packages/core-go/internal/config/defaults -x README.md
```

Without `-x README.md` the two READMEs always report as a difference, which is
not one. Never add `README.md` to `sync:defaults`, and never copy one README
over the other.
