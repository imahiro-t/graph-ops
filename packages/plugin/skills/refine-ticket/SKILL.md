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

## 3. Check whether the priority needs to be set or revisited

Look at the ticket's current priority (from step 1's `get-ticket` output) against what you now know about the ticket:

- If it's **unset**, or looks like it no longer matches the ticket's content (urgency, blast radius, whether it blocks other work), judge the priority (`HIGH`/`MEDIUM`/`LOW`) yourself, present it to the user together with your reasoning, and ask them to confirm or override it. If the user decides no priority is needed, leave it unset.
- If it's **already set and still looks right**, confirm with the user that no change is needed and skip setting it -- don't force a re-judgment every time.

## 4. Compose and write the full, updated description

Fold whatever from the current description (step 1) still applies together with the newly clarified completion criteria and why into one coherent piece of text -- this replaces the description outright, it does not get appended alongside the old text. Then write it, adding `--priority` only if step 3 concluded the priority should change:

```bash
graph-engine refine-ticket "<ticketId>" "<full updated description, including completion criteria and why>"
graph-engine refine-ticket "<ticketId>" "<full updated description, including completion criteria and why>" --priority <HIGH|MEDIUM|LOW|none>
```

For longer text, you can also pipe it in via stdin (`--priority` still works the same way, placed after the `-`):

```bash
graph-engine refine-ticket "<ticketId>" - <<'EOF'
<full updated description, including completion criteria and why>
EOF
```

If only the priority needs to change and the description doesn't, omit the description positional entirely -- `graph-engine refine-ticket "<ticketId>" --priority <HIGH|MEDIUM|LOW|none>` leaves the description untouched.

## 5. Report the result and suggest the next step

Report that the ticket's status is now `REFINED`, and suggest running `/graph-ops:process-ticket <ticketId>` next.
