---
name: create-ticket
description: Creates a new development ticket in the DB. Does not create an execution graph.
---

# create-ticket Skill

Creates a new ticket using the DB access binary (`graph-engine`). The user may start from a free-form request rather than a title/description; the ticket is created from a title/description worked out from that request and confirmed with the user, not from their very first message verbatim.

## 0. Check for user/team customization of this skill

```bash
graph-engine get-skill-context "create-ticket"
```

If the returned `content` is non-empty, follow it as additional rules on top of the steps below (this is how a team adds its own ticket-creation conventions without editing this file -- see `README.md`).

## 1. Get the user's initial idea for the ticket

A free-form request describing the ticket the user wants is enough to start. It does not need to be split into a title and description -- for example, the Web UI's "New Ticket" modal launches this skill with a single free-form request text. A rough title/description is fine too.

## 2. Firm the idea up with the user before creating anything

If the request is not already in title/description form, first work out a draft title and description from it yourself and present them to the user. Do not reuse the original request text verbatim as the title: write a short title that names the work, and put the details in the description.

Have a short back-and-forth with the user. Depending on what's missing, ask about things like:

- What exactly should happen, and what's explicitly out of scope?
- Which part of the codebase/feature does this touch?
- Any known edge cases, constraints, or examples that clarify the intent?

Then propose a concrete title + description back to the user and ask them to confirm or adjust it. Iterate until the user agrees it's ready -- don't treat the first reply as final. Never create the ticket without the user's confirmation of the title and description, even when the request arrived from the Web UI.

Keep this pass lightweight (a couple of exchanges, not a full requirements interview): it only needs to leave the ticket with a clear, actionable identity of the work, not a settled completion criteria/why -- that's `/graph-ops:refine-ticket`'s job afterward.

Once the title/description are settled, judge the ticket's priority (`HIGH`/`MEDIUM`/`LOW`) from its content -- urgency, blast radius, whether it blocks other work, how visible the problem is -- and present that judgment to the user together with your reasoning, then ask them to confirm or override it before creating the ticket. If the user has no particular preference, don't insist: the ticket can be created without `--priority`, and it then gets the default priority `MEDIUM`.

## 3. Create the ticket

Use the title/description agreed on in step 2, not the user's original raw message. If a priority other than the default was confirmed in step 2, pass it via `--priority`:

```bash
graph-engine create-ticket "<title>" "<description>"
graph-engine create-ticket "<title>" "<description>" --priority <HIGH|MEDIUM|LOW>
```

Omitting `--priority` creates the ticket with priority `MEDIUM` (the default). Every ticket always has one of `HIGH`/`MEDIUM`/`LOW` -- a priority can never be left empty.

The target project is resolved from the current directory: the project whose local path -- this environment's `projectPaths` entry in `graph-config.json`, not a DB value -- is the current directory or contains it (the deepest nested local path wins). Only if none matches does it fall back to the current project selected via `use-project` / the Web UI, and it errors if neither exists. Stdout is the ticket JSON only; the resolution is reported as one stderr line:

- `resolved project: <name> (<id>) from current directory` -- matched by the current directory.
- `resolved project: <name> (<id>) from current project (use-project)` -- no match, so the fallback was used. Check that this is the project the user meant before reporting.

Paths are compared as written, so a current directory reached through a symlink or spelled with different letter case does not match and falls back. If the fallback picked the wrong project, tell the user -- the ticket has already been created there, so don't just run the command again. To target a project explicitly, pass `--project <projectId>` (nothing is printed to stderr in that case).

The ticket is always created unassigned. Assignment is set afterward with the Web UI's per-ticket 「担当する」 ("Assign to me") button, which uses the name configured under the Web UI's Global Settings (全体設定) → App Settings (アプリ設定) → My Profile (自分の情報) (`myName`) and is hidden while that name is blank; there is no CLI command for it, and `create-ticket` takes no assignee argument.

## 4. Report the created ticket

Present the created ticket's ID (e.g. `TICK-XXXXX`) and details to the user.

## 5. Suggest the next step

Suggest running `/graph-ops:refine-ticket <ticketId>` next.
