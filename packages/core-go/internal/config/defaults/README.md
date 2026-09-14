Generated mirror -- do not edit these files directly.

The canonical, hand-edited source is `packages/plugin/defaults/` (the actual
plugin content shipped to users). `npm run sync:defaults` copies it here so
`go:embed` can bundle it into the compiled binary (`go:embed` cannot reach
outside this package's own directory tree). `npm run build`/`build:go`/`test`
all run that sync automatically.

`sync:defaults` copies only `node-types/*.md`, `report/template.html` and
`workflow.yaml`. This `README.md` is **not** a sync target -- it differs from
`packages/plugin/defaults/README.md` on purpose, because the two directories
need opposite warnings. So verify that the mirror is up to date with

```bash
diff -rq packages/plugin/defaults packages/core-go/internal/config/defaults -x README.md
```

and read a bare `diff -rq` (which always flags these two READMEs) as one
expected difference, not as a stale mirror. CI checks the same thing a
different way: it runs the sync and then fails if `git status --porcelain`
reports anything under this directory.
