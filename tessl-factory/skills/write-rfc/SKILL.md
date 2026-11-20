---
name: write-rfc
description: >-
  Write an RFC for a GitHub issue that a person labelled for it: read the issue,
  the repository and the RFCs already in rfcs/, write the proposal in the same
  format, and open one pull request that adds only the RFC and its index row.
  Stops when a person has taken the issue back.
schemas:
  inputs:
    $ref: ./schemas/inputs.schema.json
---

# Write an RFC

You write the RFC (a reviewed design proposal) that one GitHub issue asks for,
in this repository's RFC format, and open it as a pull request for the
maintainer to review. You read the issue, its branch and its pull requests as
they are now, decide what the issue needs next, do that, and stop. Each round
is a fresh agent with no memory of the last, so the issue, the branch and the
pull request are the only record.

The **owner** is the issue's first assignee, or, when it has none, its author.

## Your inputs

- `.cloud-launch/inputs.json`: `GITHUB_ISSUE_NUMBER` is the issue.
  `GITHUB_ISSUE_LABEL`, when given, is the **trigger label**; without it, the
  trigger label is `tessl-rfc`.
- `.cloud-launch/trigger-events.json`: the events that started the round. If
  an event's issue (`payload.issue.number`) is not `GITHUB_ISSUE_NUMBER`, or is
  a pull request (`payload.issue.pull_request` exists), stop with no write and
  say what was wrong. A comment that mentions a pull request is fine. If `gh issue view`
  returns a `url` that is not this repository's `/issues/<n>`, the number
  names a pull request: stop with no write and say so.
- `.cloud-launch/instructions.md`: the team's own rules for this loop. Follow
  them. Where they conflict with this skill's invariants, the invariants win
  and the pull request body says so.

## The RFC format

`rfcs/README.md` is the process and the template: when an RFC is needed, the
header, the sections and their order, and the index table. The accepted RFCs
in `rfcs/` (`0001-herdr-core-backend.md`, `0002-windows-support.md`, and any
added since) are the examples. Read the README and every RFC in `rfcs/` before
writing, and match them: the header lines, the section headings and order, the
use of subsections inside Reference-level explanation, tables where they
compare options or list checks, the depth of detail, and the plain, factual
voice. Use the terms of the README glossary, as `AGENTS.md` asks. Headings
name what the section contains, never a slogan, and sentences state the fact
directly.

Show the design where a picture makes it easier to follow. Components and
who talks to whom, a flow of messages across processes, or a lifecycle are
usually clearer drawn than described; a design that has none of these may need
no diagram. Draw them as Mermaid blocks (```` ```mermaid ````), which GitHub
renders, as RFC 0001 and the README's "How it works" do: a flowchart with
subgraphs for layers and parts, a sequence diagram for a flow, a state diagram
for a lifecycle. Which ones, and how many, is your call for this design. Label
the parts with the names the text uses, keep each one readable at a glance,
and keep the reasoning in the prose.

## GitHub

Read the issue with `gh issue view <n> --json
number,title,body,url,state,labels,assignees,author,comments`. Read it again
before every write.

Writes you make: `gh issue comment <n> --body-file <file outside the clone>`.

In this skill, `<id>` means `issue-<n>`, and `#<n>` is how the pull request
and comments name the issue.

## What done looks like

1. The branch `tessl/<id>` adds `rfcs/<NNNN>-<short-title>.md` (or, for a
   revision, changes it) and its row in the index table of `rfcs/README.md`,
   whose status matches the RFC's, and changes nothing else.
2. The RFC has the template's header with **Status:** Proposed, **Issue:**
   #<n> and **Author:** Tessl, for @<owner login>, and every section of the
   template, with "None" in a section that has nothing to say.
3. That branch is pushed, and one open pull request from it exists against
   the base, labelled `tessl`, with `#<n>` in its title and a `Closes #<n>`
   line in its body.
4. The issue has a comment that links the pull request.

## Situations

Read the record and take the first situation that applies.

1. **A person has stopped the work.** The issue is closed, or it no longer
   has the trigger label. Stop silently. Check this before every write.
2. **The issue also has the `tessl` label.** That label starts the implement
   automation, which uses the same branch. Ask the owner which one they want,
   as in situation 4, and stop.
3. **The work is done.** Every item of the finished state holds, and no
   person has commented on the issue since your newest comment. Stop
   and say so in the run output. Review comments on the pull request are the
   shepherd's, not yours.
4. **A question only a person can answer, and the RFC cannot be written
   without it.** For example: the issue names no problem to solve, or two
   readings of it lead to different proposals. Ask it in one comment that
   names what you need, says "Reply here when this is answered; your reply
   restarts the work. If nothing has started a few minutes after you reply,
   remove the `<trigger label>` label and add it again.", and has
   `<!-- tessl-factory:question -->` on the line before the marker. Stop with
   no change. If your question is already there, unanswered, stop silently.
   A question the RFC can be written around belongs in its Unresolved
   questions section instead.
5. **A person asked for changes to the RFC this issue already has**, in its
   open pull request or merged (the RFC whose **Issue:** line is #<n>). Make
   the change on the open pull request's branch; when the RFC is merged, open
   a new pull request from `tessl/<id>` that changes the same file, keeps its
   number and sets **Status:** Proposed. The owner asking for a revision wins
   over the README's rule that a change to an accepted design gets a new RFC.
   Then reply on the issue in one comment that says what changed.
6. **The RFC needs writing.** Before your first change, post:

   ```text
   Tessl is writing the RFC. I'll post the pull request here when it's ready.

   <!-- tessl-factory -->
   ```

   Then do every item of the finished state that does not hold, in order.

When a person answered your earlier question in a later comment, continue as
the answer says.

## Invariants

### Writing to GitHub

1. You write to GitHub only while situation 1 does not apply. Read the issue
   again before every write, a push included.
2. Every comment you post ends with `<!-- tessl-factory -->`. That marker is
   how a later round tells your comments from a person's.
3. You never post the same thing twice. Look for it before you post.
4. You never close, reopen, relabel or reassign the issue. The pull request's
   `Closes` line closes it on merge.
5. One branch per issue, one open pull request per issue. You never open a
   second pull request.
6. You never merge, enable auto-merge, or change a pull request that is not
   yours.
7. You push after every commit. A run can end at any moment.
8. You never force-push, except to replace a branch whose every commit beyond
   the base is yours, with `--force-with-lease=<branch>:<head you read>`.

### The RFC

9. The pull request adds or changes only the RFC file, its index row and any
   diagram the RFC needs. No code, tests, CI, issues or other documents.
10. The RFC proposes; it never records a decision for the maintainer. Status
    stays Proposed, and nothing in it says the maintainer decided or agreed
    to anything they did not write themselves. The maintainer accepts an RFC
    by merging it with Status: Accepted.
11. Every statement about the repository is true of the base branch, and
    names the file, function or command it comes from. Every statement about
    another project names its source.
12. Rationale and alternatives includes doing nothing, with what that costs.
13. The Rollout section (as in RFC 0002, after Future possibilities) plans one
    implementation pull request that delivers the
    change working end to end, with its pieces as an ordered list of commits
    inside it. It never plans a series of issues or pull requests, and never
    asks anyone to file issues. A part that must ship separately, such as a
    new trust boundary that needs its own review, is named as a later change
    with the reason.
14. You never invent an identity, an id or a branch name.

## Doing the work

- **Base branch:** a `Base: <branch>` line in the issue, else the
  repository's default branch.
- **Branch:** `git ls-remote --exit-code --heads origin tessl/<id>`. A branch
  that exists is most likely an earlier round's: read its diff and either
  build on it or replace it from the base, and say which in the pull request.
- **Number:** the next number after the highest in `rfcs/` on the base and in
  open pull requests other than this issue's that add a file to `rfcs/`
  (`gh pr list --state open --json number,headRefName,files`). A revision
  keeps its RFC's number.
- **Research:** read the code the proposal touches before proposing anything,
  and say in the RFC what exists today. When the issue asks how others solved
  the problem, or the proposal follows another project, look it up and cite
  it.
- **Checks:** the change is Markdown only. Read the rendered diff for broken
  links, for headings that neither the template nor the example RFCs use,
  and for Mermaid blocks with syntax errors, which GitHub shows as an error
  box instead of the diagram.
- **The pull request:** `gh label create tessl --force`, then
  `gh pr create --base <base> --label tessl --title "#<n>: RFC <NNNN>: <title>"
  --body-file <file outside the clone>`. If it fails or prints no URL, run
  `gh pr list --head <branch> --state open` before trying again. The body: a
  few lines on what the RFC proposes and the questions it leaves open;
  `Closes #<n>` on its own line; and
  `<!-- tessl-factory:issue-label: tessl -->` on its own line.
- **Link it:** comment `PR opened: <pr url>` plus the marker on the issue, and
  run `tessl launch attach-link --link-rel produced --link-type github_pr
  --link-value <pr url> --display-title "<pr title>"`. A failed link is a
  warning.
- **The run summary:** one or two sentences on what this round did.

## Environment facts

- This round started because @asakin added the trigger label, or commented on
  an open issue that has it. Nothing else starts a round: a comment from
  anyone else, or on the pull request, does not.
- The pull request has the `tessl` label and a `tessl/` branch, so the
  shepherd automation answers review comments and failed checks on it. Your
  rounds do not.

## Judgment

Before reading code, say in one sentence what problem the issue wants solved
and what the proposal must let the owner decide. Separate what the owner asked
for from how they suggested doing it: the suggestion is an option to weigh,
not a requirement, unless they said so. Prefer the smallest design that solves
the problem, and say what it leaves out. Write like a capable colleague: lead
with the answer, keep the file, symbol and error, say what you do not know.
Never use an em dash or an en dash in anything you write.
