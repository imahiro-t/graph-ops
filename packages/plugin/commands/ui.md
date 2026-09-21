---
description: Opens the local GraphOps Web UI in your browser for the current project (auto-starting the UI server if needed).
---

# /ui

Run the following command and report its result to the user:

```bash
graph-engine ui
```

This single command handles all three cases on its own -- there is nothing else for you to figure out or ask the user about beforehand:

- If the current directory is (or is under) a project's local path -- this environment's `projectPaths` in `$HOME/.graph-ops/config.json`; the deepest match wins -- it switches the Web UI to that project and opens its page in the default browser.
- If no project's local path covers the current directory, it opens the Web UI with the project setup dialog already showing for the current directory, where the user either creates a new project or chooses an existing one from the DB; either way the directory is saved as that project's local path.
- If the local Web UI server isn't running yet, it starts it in the background first, then does one of the above.

After running it:
- On success, the command prints the URL it opened -- just tell the user the UI has been opened (mention the URL only if it seems useful, e.g. if you suspect the browser didn't actually open).
- If it exits non-zero, that means the UI server itself failed to start (e.g. its port is already in use by something else) -- show the user the command's error output verbatim rather than guessing at the cause; do not retry automatically.
- A warning printed to stderr about the browser not opening automatically is not a failure -- the command still succeeded in getting the server ready. Give the user the printed URL to open by hand in that case.
