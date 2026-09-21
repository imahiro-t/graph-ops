---
name: onboarding
description: First-time setup for the GraphOps plugin. Asks which language the user wants the plugin to work in and persists that choice so future tickets, node names, and agent-authored deliverables (plans, Gherkin specs/tests, implementation notes, review verdicts, reports) are produced in that language. Re-run any time to change the language.
---

# onboarding Skill

Configures the plugin's working language. This is a personal (user-tier) setting: it writes only into the current user's extension root, never the team root, so it never overrides a team's shared conventions.

## 0. Check for user/team customization of this skill

```bash
graph-engine get-skill-context "onboarding"
```
If `content` is non-empty, follow it as additional rules on top of the steps below.

## 1. Find the user extension root

```bash
graph-engine get-extension-roots
```
Returns `{"userDir": "...", "teamDir": "..."}`. Use `userDir` for every write below (create the directory tree with `mkdir -p` as needed -- it may not exist yet on a fresh install). Never guess this path (e.g. `~/.graph-ops`) -- it can be overridden by `GRAPH_USER_EXTENSIONS_DIR` or by `userExtensionsDir` in `$HOME/.graph-ops/config.json` (the one file settings are read from), and this command already resolves that for you.

## 2. Ask the user which language to use

Ask the user which language the plugin should use for everything it generates on their behalf (node display names, and every agent-authored deliverable -- plans, investigation write-ups, Gherkin specs/tests, implementation notes, review verdicts, report bodies). Default the suggestion to the language the user is currently writing to you in (e.g. offer "日本語" first if they're chatting in Japanese), but let them pick anything (English, 日本語, or another language entirely).

Check what's already configured with:

```bash
graph-engine get-language-settings
```
This returns `{"resolved": "ja"|"", "source": "team"|"user"|"none", "supported_locales": ["ja"]}`. If `source` is `"user"` (or `"team"`, though this skill only ever writes the user tier), mention the currently-configured language and ask whether they want to keep it or change it, instead of assuming a fresh setup. If `<userDir>/config.yaml` already carries hand-translated `review_gates[].name` entries from an older install (pre-language-file), that still counts as "already configured" even if `get-language-settings` reports no `language:` field -- ask the same way.

This setting does not change the Web UI's own language (that has its own i18n setting) -- it only affects what the CLI/agents write into the DB (ticket-adjacent text, node names) and into artifacts.

## 3. Update `<userDir>/config.yaml` -- persist the chosen language

Match the user's chosen language against `supported_locales` from step 2's `get-language-settings` output (e.g. `["ja"]`). Which of the two cases below applies determines what you write.

**Case A -- the chosen language has a supported locale code (currently: Japanese/`ja`).** The plugin's fixed skeleton nodes (`plan`, `plan_review`, `plan_approval`, `gherkin_spec`, `gherkin_review`, `impl`, `gherkin_test`, `test_review`, `report`, `report_review`, `release_approval`, `release` -- see `${CLAUDE_PLUGIN_ROOT}/defaults/workflow.yaml`) and the six default review gates' `name`s are no longer translated by hand here: a language-file mechanism (`${CLAUDE_PLUGIN_ROOT}/defaults/locales/<code>.yaml`) auto-localizes both wherever this code is set as the resolved language. All you do is write the top-level `language: <code>` field into `<userDir>/config.yaml` (e.g. `language: ja`), preserving every other key/entry in the file untouched (disabled gates, custom node types, custom review-gate criteria, etc. -- read the file first if it exists, and use targeted edits, not a wholesale overwrite). Do **not** also write individual `review_gates[].name` translations for this case -- the locale file already covers them, and doing both would just be redundant (though harmless: an explicit `name` here would still win over the locale per the usual override precedence). If the file already has old hand-written `review_gates[].name` entries in the same language from a previous onboarding run, you may leave them in place as-is -- cleaning them up is not required.

**Case B -- the chosen language has no supported locale code (anything other than Japanese today).** Fall back to the pre-language-file approach: do **not** set the top-level `language:` field (there is no locale file it would select, and an unsupported code is silently ignored anyway -- see below -- so it would have no effect either way). Instead, hand-translate each review gate's display `name` the same way this skill always used to: read `<userDir>/config.yaml` if it exists (preserving unrelated content), then set (add or update -- never remove other keys or entries) the `name` field for each of `review_gates[]` (by key) -- `code_review`, `qa_review`, `security_review`, `non_functional_review`, `investigation_review`, `accessibility_review` -- to a natural translation into the chosen language. `criteria`/`max_iterations`/`enabled` must never be touched here -- only `name`. There is no fixed translation table for this fallback case; you are the translator. Also tell the user (in step 7) that the fixed skeleton node names (`plan`, `impl`, `report`, etc.) will keep showing in English, since no locale file exists yet for their chosen language -- only the review gates they just had translated will show in their language.

An unsupported/unrecognized `language:` code is always silently ignored by the engine (falls back to the English skeleton, per-gate `name` overrides still apply normally) rather than erroring, so there is no wrong-code failure mode to worry about here -- the worst case is simply "no additional localization happens for the skeleton".

## 4. Update skill extension files -- language for user-facing dialogue and custom node names

For each of `create-ticket`, `refine-ticket`, `process-ticket`, write (create the file/dirs if missing) `<userDir>/extensions/skills/<name>.md`, replacing only the block between the sentinel comments below if they're already present (so re-running onboarding updates in place instead of duplicating), or appending the block with a blank line before it otherwise:

```
<!-- graph-ops:onboarding:language:start -->
Use <LANGUAGE> in every user-facing message for this skill. When this skill invents a node's `name` (e.g. process-ticket's `extra_nodes` in an `expand-graph` patch, or refine-ticket's own prose), write that name in <LANGUAGE> too.
<!-- graph-ops:onboarding:language:end -->
```
Substitute `<LANGUAGE>` with the chosen language written out naturally (e.g. `日本語` or `English`), not a code.

## 5. Update node-type extension files -- language for generated deliverables

For each of `plan`, `investigation`, `gherkin_spec`, `implementation`, `review`, `review_gate`, `gherkin_test`, `report`, write (create if missing) `<userDir>/extensions/node-types/<type>.md`, using the same sentinel-block replace-or-append rule as step 4:

```
<!-- graph-ops:onboarding:language:start -->
Write every artifact you produce for this node (body text, Gherkin feature/scenario text, review comments/verdict notes, report prose, etc.) in <LANGUAGE>. Do not translate fixed structural markers required by the format itself (e.g. a report's `data-report-template`/`data-report-version` attributes and section names, or Gherkin's `Feature:`/`Scenario:`/`Given`/`When`/`Then` keywords) -- only the human-authored content.
<!-- graph-ops:onboarding:language:end -->
```

(`release` is skipped -- it's a manual approval gate with no agent-authored content.)

## 6. Install localized plan/review templates

The `language:` field from step 3 does not change the plan or review template: `get-plan-template` and `get-review-template` return the team override, else the user override, else the plugin's English default. A translated template takes effect only as a user override, so install it here.

For each `<kind>` of `plan` and `review`, check whether the plugin ships a translated template for the chosen language's code (only `ja` does today):

```bash
test -f "${CLAUDE_PLUGIN_ROOT}/defaults/locales/<code>/<kind>/template.md"
```

If the chosen language has no code (step 3's Case B) or the file does not exist, write nothing for that kind, and leave any existing `<userDir>/extensions/<kind>/template.md` untouched.

Otherwise, compare it with the destination `<userDir>/extensions/<kind>/template.md`, using the `userDir` from step 1 (never a guessed path):

- **The destination does not exist**: copy the template there without asking.
  ```bash
  mkdir -p "<userDir>/extensions/<kind>"
  cp "${CLAUDE_PLUGIN_ROOT}/defaults/locales/<code>/<kind>/template.md" "<userDir>/extensions/<kind>/template.md"
  ```
- **The destination has the same content** (`cmp -s` succeeds): leave it as is, without asking, so re-running this skill stays quiet.
- **The destination has different content**: the user or an earlier setup wrote that override. Ask the user whether to replace it with the translated template. Copy only if they agree; otherwise keep it.

Never write a template into `teamDir`.

## 7. Confirm

Report back to the user, in the chosen language, that the plugin's review-gate names, any node names its own sessions invent going forward, and future agent-authored content will now be written in that language, and that this is a personal (user-tier) setting living under `<userDir>` -- it applies across every project on this machine for this user, and can be re-run anytime by invoking this skill again. If a team `workflow.yaml` also sets a `language` or a `name` for one of the review-gate keys above, mention that the team layer wins over this personal one (for `language`, whichever tier's value was actually used; for an individual gate's `name`, that specific gate).

If step 3 took Case A (a supported locale code), also mention that the fixed skeleton's display names (`plan`, `impl`, `report`, etc.) are now automatically shown in the chosen language too, via the `language: <code>` setting just saved -- this is new behavior beyond what earlier versions of this skill did (those could only translate review gates, never the skeleton). If step 3 took Case B (no supported locale yet), mention explicitly that the skeleton names will remain in English for now, even though the review gates were just translated, since no locale file exists yet for that language.

Also report what step 6 did for each of the plan and review templates: installed the translated template, replaced an existing override, kept an existing override because the user declined, or wrote nothing because no translated template exists for that language. When a translated template is now in place, add that it can be reviewed and edited in the Web UI under Settings -> Templates, and that saving it empty there returns to the plugin's English default (unless a team override applies). When nothing was written but an override from an earlier run is still there (for example a Japanese template after switching to English), mention that clearing it the same way returns to the English default.
