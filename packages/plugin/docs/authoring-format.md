# Authoring format for node-type and skill instructions

The Markdown files under `defaults/node-types/` and `skills/` are not prose
documentation for humans -- they are injected verbatim into an agent's context
at runtime (`graph-engine get-node-type-context <type>` /
`get-skill-context <skill>`), and an agent acts on whatever they say. This
document is the single, authoritative format for those files: heading
structure, voice, what may and may not be written, and length. Anyone editing
one of those files must follow this document, and a reviewer checking such an
edit must judge against this document rather than against personal taste.

## 1. Scope

In scope -- every file below must conform:

| Group | Files |
|---|---|
| Node types | `packages/plugin/defaults/node-types/*.md` (11 files) |
| Skills | `packages/plugin/skills/*/SKILL.md` (4 files) |

The file name matches the node type in every case except `report`, whose
default content lives in `deliverable.md` (see `nodeTypeDefaultFile` in
`packages/core-go/internal/config/extensions.go`).

Out of scope -- do not reformat these under this document:

- `packages/plugin/agents/*.md` and `packages/plugin/commands/*.md`. They are
  the same genre of instruction text and are a candidate for a future
  extension of this format, but they are deliberately not covered today.
- `packages/plugin/defaults/workflow.yaml`, `packages/plugin/defaults/report/template.html`,
  `README.md` files, and everything outside `packages/plugin`.
- User/team extension files (`<root>/extensions/node-types/<type>.md`,
  `<root>/extensions/skills/<skill>.md`). Those belong to whoever installs
  the plugin; the engine appends them after the text of the in-scope files.

## 2. Principles

1. **Correctness outranks the length range.** The ranges in sections 3.5 and
   4.5 are targets, not quotas. Never invent a rule, a step, or a constraint
   in order to reach a lower bound. If a file runs short after you have
   written everything that is true, leave it short and record why.
2. **Write only what is already true.** These files change agent behaviour.
   Reformatting must not introduce new obligations. Everything you write must
   be either (a) something the file's existing text already says or directly
   implies, or (b) behaviour the engine actually enforces, verifiable in the
   Go source. Anything else -- "how we would like this to work", a naming
   convention nobody enforces -- must not be written here.
3. **One structure for all files in a group.** The value of this format is
   that an agent reading any node-type file finds the same sections in the
   same order. A file that is "better" in its own bespoke shape is worse in
   aggregate.

## 3. Format for `defaults/node-types/*.md`

### 3.1 Required structure

```
(opening paragraph, no heading -- 1 to 3 sentences)

## Procedure

1. ...
2. ...

## Artifacts

...

## Notes

- ...
```

- **Opening paragraph (required).** No heading. One to three sentences
  stating what this node type is responsible for. If the type is frequently
  confused with another, add a sentence saying what it does *not* do.
- **`## Procedure` (required).** A numbered list of the steps the agent
  performs, in order. Each item starts with a verb in the imperative. Show
  every command to run in backticks, with placeholders in angle brackets:
  `` `get-review-criteria "<nodeId>"` ``.
- **`## Artifacts` (required).** What the node must save through
  `add-artifact`, and with which artifact type. If the node saves nothing,
  say so explicitly in one sentence -- do not omit the section.
- **`## Notes` (optional).** Include it only when there is something an agent
  would plausibly get wrong: a prohibition, a boundary condition, or how this
  type divides responsibility with a neighbouring type. Omit the heading
  entirely when there is nothing to say; never leave it present and empty.

Headings use exactly these names, exactly this order. Do not add, rename, or
reorder sections.

### 3.2 What may go in `## Procedure`

Permitted:

- Steps that the file's existing sentence already states or implies.
- Engine-enforced behaviour that is verifiable in the source. The facts below
  are verified and may be relied on:
  - `review` and `review_gate` nodes fetch their criteria with
    `get-review-criteria "<nodeId>"`. A `review_gate` gets gate-specific
    criteria plus the iteration convergence criteria; a `review` gets only
    the convergence criteria.
  - `report` nodes fetch the fixed template with `get-report-template`, and
    their `html` artifact is validated against the template's structural
    markers (`data-report-template="`, `data-report-version="`, and
    `data-report-section="header"|"summary"|"results"|"footer"`), so
    free-formed HTML is rejected at save time
    (`packages/core-go/internal/config/extensions.go`, `requiredReportMarkers`
    / `ValidateReportHTML`).
  - For `add-artifact`, the `text`, `gherkin`, and `json` types never read a
    file path -- the final argument is taken as literal content. Pass `-` to
    read the content from stdin instead. Only `html` and `image` resolve a
    real path on disk (`packages/core-go/cmd/graph-engine/main.go`).

Not permitted:

- Operational advice with no enforcement behind it.
- Steps copied from a different node type because they "seem generally good".

### 3.3 What may go in `## Artifacts`

| | |
|---|---|
| Write | The *kind* of output to save (for example: the execution plan text; the verdict and the reasoning behind it). |
| Write | The `add-artifact` type to use: `text`, `gherkin`, `html`, `image`, or `json`. |
| Write | Guidance on naming, at most: "give it a name that describes its content". |
| Do not write | A fixed artifact name. |

The prohibition on fixed names is not stylistic. Artifact names are free-form
strings; nothing in the repository defines a per-node-type default name, so
writing one here would not be documenting an existing convention -- it would
be creating a new one, and it would diverge from the names already stored on
existing tickets.

### 3.4 Voice

- English, second person imperative, addressed to the agent executing the
  node ("Write an execution plan...", "Fetch the review criteria...").
- Use `must` and `do not` for anything binding. Do not use `should` -- an
  agent reading `should` cannot tell whether the step is optional.
- Refer to files, commands, node types, and artifact types in backticks.
- Use `--` for a parenthetical dash and plain ASCII quotes. Do not introduce
  typographic dashes or curly quotes. Non-ASCII text is allowed only where it
  is the subject matter itself (for example the language examples in
  `onboarding/SKILL.md`).

### 3.5 Length

Length is the file's total line count as reported by `wc -l`, blank lines
included.

| Group | Types | Range |
|---|---|---|
| Standard | `plan`, `investigation`, `gherkin_spec`, `gherkin_test`, `implementation`, `review`, `review_gate`, `documentation`, and `report` (whose file is `deliverable.md`) | 15-45 lines |
| Manual | `approval_gate`, `release` | 15-80 lines |

The upper bound is binding: a file over it is too long and must be tightened.
The lower bound is a target only, governed by principle 1 -- a file that falls
short after everything true has been written stays short, and the file name
and the reason are recorded in the artifact of the node that made the edit.

The two groups share the same lower bound deliberately. Manual types carry
more mechanism to explain, so they get a wider ceiling, but there is no reason
a floor should be higher for them; a higher floor would only push an author
toward padding, against principle 1.

### 3.6 Prohibited

- H1 (`#`). These files are injected into an agent's context alongside other
  text; an H1 claims to be the top of a document and breaks the surrounding
  structure. Start at H2.
- Pseudo-headings such as `**1. Work out the candidate scopes.**` used to
  structure steps. Steps belong in the numbered list under `## Procedure`.
- H3 and deeper inside a node-type file. If content needs a third level, it
  is too detailed for this file.

### 3.7 Worked example

Illustrative only -- it shows the shape, not content to copy into another
type. The real content of each file must come from what that file already
says plus verified engine behaviour.

```markdown
Investigate the topic this ticket asks about and report what you found. This
node does not change code; it produces the findings later nodes act on.

## Procedure

1. Read the ticket and the artifacts earlier nodes have already saved.
2. Investigate the topic, gathering evidence from the repository and any
   other source the ticket points at.
3. Separate what you verified from what you inferred, and record where each
   conclusion came from.

## Artifacts

Save the findings, the conclusions drawn from them, and the references
backing them as one `text` artifact via `add-artifact`. Give it a name that
describes its content.

## Notes

- Cite the file and location behind each factual claim so a later node can
  re-check it without repeating the investigation.
```

## 4. Format for `skills/*/SKILL.md`

### 4.1 Frontmatter

```yaml
---
name: <skill-name>
description: <1 to 3 sentences>
---
```

- `name` matches the directory name.
- `description` is written in the third person present tense ("Creates a new
  development ticket in the DB."), and states what the skill does *not* do
  when that is a common misunderstanding ("Does not create an execution
  graph.").
- When aligning an existing `description`, change the voice only. The text is
  what a model reads to decide whether to select this skill, so the
  distinguishing content must survive the edit unchanged.

### 4.2 Required structure

```
---
name: ...
description: ...
---

# <skill-name> Skill

(opening paragraph, no heading -- 1 to 3 sentences)

## 0. Check for user/team customization of this skill

...

## 1. <step name>

## 2. <step name>
```

- Exactly one H1, immediately after the frontmatter, in the form
  `# <skill-name> Skill`. This is the one place an H1 is correct: a SKILL.md
  is the root of its own document, not a fragment injected into another.
- An opening paragraph with no heading, one to three sentences, stating the
  skill's purpose.
- Numbered H2 sections from `## 0.` upward, one per step, in execution order.
- H3 is allowed only for a sub-step inside an H2 section. Do not use H4.
- Do not use a generic container heading such as `## Steps` with the real
  steps as list items beneath it. Every step is its own numbered H2.

### 4.3 Section 0 is fixed

Every skill starts with:

```markdown
## 0. Check for user/team customization of this skill
```

All four skills already perform this check; the heading text is what is being
standardized. When promoting an existing step 0 from a list item to an H2,
move the wording across unchanged -- promoting the heading must not alter what
the step tells the agent to do.

### 4.4 Voice

Same as section 3.4, with one difference: a skill addresses the session
running the skill, so it may say "this session" and "you". A command that
stands on its own line goes in a fenced `bash` block rather than inline
backticks.

### 4.5 Length

25-150 lines by `wc -l`, frontmatter included. The upper bound is binding; the
lower bound is a target, governed by principle 1.

### 4.6 Worked example

Illustrative excerpt showing the promotion of a step-0 list item to a numbered
H2 and the removal of a `## Steps` container:

```markdown
# create-ticket Skill

Creates a new ticket using the DB access binary (`graph-engine`). The ticket
is created from a title/description that has already been talked through with
the user, not from their very first message verbatim.

## 0. Check for user/team customization of this skill

Run `graph-engine get-skill-context "create-ticket"`. If the returned
`content` is non-empty, follow it as additional rules on top of the steps
below.

## 1. Get the user's initial idea for the ticket

A rough title/description is enough to start.
```

## 5. Checklist before you finish an edit

Run through this for every file you touched:

- [ ] Headings match section 3.1 (node types) or 4.2 (skills) exactly -- same
      names, same order, no extras.
- [ ] No H1 in a node-type file; exactly one H1 in a SKILL.md.
- [ ] No pseudo-headings standing in for structure.
- [ ] Every statement is either already in the previous version of the file or
      verified against the Go source (principle 2).
- [ ] `## Artifacts` names a type and a kind of output, and no fixed artifact
      name.
- [ ] Voice is imperative English; no `should`.
- [ ] `wc -l` is within the upper bound. If it is under the lower bound, the
      file name and reason are recorded in the node's artifact.

## 6. After editing `defaults/`

`packages/core-go/internal/config/defaults/` is a generated mirror of
`packages/plugin/defaults/` (`go:embed` cannot reach outside its own package
tree). After editing any file `sync:defaults` copies -- `node-types/*.md`,
`report/template.html`, `plan/template.md`, `review/template.md`,
`workflow.yaml`, or `locales/*.yaml` -- run:

```bash
npm run sync:defaults
```

Verify the mirror with:

```bash
diff -rq -x README.md -x ja packages/plugin/defaults packages/core-go/internal/config/defaults
```

`README.md` is excluded on purpose. It is not a sync target, and the two
README files differ by design -- the one under `packages/plugin/defaults/`
describes the canonical content, the one under the mirror warns that the
directory is generated. Never add `README.md` to `sync:defaults`, and never
copy one over the other.

`ja` is excluded for a similar reason. The translated plan/review templates
under `locales/ja/` live in the plugin only: the engine does not embed them,
and the onboarding skill copies them into the user tier as overrides. Editing
them needs no sync step.

Files under `docs/` (this document included) are not mirrored and need no
sync step.
