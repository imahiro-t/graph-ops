---
name: refine-ticket
description: Nails down a ticket's completion criteria and Why (background/purpose) and rewrites its description to fold them in. Does not create an execution graph.
---

# refine-ticket Skill

Nails down a ticket's "completion criteria" and "Why (background/purpose)", then rewrites the ticket's description around them. **This skill never creates an execution graph (nodes)** -- graph construction happens automatically on the `process-ticket` side.

## 0. Check for user/team customization of this skill

```bash
graph-engine get-skill-context "refine-ticket"
```

If the returned `content` is non-empty, follow it as additional rules on top of the steps below (this is how a team adds its own refinement conventions without editing this file -- see `README.md`).

## 1. Check the target ticket's current content

```bash
graph-engine get-ticket "<ticketId>"
```

## 2. Clarify the completion criteria and the Why with the user

Based on the ticket's title and description, work with the user to clarify:

- **Completion criteria**: the concrete bar this ticket must clear to be considered "done" (acceptance criteria, numeric targets, etc.)
- **Why**: why this work is needed (background, purpose, the problem this ticket solves)

## 3. Check whether the priority needs to be revisited

Look at the ticket's current priority (from step 1's `get-ticket` output) against what you now know about the ticket:

Every ticket always has one of `HIGH`/`MEDIUM`/`LOW`; a ticket created without an explicit judgment has the default `MEDIUM`.

- If it looks like it doesn't match the ticket's content (urgency, blast radius, whether it blocks other work) -- including a default `MEDIUM` that was never really judged -- judge the priority (`HIGH`/`MEDIUM`/`LOW`) yourself, present it to the user together with your reasoning, and ask them to confirm or override it.
- If it **still looks right**, confirm with the user that no change is needed and skip setting it -- don't force a re-judgment every time.

## 3b. Check whether the labels need to change

Look at the ticket's current `labels` in step 1's `get-ticket` output (an array of `{id, name, color, ...}`; `[]` means no labels), then:

1. **List the ticket's project's labels.** Always pass the ticket's `project_id` from step 1's `get-ticket` output -- without `--project` the command resolves the project from the current directory, which may be a different project than the ticket's:

   ```bash
   graph-engine list-labels --project "<ticket's project_id>"
   ```

   The output is a JSON array of `{id, name, color, ticket_count, ...}` (`[]` when the project has none).
2. **Propose existing labels.** If the clarified content suggests adding or removing labels, propose the change using the existing labels that fit, with a short reason for each. If the current labels still fit, say so and leave labels alone.
3. **Propose a new label only if nothing fits.** If no existing label covers something the ticket clearly needs, propose a new label: a name, and a color from the palette `gray red orange amber green teal blue indigo purple pink` -- or leave the color out and let `create-label` pick one the project doesn't use yet. Prefer an existing label over a near-duplicate new one.
4. **Register new labels only after the user approves them, then set them in step 4.** **Never create a label without the user's explicit approval** of that label -- a proposal is not approval, and the user may rename it, pick a different color, or decline it. Once approved, register each one in the ticket's project:

   ```bash
   graph-engine create-label "<name>" [--color <color>] --project "<ticket's project_id>"
   ```

   Then include it in the `--label` set of the `refine-ticket` command in step 4.

Keep in mind:

- `--label <name>` (repeatable) **replaces the ticket's whole label set** with exactly the names given. To add a label, pass every existing label you want to keep **plus** the new one; a label you leave out is removed.
- Omitting `--label` leaves the labels unchanged. There is no CLI way to remove every label -- that is done in the Web UI.
- Only labels registered for the ticket's project can be attached. Names are matched ignoring surrounding spaces and letter case. Label names are 1-50 characters and unique within the project ignoring letter case: `create-label` fails with `LABEL_NAME_TAKEN` when the same name in different case already exists -- use that existing label instead. On `INVALID_LABEL_NAME` / `INVALID_LABEL_COLOR` nothing is created; fix it with the user and try again.
- A label registered here stays in the project even if the refine is then abandoned or fails -- tell the user so; it can be removed in the Web UI.
- Renaming and deleting labels are done only in the Web UI's settings (設定 → ラベル); there is no CLI command for them.
- If any given name isn't registered, `refine-ticket` fails with `LABEL_NOT_FOUND` and **nothing on the ticket changes** (not the description, priority, labels or status). The error message lists the registered label names.
- Note: `refine-ticket` always sets the ticket's status to `REFINED`, even when you only change labels. If the ticket is already further along (e.g. `IN PROGRESS`), tell the user about this side effect before running it for a label-only change, or suggest changing the labels in the Web UI instead. (`graph-engine update-ticket` keeps the status but cannot change labels.)

## 4. Compose and write the full, updated description

Fold whatever from the current description (step 1) still applies together with the newly clarified completion criteria and why into one coherent piece of text -- this replaces the description outright, it does not get appended alongside the old text. Then write it, adding `--priority` only if step 3 concluded the priority should change:

```bash
graph-engine refine-ticket "<ticketId>" "<full updated description, including completion criteria and why>"
graph-engine refine-ticket "<ticketId>" "<full updated description, including completion criteria and why>" --priority <HIGH|MEDIUM|LOW>
```

For longer text, you can also pipe it in via stdin (`--priority` still works the same way, placed after the `-`):

```bash
graph-engine refine-ticket "<ticketId>" - <<'EOF'
<full updated description, including completion criteria and why>
EOF
```

`-` means "read stdin" only when it is the whole description argument, so use it only together with a pipe or heredoc (on its own in a terminal the command just waits for input). The text is saved byte for byte. If stdin is empty or only whitespace -- or cannot be read -- the command fails and **nothing on the ticket changes** (not the description, priority, labels or status; it does not become `REFINED`); fix the input and run it again. A description argument that is only whitespace (e.g. `"   "`) fails the same way.

If only the priority needs to change and the description doesn't, omit the description positional entirely -- `graph-engine refine-ticket "<ticketId>" --priority <HIGH|MEDIUM|LOW>` leaves the description untouched.

If the user only wants to correct the title, description or priority of a ticket that is already further along (e.g. `IN PROGRESS`) and does **not** want it to go back to `REFINED`, this skill is the wrong tool: `refine-ticket` always sets the status to `REFINED`. Use `graph-engine update-ticket "<ticketId>" [--title "<text>"] [--description "<text>"] [--priority <HIGH|MEDIUM|LOW>]` instead -- it changes only the fields given and never the status (`--description -` reads stdin, like above). It cannot change labels.

A priority can be changed to another level but can never be emptied or cleared: any value other than `HIGH`/`MEDIUM`/`LOW` is an error that leaves the ticket unchanged.

If step 3b concluded the labels should change, add one `--label <name>` per label the ticket should end up with (the full set, including existing labels to keep), in the same command:

```bash
graph-engine refine-ticket "<ticketId>" "<full updated description>" --label "<kept label>" --label "<new label>"
```

## 5. Report the result and suggest the next step

Report that the ticket's status is now `REFINED`, and suggest running `/graph-ops:process-ticket <ticketId>` next.
