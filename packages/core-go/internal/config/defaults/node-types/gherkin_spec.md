Write the Gherkin specification for this ticket -- the `Feature` and
`Scenario` blocks describing the behaviour to be built and later tested.

## Procedure

1. Read the ticket's description and any artifacts earlier nodes have already
   saved, in particular the execution plan.
2. Write the specification as Gherkin, with a `Feature` and the `Scenario`
   blocks covering the behaviour the ticket asks for.
3. Keep the scenario steps in Gherkin's `Given` / `When` / `Then` form, so the
   node that later runs the tests can work against them.

## Artifacts

Save the specification as one `gherkin` artifact via `add-artifact`. Give it a
name that describes its content.
