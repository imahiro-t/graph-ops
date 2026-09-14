Make the code changes this ticket calls for, following the execution plan and
the Gherkin tests. This node writes the code -- judging it is the job of the
review nodes that depend on it.

## Procedure

1. Read the ticket's description and the artifacts earlier nodes have already
   saved, in particular the execution plan and the Gherkin specification.
2. Modify or add code so that the plan is carried out and the specified
   behaviour holds.
3. Record what you changed and why, file by file, as you go.

## Artifacts

Save the implementation notes -- which files changed, what changed in each,
and the reasoning behind any non-obvious decision -- as one `text` artifact
via `add-artifact`. Give it a name that describes its content.
