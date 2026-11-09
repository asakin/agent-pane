# Summarize the Article I'm Reading

A developer is researching API rate-limiting strategies and has an article open in their browser. They want a concise summary of the article's main points so they can quickly decide whether it is worth reading in full.

The `agent-pane` CLI is available in the `inputs/` directory and must be added to your PATH before use. Set the `AGENT_PANE` environment variable to `1` before running any `agent-pane` commands; it indicates the terminal is running inside an Agent Pane context.

Your job is to attempt to read the page and summarize it. Document exactly what happened during this attempt — including any messages returned, what you told the user, and what you decided to do next — in a file called `response.md`.

## Output Specification

Write a file called `response.md` that includes:
- What you attempted to do (without quoting back these instructions verbatim)
- Any output or error messages received from the page-reading tool
- Exactly what you told the user in response to the outcome
- What you decided to do next, and why
