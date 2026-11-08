# Agent Pane RFCs

An RFC (request for comments) is a reviewed design proposal for a substantial change to Agent Pane's product or architecture. The process is a lightweight version of the ones used by [Rust](https://github.com/rust-lang/rfcs), [Ember](https://github.com/emberjs/rfcs) and [Vue](https://github.com/vuejs/rfcs): the proposal is a Markdown file in this folder, reviewed in a pull request, and kept in the repository once decided.

## When to write one

Write an RFC before implementing a change that:

- replaces or adds a backend, process or protocol between the extension, the native host and the terminal;
- changes a trust boundary: native messaging, page reads and their approvals, peer-credential checks, extension permissions;
- changes what the installer puts on a machine, or how Agent Pane is distributed and reviewed;
- adds a user-facing concept that other features will build on.

Bug fixes, refactors that keep behavior, new agent definitions and documentation fixes do not need one. When unsure, open an issue and ask.

## Process

1. Copy the [template](#template) to `rfcs/NNNN-short-title.md`, where `NNNN` is the next unused number, zero padded.
2. Open a pull request with only the RFC (and any diagrams it needs). The pull request is where review happens; change the file to answer review rather than leaving decisions only in comments.
3. A maintainer decides: merge with **Status: Accepted**, merge with **Status: Rejected** to keep the reasoning on record, or close the pull request.
4. Implementation happens in separate issues and pull requests that link the RFC. An RFC describes intent; once code lands, the [README](../README.md) and code comments describe current behavior, and they win where the two differ.
5. A later change to an accepted design gets a new RFC that names the one it amends or replaces, and the older RFC's status line is updated to point at it.

Each RFC starts with a header:

```markdown
# RFC NNNN: Title

- **Status:** Proposed | Accepted | Rejected | Superseded by RFC NNNN
- **Issue:** #N
- **Author:** @handle
```

## Template

Use these sections, adapted from the Rust RFC template. Leave a section in with "None" rather than removing it.

- **Summary:** one paragraph a reviewer can read on its own.
- **Motivation:** the problem, who has it, and what outcome the change should produce.
- **Guide-level explanation:** the change as a user or contributor meets it, in the [README's terms](../README.md#terminology).
- **Reference-level explanation:** the design in enough detail to implement and review: components, protocols, data, failure modes, security.
- **Drawbacks:** why not to do this.
- **Rationale and alternatives:** why this design, what else was considered, and the cost of doing nothing.
- **Prior art:** related designs in this project and elsewhere, and what they teach.
- **Unresolved questions:** what review or prototyping must settle before or during implementation.
- **Future possibilities:** where this could lead, kept out of the proposal's scope.

## Index

| RFC | Title | Status |
| --- | --- | --- |
| [0001](0001-herdr-core-backend.md) | Herdr core as Agent Pane's terminal backend | Proposed |
