---
name: write-rfc
description: >-
  Write or revise Agent Pane RFCs with a consistent Markdown structure,
  clear prose, readable diagrams and reviewable references. For the GitHub
  issue automation, open or update the RFC pull request.
schemas:
  inputs:
    $ref: ./schemas/inputs.schema.json
---

# Write an RFC

An Agent Pane RFC is a design proposal reviewed as a Markdown file in a pull
request. Make it easy to scan, discuss and revise. This skill defines the
presentation; the issue and review discussion determine the proposal's scope
and substance.

## What done looks like

A focused proposal in `docs/rfcs/NNNN-short-title.md`, following the format
below. Read [RFC 0001](../../../docs/rfcs/0001-herdr-core-backend.md) and
[RFC 0002](../../../docs/rfcs/0002-windows-support.md) as examples of layout
and voice. Their technical choices, length and rollout plans are specific to
those proposals, not requirements for new RFCs.

For a GitHub issue automation run, read [Automation](references/automation.md)
for inputs, ownership, stopping conditions and pull request handling. It
contains the workflow, not additional requirements for the design.

## Document shape

Use a zero-padded, four-digit RFC number and a short, lowercase, hyphenated
filename. Keep the title descriptive. Start with this header, substituting
verified issue and author details:

```markdown
# RFC NNNN: Title

- **Status:** Proposed
- **Issue:** #N
- **Author:** Tessl, for @owner
```

Status is Proposed while the RFC is under review, Accepted once the
maintainer merges it as accepted, and Implemented once the work it describes
has merged. The pull request that completes it sets Implemented and adds
`- **Implemented in:** #<pr>` after the Status line, listing every pull
request that delivered part of it.

Use these second-level headings in this order so readers can find the same
kind of information across RFCs:

```markdown
## Summary
## Motivation
## Guide-level explanation
## Reference-level explanation
## Drawbacks
## Rationale and alternatives
## Prior art
## Unresolved questions
## Future possibilities
```

Keep a section brief when it has little to say; use "None" when it does not
apply. These headings organize the proposal, not a checklist of topics to
invent. An optional `## Rollout` may follow Future possibilities when useful;
its contents and delivery sequence depend on the proposal.

Use third-level headings to divide longer sections by subject. Use fourth-level
headings sparingly and do not skip levels. Subsections should describe this
proposal, rather than copy the component names or topic lists of an older RFC.

## Prose and emphasis

Lead with a short Summary that stands on its own. Keep paragraphs focused on
one point, with the claim before its explanation. Let the guide-level section
read in user or contributor terms and the reference-level section carry the
technical detail; link between them instead of repeating whole passages.

Use the [README glossary](../../../README.md#terminology) consistently.
Name headings for their contents. Write in plain, factual language, with
concrete subjects and verbs. Distinguish current behavior, proposed behavior
and unresolved choices in the wording. Avoid slogans, sales language and
claims of agreement that the discussion does not support. Do not use em or en
dashes.

Use prose for reasoning, bullets for parallel items and numbered lists where
order matters. Reserve bold for short labels or a point that needs emphasis;
do not turn whole paragraphs into bold text. Scale the length to the decision
being reviewed, not to the longest example RFC.

## Tables, examples and diagrams

Use tables when readers need to compare the same properties across options,
components or checks. Keep cells short, give columns descriptive labels and
put extended reasoning in nearby prose. Do not turn a narrative into a table
just to make it look structured.

Use inline code for paths, symbols, commands and literal values. Fence code,
configuration and command examples with an appropriate language tag. Include
only enough of an example to clarify the proposal and label illustrative
values so they cannot be mistaken for existing interfaces.

Use Mermaid when a diagram clarifies relationships, message flow or lifecycle.
Choose a flowchart, sequence diagram or state diagram to suit the explanation.
Keep diagrams small enough to read at normal size, use the same names as the
prose and label meaningful connections. Place each diagram beside the text it
explains. A proposal that is clearer in prose needs no diagram.

## References and review

Link repository files with relative Markdown links from the RFC's location.
Link to section anchors for navigation within the proposal. Cite external
sources beside the claims they support, using descriptive link text. Keep
references close to the relevant reasoning rather than collecting unexplained
URLs. Verify current-behavior claims against the base branch; older RFCs are
design records, not proof of today's implementation.

Before handing off, read the document as a reviewer would: check the heading
hierarchy, paragraph flow, table readability, links and consistency between
text and diagrams. Preview Markdown and Mermaid where available. Revise the
RFC itself as review settles points so readers need not reconstruct the
proposal from comment threads.

## Invariants

1. The RFC pull request contains only the proposal and its diagram assets.
2. A new or revised proposal stays Proposed until the maintainer decides.
3. The issue and human feedback determine requirements. Do not turn an
   example RFC's architecture, implementation detail or number of delivery
   pull requests into a rule for another proposal.
4. State assumptions and open questions honestly; do not fill sections with
   invented facts or decisions.

## Judgment

Choose the amount of detail that makes this proposal reviewable. Ask the owner
only when missing information prevents a coherent proposal; otherwise make
uncertainty visible in the relevant section. The format should help a reader
understand and evaluate the design, while leaving the author room to design it.
