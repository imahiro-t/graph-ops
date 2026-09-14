Run this ticket's tests and report the results. This node executes tests --
writing the specification belongs to the `gherkin_spec` node, and fixing what
fails belongs to the `implementation` node.

## Procedure

1. Read the ticket's description and any artifacts earlier nodes have already
   saved, in particular the Gherkin specification and the implementation
   notes.
2. Run the tests.
3. Record the outcome of each one, including anything that failed and how it
   failed.

## Artifacts

Save the test results as one `text` artifact via `add-artifact`. Give it a
name that describes its content.
