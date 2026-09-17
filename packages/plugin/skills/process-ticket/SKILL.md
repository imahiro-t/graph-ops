---
name: process-ticket
description: Decides the execution graph's shape based on the ticket's content (investigation-only, implementation-only, implementation + Gherkin tests, etc.), then drives it via parallel subagent execution, handling artifact storage and review convergence control.
---

# process-ticket Skill

Builds and executes the ticket's execution graph based on the nature of the ticket. **All node work must be done by subagents (the Agent tool, `graph-node-agent` subagent type -- see step 3 below) -- this session itself never does the work directly.** Artifacts must always be saved to the DB, and context sharing between subagents happens exclusively through the DB.

## 0. Check for user/team customization of this skill, and resolve the session's language

Before doing anything else, check whether the user or team has added extra instructions for this skill:
```bash
graph-engine get-skill-context "process-ticket"
```
This returns `{"skill": "process-ticket", "content": "..."}`, merging (in order) any content from a configured user extension directory then a team extension directory (`GRAPH_USER_EXTENSIONS_DIR` / `GRAPH_TEAM_EXTENSIONS_DIR`, see `README.md`). If `content` is non-empty, treat it as additional rules to follow on top of everything below for the rest of this session -- this is how a team standardizes wording/rules without ever editing this file. An empty `content` means no customization is configured; proceed as-is.

Next, check whether a persistent language setting already exists, since that decides whether every `get-executable`/`expand-graph` call below (step 1's seeding call in particular -- it's the one that actually writes the seed nodes' `name`s to the DB) needs an explicit `--language` flag:
```bash
graph-engine get-language-settings
```
This returns `{"resolved": "ja"|"", "source": "team"|"user"|"none", "supported_locales": ["ja"]}`.
- If `source` is `"team"` or `"user"`, a persistent setting already exists -- **do not** pass `--language` to `get-executable`/`expand-graph` below; let them resolve it themselves from that persistent tier, so this session never silently overrides a deliberately-configured team/user language just because of how this one conversation happens to be phrased. (If the user explicitly asks, in this session, to use a different language for this ticket only, that request overrides this default -- pass `--language <code>` for that one ticket's calls in that case.)
- If `source` is `"none"`, nothing persistent is set yet. Judge the language this session's conversation is actually happening in (the ticket text, the user's own messages) and pass `--language <code>` on every `get-executable`/`expand-graph` call for this ticket from here on -- **especially** the very first `get-executable` call in step 1, since that is the one that seeds `plan`/`plan_review`'s `name`s into the DB and cannot be changed by a later flag once it has run. An unsupported/unrecognized code (not in `supported_locales`) is harmless to pass -- the engine silently ignores it and falls back to the English skeleton -- so there's no need to cross-check the code against `supported_locales` yourself first.

This is a call-scoped, non-persistent choice (see `--language`'s own description in `graph-engine help`) -- it never writes anything to `<userDir>/config.yaml` or a team `workflow.yaml`. Persisting a language choice across sessions is the onboarding skill's job (`graph-engine ui`'s settings, or re-running onboarding), not this skill's.

## 1. Check/run the seed

Calling `get-executable` for the first time auto-generates the 2 seed nodes: "Plan Creation" (`plan`) and "Plan Review" (`plan_review`). Like any other node, drive these 2 nodes through the steps in "3. Execution loop" below (launching subagents). Per step 0 above, include `--language <code>` here if (and only if) `get-language-settings` reported `source: "none"`.

```bash
graph-engine get-executable "<ticketId>" [--language <code>]
```

## 2. Decide how to expand the graph (once the seed is done and `get-executable` returns empty)

Once both seed nodes (`plan`, `plan_review`) pass, `get-executable` returns an empty list. This means "the graph hasn't been expanded yet" -- at this point, **decide what node structure this ticket needs and expand it with `expand-graph --patch`**. There is no "use the default shape as-is" shortcut: every ticket's graph is assembled by an explicit patch, because which review gates apply and whether a documentation node is needed are per-ticket judgment calls, not a fixed template.

1. Run `graph-engine get-ticket "<ticketId>"` to check the ticket's description and the `plan` node's artifact (the execution plan).
2. Run `graph-engine get-workflow-catalog` to see every available review gate (`review_gates`, referenceable via `gate_ref`) -- this includes the standard four (`code_review`, `qa_review`, `security_review`, `non_functional_review`), situational ones the plugin ships (`accessibility_review`, `investigation_review`), and anything a team/user extension added on top. Treat this list as a pool of *available* checks, not a checklist to include wholesale -- a gate being enabled in the catalog only means it's a legal choice, not that this ticket needs it. The same call's `nodes` field is the plugin's fixed default template (types, names, `depends_on` shape) -- unlike `review_gates`, it is no longer team/project-configurable, but it's still worth reading as a naming/structure reference. It is a *reference*, not the fixed shape of every ticket's graph: step 4 below is not limited to reproducing it or the templates shown there. It no longer doubles as a registry of team-defined custom node types (a type that only ever appeared there, never in an actual ticket's patch, has no other trace) -- see step 3's last bullet for when a step needs a type not shown here.
3. Decide the following, from the ticket description and the plan artifact:
   - **Does it involve implementation (code changes)?**
   - **Does it need Gherkin-based behavioral testing?**
   - **Does it need an in-repository documentation update** (`README.md`, a `CHANGELOG`, files under `docs/`, etc.)? Most tickets don't; only include this when the change actually makes existing repo documentation stale or clearly calls for a new entry (see `node-types/documentation.md` for the node's exact scope -- by default it only touches files inside the repository, not any external doc system).
   - **Which review gates actually apply**, judged one by one against this ticket's actual content -- not "always the same 4":
     - `code_review` almost always applies to any ticket with implementation.
     - `qa_review` applies whenever there's a requirement/behavior to verify against.
     - `security_review` applies when the change touches auth, input handling, sensitive data, or anything else security-relevant -- skip it for changes with no plausible security surface (e.g. a pure copy/wording fix).
     - `non_functional_review` applies when the change plausibly affects performance, scalability, availability, or logging/monitoring -- skip it for changes where none of those are in play.
     - `accessibility_review` applies only when the change touches UI/user-facing behavior a person interacts with directly -- skip it for backend-only or non-UI changes.
     - Any team/user-added gate from `get-workflow-catalog` follows the same rule: include it only when its stated criteria are actually relevant to this ticket, never automatically just because it's enabled.
   - It's expected and normal for the resulting gate count to vary ticket to ticket (e.g. 2 for a narrow backend fix, 5 for a UI feature with security implications).
   - **Does this ticket need a step that doesn't fit any of the above** (e.g. a data migration, a config/infra rollout, a dependency upgrade check, a stakeholder sign-off distinct from the standard approval gates, a step specific to this team's own process)? If so, add a node for it: reuse a custom node type this same session (or a past one, if you have that context) already knows fits, otherwise invent a new one -- there is no catalog listing of previously-invented custom types to check against. A default-behavior judgment call, same as everything else in this step -- don't force-fit an unrelated built-in type just to avoid adding one, and don't invent a new type when an existing built-in or custom one already covers the need.
4. Assemble exactly the necessary nodes as a patch, built for this ticket rather than copied wholesale from a template. A patch **replaces** the catalog's default `nodes` list entirely (it does not inherit anything beyond the seed) -- so every patch must include its own pair of `approval_gate` nodes: one right after `plan_review` (gating everything that follows it) and one right before `release` (gating the release/deployment step itself). Rejecting either one requires a reason and is not something a human decides how to recover from -- see step 4 below.

   The three shapes below (investigation-only / implementation / implementation+Gherkin+docs) are common baselines to adapt, not an exhaustive menu -- freely insert, rename, reorder, or add extra nodes (including a freshly-invented custom `type`, per step 3's last bullet) wherever the ticket's actual content calls for it, as long as the graph stays well-formed: no cycles, every `depends_on`/`loop_back_to` target actually exists in the same patch (or is the seed), and the `approval_gate`/`release` wiring above is intact. A custom type you invent on the spot has no instruction file behind it (see step 3 of "Execution loop" below) -- give it a clear, descriptive `id`/`name` so the subagent that runs it has enough to go on, and mention in its `name` what "done" looks like if that isn't obvious from the id alone.

   - **Investigation only** (no implementation or Gherkin needed, e.g. "investigate X and summarize it"):
     ```json
     {
       "extra_nodes": [
         { "id": "plan_approval", "type": "approval_gate", "name": "Plan Approval", "depends_on": ["plan_review"] },
         { "id": "investigation", "type": "investigation", "name": "Investigation", "depends_on": ["plan_approval"] },
         { "id": "investigation_review", "type": "review_gate", "gate_ref": "investigation_review", "depends_on": ["investigation"], "loop_back_to": "investigation" },
         { "id": "release_approval", "type": "approval_gate", "name": "Release Approval", "depends_on": ["investigation_review"] },
         { "id": "release", "type": "release", "name": "Release & Deployment (Manual)", "is_manual": true, "depends_on": ["release_approval"] }
       ]
     }
     ```
     If this ticket also needs a documentation/runbook artifact, insert it the same way the implementation examples below do (depends on `investigation_review`, and `release_approval` depends on `documentation_review` instead of/alongside `investigation_review`).
   - **Implementation, with only the gates judged relevant in step 3** (here: `code_review` and `security_review`; drop/add ids per your own judgment for this ticket) -- each selected gate depends only on `impl`, with no dependencies between them, so they run in parallel:
     ```json
     {
       "extra_nodes": [
         { "id": "plan_approval", "type": "approval_gate", "name": "Plan Approval", "depends_on": ["plan_review"] },
         { "id": "impl", "type": "implementation", "name": "Implementation", "depends_on": ["plan_approval"] },
         { "id": "code_review", "type": "review_gate", "gate_ref": "code_review", "depends_on": ["impl"], "loop_back_to": "impl" },
         { "id": "security_review", "type": "review_gate", "gate_ref": "security_review", "depends_on": ["impl"], "loop_back_to": "impl" },
         { "id": "release_approval", "type": "approval_gate", "name": "Release Approval", "depends_on": ["code_review", "security_review"] },
         { "id": "release", "type": "release", "name": "Release & Deployment (Manual)", "is_manual": true, "depends_on": ["release_approval"] }
       ]
     }
     ```
   - **Implementation + Gherkin testing, with a documentation node** (a UI feature ticket, hence 5 gates including `accessibility_review`, plus docs) -- `documentation` runs in parallel with `test_review` off `gherkin_test`, is reviewed by a generic `review`-type node (not a `review_gate`: it's a single-artifact checkpoint like `plan_review`/`report_review`, not a multi-perspective gate), and `report` waits on both branches:
     ```json
     {
       "extra_nodes": [
         { "id": "plan_approval", "type": "approval_gate", "name": "Plan Approval", "depends_on": ["plan_review"] },
         { "id": "gherkin_spec", "type": "gherkin_spec", "name": "Gherkin Spec Creation", "depends_on": ["plan_approval"] },
         { "id": "gherkin_review", "type": "review", "name": "Gherkin Spec Review", "depends_on": ["gherkin_spec"], "loop_back_to": "gherkin_spec" },
         { "id": "impl", "type": "implementation", "name": "Implementation", "depends_on": ["gherkin_review"] },
         { "id": "code_review", "type": "review_gate", "gate_ref": "code_review", "depends_on": ["impl"], "loop_back_to": "impl" },
         { "id": "qa_review", "type": "review_gate", "gate_ref": "qa_review", "depends_on": ["impl"], "loop_back_to": "impl" },
         { "id": "security_review", "type": "review_gate", "gate_ref": "security_review", "depends_on": ["impl"], "loop_back_to": "impl" },
         { "id": "non_functional_review", "type": "review_gate", "gate_ref": "non_functional_review", "depends_on": ["impl"], "loop_back_to": "impl" },
         { "id": "accessibility_review", "type": "review_gate", "gate_ref": "accessibility_review", "depends_on": ["impl"], "loop_back_to": "impl" },
         { "id": "gherkin_test", "type": "gherkin_test", "name": "Gherkin Test Execution", "depends_on": ["code_review", "qa_review", "security_review", "non_functional_review", "accessibility_review"] },
         { "id": "test_review", "type": "review", "name": "Test Result Review", "depends_on": ["gherkin_test"], "loop_back_to": "impl" },
         { "id": "documentation", "type": "documentation", "name": "Documentation", "depends_on": ["gherkin_test"] },
         { "id": "documentation_review", "type": "review", "name": "Documentation Review", "depends_on": ["documentation"], "loop_back_to": "documentation" },
         { "id": "report", "type": "report", "name": "Test Report Creation (HTML/Captures)", "depends_on": ["test_review", "documentation_review"] },
         { "id": "report_review", "type": "review", "name": "Report Review", "depends_on": ["report"], "loop_back_to": "report" },
         { "id": "release_approval", "type": "approval_gate", "name": "Release Approval", "depends_on": ["report_review"] },
         { "id": "release", "type": "release", "name": "Release & Deployment (Manual)", "is_manual": true, "depends_on": ["release_approval"] }
       ]
     }
     ```
     When no documentation is needed, drop the `documentation`/`documentation_review` nodes and point `report`'s `depends_on` at `test_review` alone (as in the plain implementation+Gherkin case). When there's no `gherkin_test` node (Gherkin testing not needed) but documentation is still needed, depend `documentation` on whichever node(s) are last in the impl/review phase instead (the selected `review_gate` nodes), and have `release_approval` depend on `documentation_review` alongside them.
   - Save the patch to a temporary file and run (same `--language` rule as step 0/1 above -- include it only when `get-language-settings` reported `source: "none"`):
     ```bash
     graph-engine expand-graph "<ticketId>" --patch /tmp/patch.json [--language <code>]
     ```
   - Always include a `release` node in the patch, with `depends_on` pointing at a `release_approval` (`approval_gate`) node whose own `depends_on` correctly points at whichever node(s) are actually last in that ticket's flow. Invalid references or cycles will error out on the engine side. An `approval_gate` node needs no `is_manual: true` of its own -- the engine forces it regardless of what the patch says.

## 3. Execution loop (parallel execution via subagents)

1. Call `get-executable` to get the runnable nodes.
   - If it's empty and only a manual `approval_gate` node remains (still `TODO`, never yet judged), wait for a human decision that may come from either the terminal or the Web UI:
     1. Start a watcher with the Bash tool's `run_in_background`, passing every such gate's node id and no `--timeout`:
        ```bash
        graph-engine wait-node "<nodeId>" ["<nodeId>" ...]
        ```
        It polls the DB and exits as soon as any given node leaves `TODO`, printing `{"result":"changed","nodes":[{"id","status","rejection_reason"?}]}` (exit 0). Without `--timeout` it never times out; exit 1 means an error, e.g. `node <id> not found` when the gate was deleted while waiting.
     2. Tell the user, **in plain text**, what is being approved (the gate's name and the gist of the artifacts right before it) and that they can either answer approve/reject here in the terminal or approve/reject it in the Web UI. Then end your turn. **Do not use AskUserQuestion here**: it keeps the turn open, so the background watcher's completion notification could never resume the session when the decision is made in the Web UI.
     3. You resume either because the user answered in the terminal or because the `wait-node` notification arrived. Either way, **first run `get-ticket` and check the gate's current status** before calling anything:
        - Still `TODO` and the user answered in the terminal: record their answer with `graph-engine complete-node "<nodeId>" true`, or for a rejection `graph-engine complete-node "<nodeId>" false --reason "<text>"` (a rejection requires a reason; this call records it as the node's `rejection_reason` artifact). Then continue as for the matching case below.
        - Already `DONE` (approved in the Web UI): **do not call `complete-node`** -- go straight back to `get-executable` (this step 3 loop).
        - Already `REJECTED` (rejected in the Web UI): **do not call `complete-node`** -- go to step 4's triage.
        - The gate no longer exists (`wait-node` exited 1 with `not found`, e.g. the graph was rebuilt while waiting): **do not restart the watcher** -- go back to `get-executable` (this step 3 loop), which works from the ticket's current graph.
        - Still `TODO` and `wait-node` ended with exit 1 for another reason: re-check, then start the watcher again, or tell the user what happened if it keeps failing.
     4. A leftover notification can arrive after the session has already moved on (for example the user answered in the terminal first, and the watcher then saw that same change). Check with `get-ticket` as above; if the gate was already handled, do nothing -- never call `complete-node` or `reopen-nodes` twice for one decision. If the session moves on while a watcher is still running, you may stop that background task.
     Do not ask the user *which nodes to redo* -- that triage is this skill's job, not theirs (see step 4).
   - If it's empty and only a manual `release` node remains, ask the user to confirm the release/deployment has actually been carried out, then complete it.
   - If `blocked: true` and no manual node is waiting on a first decision, an approval_gate was likely rejected (or an iteration limit was reached) -- go to step 4 before reporting anything to the user.
2. For **each node returned, launch one subagent via the Agent tool using the `graph-node-agent` subagent type** (this plugin's default agent definition for graph-node work -- see `${CLAUDE_PLUGIN_ROOT}/agents/graph-node-agent.md`; fall back to a generic Agent-tool call with the same task content if that subagent type isn't available in your environment). When multiple nodes are returned at once (e.g. the parallel review gates), **issue multiple Agent calls within the same message so they truly run in parallel**. Launch a subagent the same way even for a single node (this session itself never does node work).
3. Give each subagent's task the ticket id and the node's id/type/name -- that's all `graph-node-agent` needs to load the ticket's context, fetch that node type's merged instructions (plugin default + any user/team extension content, resolved by the engine itself), do the work, save artifacts, and call `complete-node` on its own. This works the same way for the engine's built-in node types (`plan`, `investigation`, `gherkin_spec`, `implementation`, `review`, `review_gate`, `gherkin_test`, `documentation`, `report`, `release`) and for any custom node type this patch introduced -- a custom type simply has no plugin-default instruction layer, so the agent works from whatever user/team extension text exists for that exact type name (via `get-node-type-context`) or, absent that too, from the node's name.
4. Wait for every launched subagent to finish before moving on to the next `get-executable` call.

### Registering an artifact
```bash
graph-engine add-artifact "<ticketId>" "<nodeId>" "<artifactName>" "<text|gherkin|html|image|json>" "<contentOrFilePath>"
```
For `text`/`gherkin`/`json`, the last argument is always literal content, never a file path -- a path there gets stored verbatim as the content string, not the file's contents. Pipe a scratch file through stdin with `-` instead of passing its path.

### Node completion notification
```bash
graph-engine complete-node "<nodeId>" <true|false>
```

Repeat "3. Execution loop" until every automatic node is done or a manual node is reached.

A node showing `AWAITING FIX` in `get-ticket` is a review/review_gate that looped back and is waiting for its loop target's rework; it needs no special handling -- `get-executable` returns it again automatically once its prerequisites are `DONE`.

## 4. Triage after an approval_gate rejection

When `get-executable` is empty, `blocked: true`, and `get-ticket` shows an `approval_gate` node with status `REJECTED`, the engine is deliberately not deciding what happens next -- that judgment call is this skill's, made fresh each time by reading the ticket's actual content (there is no always-on process that does this; a process-ticket session parked at the gate with a `wait-node` watcher (step 3) resumes and triages a Web UI rejection automatically, but with no such session waiting, the rejection sits blocked until a process-ticket session like this one looks at it again):

1. Read the `REJECTED` node's `rejection_reason` artifact (its free-text reason).
2. Read the ticket's other artifacts (plan, reviews, implementation notes, Gherkin spec, test results, report, ...) to understand what the reason is actually pointing at.
3. Decide one of two outcomes:
   - **The reason implicates specific, already-completed work** (e.g. "the plan's section 2 design doesn't match the requirement" -> the `plan` node; "the report doesn't match what the tests actually found" -> the `gherkin_test` node): identify the node id(s) responsible and run
     ```bash
     graph-engine reopen-nodes "<ticketId>" "<nodeId1>,<nodeId2>,..."
     ```
     This resets exactly those nodes to `TODO` (plus anything downstream that already ran off them, via the engine's own forward-closure sweep -- you never need to enumerate downstream nodes yourself), bumps their iteration counts, and clears the ticket's blocked flag. If it errors because a node would exceed its `max_iterations`, report that to the user instead of retrying further. Resume the normal execution loop (step 3) afterward.
   - **The reason requires a requirements-level rethink** that no amount of redoing existing nodes would fix (e.g. disagreement with the ticket's premises, not with how it was executed): call nothing. Report the rejection reason and your assessment to the user and end the session -- the ticket stays blocked and the gate stays `REJECTED` until a human resolves it (e.g. via `refine-ticket` or further discussion).
4. Never ask the user to pick which nodes to reopen, and never reopen nodes without first reading the actual rejection reason and ticket artifacts -- both defeat the point of this being an automatic judgment rather than a manual one.
