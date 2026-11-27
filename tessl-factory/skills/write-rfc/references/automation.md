# RFC automation

Read this when running the GitHub issue loop. The issue, branch and pull
request are the record between rounds. The **owner** is the first assignee,
or the issue author when unassigned.

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

## GitHub

Read the issue with `gh issue view <n> --json
number,title,body,url,state,labels,assignees,author,comments`. Read it again
before every write.

Writes you make: `gh issue comment <n> --body-file <file outside the clone>`.

In this skill, `<id>` means `issue-<n>`, and `#<n>` is how the pull request
and comments name the issue.

## What done looks like

1. The branch `tessl/<id>` adds `docs/rfcs/<NNNN>-<short-title>.md` (or, for a
   revision, changes it), plus any diagram assets it needs, and changes
   nothing else. There is no RFC index to update.
2. The RFC has the template's header with **Status:** Proposed, **Issue:**
   #<n> and **Author:** Tessl, for @<owner login>, and the format described
   in [the skill](../SKILL.md).
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
   number and sets **Status:** Proposed. An explicit request to revise an
   existing RFC keeps its number; a new proposal that amends an accepted design gets a new number and links it.
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
2. Every comment you post ends with `<!-- tessl-factory -->`. Use the marker
   together with the comment author to recognize your earlier comments; a
   person may quote the marker.
3. You never post the same thing twice. Look for it before you post.
4. You never close, reopen, relabel or reassign the issue. The pull request's
   `Closes` line closes it on merge.
5. One branch per issue, one open pull request per issue. Reuse an existing
   open pull request; a requested revision after merge may have a new one.
6. You never merge, enable auto-merge, or change a pull request that is not
   yours.
7. You push after every commit. A run can end at any moment.
8. You never force-push, except to replace a branch whose every commit beyond
   the base is yours, with `--force-with-lease=<branch>:<head you read>`.

### The RFC

9. The pull request adds or changes only the RFC file and any
   diagram the RFC needs. No code, tests, CI, issues or other documents.
10. The RFC proposes; it never records a decision for the maintainer. Status
    stays Proposed, and nothing in it says the maintainer decided or agreed
    to anything they did not write themselves. The maintainer accepts an RFC
    by merging it with Status: Accepted.
11. Claims about current repository behavior are checked against the base
    branch and cite the relevant file, function or command. Distinguish
    proposed behavior from current behavior. Claims about another project
    name their source.
12. You never invent an identity, an id or a branch name.

## Doing the work

- **Base branch:** a `Base: <branch>` line in the issue, else the
  repository's default branch.
- **Branch:** `git ls-remote --exit-code --heads origin tessl/<id>`. A branch
  that exists is most likely an earlier round's: read its diff and either
  build on it or replace it from the base, and say which in the pull request.
- **Number:** the next number after the highest in `docs/rfcs/` on the base and in
  open pull requests other than this issue's that add a file to `docs/rfcs/`
  (`gh pr list --state open --json number,headRefName,files`). A revision
  keeps its RFC's number.
- **Research:** read the code the design builds on, so what the RFC says
  about it is accurate. When the issue asks how others solved the problem, or
  the proposal follows another project, look it up and cite it.
- **Checks:** check the diff, relative links and heading hierarchy against
  the skill format, and preview Markdown and Mermaid rendering where available.
- **The pull request:** `gh label create tessl --force`, then
  `gh pr create --base <base> --label tessl --title "#<n>: RFC <NNNN>: <title>"
  --body-file <file outside the clone>`. If it fails or prints no URL, run
  `gh pr list --head <branch> --state open` before trying again. The body: a
  few lines on what the RFC proposes and the questions it leaves open;
  `Closes #<n>` on its own line; and
  `<!-- tessl-factory:issue-label: tessl-rfc -->` on its own line.
- **Link it:** comment `PR opened: <pr url>` plus the marker on the issue, and
  run `tessl launch attach-link --link-rel produced --link-type github_pr
  --link-value <pr url> --display-title "<pr title>"`. A failed link is a
  warning.
- **The run summary:** one or two sentences on what this round did.

## Environment facts

- This round started because @asakin added the trigger label, or commented on
  an open issue that has it. Nothing else starts a round: a comment from
  anyone else, or on the pull request, does not.
- The pull request's `tessl` label and `tessl/` branch are what start the
  shepherd automation on review comments and failed checks; your rounds do
  not answer them. The marker's `tessl-rfc` keeps the shepherd working: it
  stops when the issue no longer has that label.
