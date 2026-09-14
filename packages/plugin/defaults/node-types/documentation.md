Update the in-repository documentation this ticket's change actually calls for
-- typically `README.md`, a `CHANGELOG`, or files under a `docs/` directory.
Do not perform a generic sweep of unrelated material, and do not create
documentation files or sections the ticket did not call for.

## Procedure

1. Read the ticket's plan, the implementation notes, and whichever Gherkin
   spec and test results this ticket has, so the documentation reflects what
   was actually built and tested rather than only what was planned.
2. Work out which documentation the completed change makes stale or newly
   necessary, and hold the scope to that.
3. Edit those files in the repository.

## Artifacts

Save one `text` artifact via `add-artifact` listing exactly which files were
touched and, for each, a short description of what changed. Give it a name
that describes its content.

## Notes

- That artifact must name every file and change precisely enough to verify
  without re-reading the diff from scratch -- it is what
  `documentation_review`, and later readers of this ticket, review against.
- Only documentation living in the repository is in scope. Documentation held
  in an external system (a wiki, a docs site, an internal knowledge base) is
  out of scope unless a user/team extension for this node type
  (`extensions/node-types/documentation.md` under the configured user/team
  directory -- see `README.md`) adds instructions for reaching it. Absent
  that, edit only files inside the repository.
