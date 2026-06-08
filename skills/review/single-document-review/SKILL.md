---
name: single-document-review
description: Use this skill when the user requests to review, analyze, critique, or summarize a SINGLE academic paper, general document, internal report, proposal, or web article. Supports comprehensive structured reviews covering methodology/logic assessment, strengths, weaknesses, and constructive feedback. Retrieves content using native tools (`url_fetch`, `kb_search`, `arxiv_search`) and outputs the analysis directly in the chat.
---

# Single Document & Paper Review Skill

## Overview

This skill produces structured, professional-grade analyses of single academic papers or general business/technical documents. It adapts established academic peer-review standards to evaluate both scientific publications and corporate reports (e.g., whitepapers, strategic memos, design docs). 

The review covers **executive summary, strengths, weaknesses, methodology/logic assessment, contextual positioning, and actionable recommendations** — all grounded in evidence from the text itself.

## When to Use This Skill

**Always load this skill when:**
- User provides a single URL (arXiv, blog, documentation) or file path and asks to "review", "analyze", or "summarize" it.
- User queries a specific document from the Knowledge Base (`kb_search`) for detailed critique.
- User wants to understand the strengths, weaknesses, and validity of a specific study, proposal, or report.
- User requests a peer-review-style evaluation of their own drafted document.

*Note: If the user asks to synthesize or compare MULTIPLE documents, use the multi-document systematic review skill instead.*

## Available Tools & Acquisition

Depending on how the user provides the document, use the appropriate native tool to ingest the text into your context:
- **Academic Papers:** Use `url_fetch` on the HTML version (e.g., `https://ar5iv.labs.arxiv.org/html/<id>`) or use `arxiv_search` for metadata.
- **Web Articles:** Use `url_fetch`.
- **Internal Knowledge:** Use `kb_search` or `kb_keyword_search` to pull the specific document from the Knowledge Base.

## Review Methodology (Internal Processing)

Once the document is loaded into your context, perform a deep reading pass using your internal attention. 

### Phase 1: Comprehension & Metadata Extraction
Identify:
1. **Title & Creators**: Authors, Departments, or Organizations.
2. **Document Type**: Is this an Empirical Paper, Theoretical Proof, Business Proposal, Technical Design Doc, or Quarterly Report?
3. **Core Claims**: What are the 2-3 main arguments or contributions?

### Phase 2: Critical Analysis & Context
1. **Contextualization**: If necessary, perform a quick `kb_search` (for internal docs) or `arxiv_search` (for academic docs) to see if this document aligns with or contradicts existing knowledge.
2. **Methodology / Logic Assessment**: Evaluate based on the document type:
   - *For Academic/Technical*: Assess Soundness, Novelty, Reproducibility, and Statistical/Data Rigor.
   - *For Business/General*: Assess Clarity, Strategic Alignment, Feasibility, and Evidence Rigor (are claims backed by data?).

### Phase 3: Strengths and Weaknesses Extraction
For each strength or weakness, explicitly note:
- **What**: The specific observation.
- **Where**: Section/figure/table reference.
- **Why it matters**: Impact on the document's overall reliability or utility.

## Output Format

Do not attempt to save the review to a file. **Directly output the complete review in Markdown format in the chat.** Use the following unified template, adapting the fields based on whether the document is academic or general:

```markdown
# Document Review: [Title]

## Metadata
- **Creator(s) / Author(s)**: [Name or Department]
- **Type**: [Academic Paper / Business Proposal / Technical Spec / etc.]
- **Date / Context**: [Year or specific context]

## Executive Summary
[2-3 paragraph summary of the document's core objective, approach, and main findings/proposals. State your overall assessment upfront: what it does well, where it falls short, and its overall significance.]

## Key Claims & Contributions
1. [First major claim/contribution — one sentence]
2. [Second major claim/contribution — one sentence]
3. [Additional claims if any]

## Strengths
### S1: [Concise strength title]
[Detailed explanation with specific references to sections. Explain WHY this is a strength.]

### S2: [Concise strength title]
[...]

## Weaknesses & Limitations
### W1: [Concise weakness title]
[Detailed explanation with specific references. Explain the impact of this weakness on the document's claims. Suggest how it could be addressed.]

### W2: [Concise weakness title]
[...]

## Rigor & Logic Assessment
| Criterion | Rating (1-5) | Assessment Justification |
|-----------|:---:|------------|
| Clarity & Structure | X | [Brief justification] |
| Evidence / Data Quality | X | [Brief justification] |
| Methodology / Feasibility | X | [Brief justification] |
| Contextual Alignment | X | [Brief justification, e.g., how it fits into the broader field or company strategy] |

*(Note: Adjust the criteria names slightly if reviewing a highly theoretical physics paper vs. a corporate marketing plan).*

## Questions for the Creators
1. [Specific question that would clarify a concern or ambiguity]
2. [Question about methodology choices, alternative approaches, or implementation details]

## Actionable Recommendations
**Overall Assessment**: [Strongly Endorse / Endorse with Revisions / Neutral / Needs Major Rework]

**Specific Suggestions for Improvement:**
1. [Actionable, constructive suggestion to fix W1]
2. [Actionable, constructive suggestion to fix W2]
3. [Minor formatting/typo fixes if applicable]
```

## Review Principles

- **Constructive Criticism:** Always suggest how to fix a problem. Don't just point out flaws; propose solutions.
- **Be Specific:** Reference exact sections, claims, or data points.
  - ❌ "The report lacks detail."
  - ✅ "Section 3 claims a 20% growth rate but provides no historical data or citation to back this projection."
- **Evaluate on its Own Merits:** Do not penalize a business memo for not being an academic paper, or vice versa. Judge it by the standards of its intended format.
- **Maintain Objectivity:** Ensure your tone is professional, respectful, and strictly analytical.