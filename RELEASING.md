# Releasing GraphOps

This document describes the full procedure for cutting a GitHub Release of
GraphOps, from verifying the version is consistent across the repo through
confirming the published Release has every expected asset attached.

## 1. Pre-flight: verify the version is consistent

The release version is hardcoded in 5 places:

- `package.json`
- `packages/web/package.json`
- `packages/plugin/package.json`
- `packages/plugin/.claude-plugin/plugin.json`
- `.claude-plugin/marketplace.json` (the graph-ops entry's `git-subdir`
  source `ref`, as the tag `vX.Y.Z`)

Run:

```sh
npm run check:versions
```

This runs `scripts/check-versions.js`, which reads all 5 locations and fails
(non-zero exit, with a message naming every location and its value) if any
of them disagree. The same check also runs as the "Verify version
consistency across 5 locations" step in the `test` job of
`.github/workflows/ci.yml` on every push and pull request, so confirming
that CI job is green on the commit you intend to release is an acceptable
substitute for running the command locally.

Do not proceed to tagging until this passes. A tag whose version doesn't
match one of the 5 locations produces a release that disagrees with what
the plugin itself reports as its version.

## 2. Create and push the tag

```sh
git tag vX.Y.Z
git push origin vX.Y.Z
```

The tag name **must** be exactly `v` followed by the version string that
matches all 5 locations checked in step 1 (e.g. `v0.1.0` for version
`0.1.0`). This is a hard constraint stated at the top of
`.github/workflows/release.yml`: `packages/plugin/scripts/install-binary.js`
downloads release assets from the tag named `v<plugin.json's version>`, so
the plugin version and its release tag must always be bumped together. A
mismatched tag name will not be caught by `check:versions` (which only
compares the 5 in-repo locations to each other, not to the tag), so double
check the tag string itself before pushing it.

Pushing the tag is what triggers the release workflow below
(`.github/workflows/release.yml` triggers `on: push: tags: 'v*'`) -- once
pushed, a real GitHub Release build starts automatically.

## 3. What `release.yml` does

Pushing a `v*` tag runs three jobs in sequence:

1. **`web`** -- checks out the repo, installs dependencies with
   `npm ci --ignore-scripts`, and runs `npm run build:web` to build the web
   UI. It verifies the build output (`packages/web/dist/index.html` and a
   non-empty `assets/` directory) exists, then uploads two build artifacts:
   - `webdist` -- the built web UI, to be embedded into each binary.
   - `third-party-notices` -- the repo-root `THIRD_PARTY_NOTICES` file.

2. **`build`** -- runs once per target platform (4 targets: `darwin/amd64`,
   `darwin/arm64`, `linux/amd64`, `windows/amd64`), after `web` completes.
   Each run downloads the `webdist` artifact into
   `packages/core-go/internal/httpserver/webdist/` (checking it's really
   there before proceeding, since the Go binary `//go:embed`s that
   directory), then cross-compiles `graph-engine` with `CGO_ENABLED=0` for
   that `GOOS`/`GOARCH` and uploads the resulting binary as its own build
   artifact (e.g. `graph-engine-darwin-arm64`).

3. **`release`** -- runs after all 4 `build` jobs complete. It downloads all
   4 binary artifacts into `dist/` (using a `graph-engine-*` pattern so the
   unrelated `webdist` artifact isn't pulled in), generates
   `dist/checksums.txt` via `sha256sum * > checksums.txt`, downloads the
   `third-party-notices` artifact into a separate `notices/` directory (kept
   out of `dist/` so it is never included in `checksums.txt`), and finally
   runs:
   ```sh
   gh release create "$TAG" dist/* notices/THIRD_PARTY_NOTICES \
     --repo "$REPO" --title "$TAG" --generate-notes
   ```
   creating the GitHub Release and attaching all 4 platform binaries, plus
   `checksums.txt`, plus `THIRD_PARTY_NOTICES`, as separate Release assets.

   This is the only job in the workflow with `contents: write` permission
   (every other job is read-only), and it checks out no code and runs no
   npm/Go build steps of its own -- it only assembles and publishes what the
   earlier jobs produced.

Monitor the run with:

```sh
gh run list --workflow=release.yml
gh run watch <run-id>
```

The full `web` -> `build` (x4) -> `release` pipeline takes a few minutes.
Do not move on to step 4 until `gh run watch` reports the run finished
successfully -- checking the Release before the `release` job completes
will show it incomplete or missing entirely.

## 4. Confirm the Release is published with all expected assets

```sh
gh release view vX.Y.Z
```

(or open the Release in the GitHub web UI). Confirm all of the following
are attached:

- `graph-engine-darwin-amd64`
- `graph-engine-darwin-arm64`
- `graph-engine-linux-amd64`
- `graph-engine-windows-amd64.exe`
- `checksums.txt`
- `THIRD_PARTY_NOTICES`

### About `THIRD_PARTY_NOTICES`

The repo-root `THIRD_PARTY_NOTICES` file (third-party license notices for
`packages/core-go`'s statically-linked Go dependencies) is picked up and
uploaded as a build artifact by the `web` job (the only early job that has
the repo checked out), and attached to the Release by the `release` job as
its own, separate asset -- it is downloaded into `notices/`, kept outside
`dist/`, so it is never bundled into `checksums.txt` alongside the binaries.
This document only covers that it gets attached during a release; how the
file itself is generated or kept up to date is out of scope here and is
tracked separately.

If any expected asset is missing, the release is not complete: the binaries
alone are not installable without a matching `checksums.txt` (see
`install-binary.js`'s `downloadAndVerify()`, which refuses to install a
binary whose SHA256 doesn't match), so do not direct users to a Release
until every item in the list above is confirmed present.
