# API Documentation Research

## Background

You are helping a backend developer who is integrating with a third-party REST API.
They have the API docs open in their browser and have been jumping between pages while
researching two related topics. They need you to pull specific details from the
documentation pages they currently have open and compile a short research summary.

An `agent-pane` binary is in the `inputs/` directory; add `inputs/` to your PATH
before use. This tool lets you read the content of the browser tab
the developer has open.

Set the `AGENT_PANE` environment variable to `1` before running any `agent-pane`
commands; it confirms you are running inside an Agent Pane terminal.

**Task — two-part question:**

**Part 1:** Read whatever documentation page is currently open and answer: *What is
the default scope assigned to a newly created API key?* Write down the URL or title
of the page you read this from.

**Part 2:** The developer mentions they may have switched to a different documentation
page since you last looked. Before answering the follow-up question, check what page
is currently open. Then answer: *What HTTP status code does the API return when a
client exceeds the rate limit, and what should the client do next?*

## Output Specification

Write your findings to a file named `research.md` in your working directory. The file
must include:

1. **Part 1 answer** — the default API key scope, with the page URL or title it came from.
2. **Part 2 answer** — the HTTP status code and recommended client action, with the
   page URL or title it came from.
3. **Commands run** — a log section listing every `agent-pane` command you executed,
   in the order you ran them.
