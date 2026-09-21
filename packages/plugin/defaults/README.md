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
- `plan/template.md` / `review/template.md` -- the fixed English Markdown
  templates `plan` and `review`/`review_gate` nodes fill in.
- `workflow.yaml` -- the default node/review-gate catalog.
- `locales/<code>.yaml` -- per-language display names for the fixed workflow
  skeleton's nodes and the default review gates.
- `locales/<code>/{plan,review}/template.md` -- translated plan/review
  templates. The engine never selects these by language: the onboarding
  skill copies them into the user's extension directory as overrides when
  that language is chosen.

A user or team never edits these files. They customize by adding files under
their own extension directory instead -- the user tier
(`~/.graph-ops/extensions/...`, or wherever `userExtensionsDir` /
`GRAPH_USER_EXTENSIONS_DIR` points) and the team tier, which is the shared
directory `teamExtensionsDir` / `GRAPH_TEAM_EXTENSIONS_DIR` names
(`<teamExtensionsDir>/extensions/...`). The team tier has no default: it
exists only when that setting points somewhere, and a `.graph-ops/` directory
inside a repository is never read. Those files are what the engine resolves
on top of this content at runtime -- see the "Settings" section of
`README.md` at the repo root ("Where these settings live") for the tiers,
and `graph-engine get-node-type-context <type>` /
`get-skill-context <skill>` / `get-report-template` / `get-plan-template` /
`get-review-template` to see the merged result for a given install.

`packages/core-go/internal/config/defaults/` is a generated mirror of this
directory (`go:embed` needs the files inside its own package tree) -- edit
here, then run `npm run sync:defaults` (or any `npm run build`/`build:go`/
`test`, which do it automatically) to refresh that mirror.

Everything listed above is copied except `locales/<code>/` directories: the
translated plan/review templates stay here only, because the engine does not
embed them (onboarding copies them from the plugin into the user tier). This
`README.md` is **not** a sync target either, and the mirror's own README says
something different on purpose (it warns that the directory is generated).
Verify the mirror with

```bash
diff -rq packages/plugin/defaults packages/core-go/internal/config/defaults -x README.md -x ja
```

Without `-x README.md` the two READMEs always report as a difference, which is
not one; without `-x ja` the plugin-only `locales/ja/` directory does. Add one
`-x <code>` per translated-template directory under `locales/`. Never add
`README.md` or a `locales/<code>/` directory to `sync:defaults`, and never
copy one README over the other.
