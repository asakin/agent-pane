# Web Research Assistant: Browser Page Summary

## Problem/Feature Description

A developer on your team is in the middle of migrating some legacy Python 2 async code to modern Python 3 patterns. They've been reading through the official Python asyncio documentation in their browser and want your help understanding a subtlety they ran into. Rather than copy-pasting large sections, they want you to look at the page they have open and answer their question directly from it.

Their request: "I'm looking at the Python asyncio documentation page right now. Can you summarize the key differences between `asyncio.gather()` and `asyncio.TaskGroup`? I want to understand when I should prefer one over the other."

Before attempting to access or read any browser content, check what environment you're running in and what capabilities are available to you. Then respond based on what you find — either by reading the page and answering, or by clearly explaining what you found and why you can or cannot help this way.

## Output Specification

Write a file named `status.md` in your working directory that documents:

1. The exact command you ran to check the environment, and its full output
2. What you concluded from that check about your current environment
3. What you communicated to the user about whether you can read their browser page directly
4. Whether you attempted any browser-reading commands, and why or why not
