Generated mirror -- do not edit these files directly.

The canonical, hand-edited source is `packages/plugin/defaults/` (the actual
plugin content shipped to users). `npm run sync:defaults` copies it here so
`go:embed` can bundle it into the compiled binary (`go:embed` cannot reach
outside this package's own directory tree). `npm run build`/`build:go`/`test`
all run that sync automatically.

`sync:defaults` copies only `node-types/*.md`, `report/template.html`,
`plan/template.md`, `review/template.md`, `workflow.yaml` and
`locales/*.yaml`. The translated plan/review templates under
`packages/plugin/defaults/locales/<code>/` are deliberately **not** copied:
the engine never selects a template by language, and the onboarding skill
copies them from the plugin into the user tier as overrides. This `README.md`
is **not** a sync target either -- it differs from
`packages/plugin/defaults/README.md` on purpose, because the two directories
need opposite warnings. So verify that the mirror is up to date with

```bash
diff -rq packages/plugin/defaults packages/core-go/internal/config/defaults -x README.md -x ja
```

and read a bare `diff -rq` (which always flags these two READMEs and the
plugin-only `locales/ja/` directory) as expected differences, not as a stale
mirror. CI checks the same thing a
different way: it runs the sync and then fails if `git status --porcelain`
reports anything under this directory.
