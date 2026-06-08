---
name: systematic-document-and-literature-review
description: Use this skill when the user wants a systematic review, thematic synthesis, or cross-document analysis across multiple academic papers OR provided general documents (reports, internal memos, web articles). Retrieves sources via arXiv, internal Knowledge Base (KB), or direct URLs. Analyzes the content within the current context and outputs a fully structured synthesis report directly in the chat.
---

# Systematic Document & Literature Review Skill

## Overview

This skill produces a structured **systematic review and synthesis** across multiple academic papers or general documents. Given a topic query or a list of specific files/URLs, it gathers the source material, analyzes the content (objectives, methodology, key findings, limitations) sequentially, synthesizes themes, and outputs a final structured report directly to the user.

**Distinct from single-document review:** This skill does breadth-first synthesis across many sources. If the user hands you exactly one document or one paper URL and asks "review this", route to a standard reading or single-document review skill instead.

## When to Use This Skill

Use this skill when the user wants any of the following:

- A literature survey on an academic topic ("survey transformer attention variants")
- A thematic synthesis across internal documents ("synthesize the last 5 quarterly reports from the KB and find common trends")
- A cross-document comparison ("compare the methodologies across these provided architecture design URLs")
- An overview of trends across a set of files, URLs, or an arXiv time window

Do **not** use this skill when:

- The user provides exactly one document/paper and asks to review it.
- The user asks a factual question that does not require synthesizing multiple sources.

## Workflow

The workflow has four phases. Follow them in order.

### Phase 1: Plan

Before doing any retrieval, confirm the following with the user. If any of these are unclear, ask **one** clarifying question that covers the missing pieces. 

- **Source Material**: Are we searching an academic database (`arxiv_search`), an internal Knowledge Base (`kb_search`), or reviewing specific URLs (`url_fetch`)?
- **Scope**: How many sources total? **(Limit to max 15-20 sources to prevent context overflow)**.
- **Output Format**: APA, IEEE, BibTeX, or Standard Professional Report (default for non-academic documents).

### Phase 2: Acquire Sources

Depending on the Source Material established in Phase 1, follow the appropriate acquisition path:

#### Path A: Academic Literature (arXiv)
Use the native `arxiv_search` tool. 
- Extract 2-3 core keywords from the user's prompt. Do not use overly long phrases.
- Use relevance sorting unless chronological order is strictly requested.

#### Path B: Internal Documents (Knowledge Base)
Use `kb_search` or `kb_keyword_search` to find relevant internal documents. 
- Gather the returned nodes. If context is missing, use `kb_get_window_nodes` or `kb_get_parent_node` to ensure sufficient text for the subagents.

#### Path C: Provided URLs
If the user provides a list of links or file paths:
- Verify the list (cap at 50). 
- Use `url_fetch` (for web links) to ingest the content.

### Phase 3: Analyze & Synthesize (Internal Processing)

Once you have ingested the source texts into your context, carefully analyze them. You must mentally or explicitly (in your thinking process) extract the following for *each* source:
- Primary objective
- Approach or methodology
- Key findings
- Limitations or next steps

**Cross-source synthesis**: Then, look across all analyzed sources to identify:
- **Themes**: 3-5 recurring directions, topics, or problem framings.
- **Convergences**: Findings or arguments that multiple documents agree on.
- **Disagreements**: Where sources reach different conclusions.
- **Gaps**: What the collective set of documents does not address.

### Phase 4: Output Final Report

Do not attempt to save to a file. **Directly output the final synthesized report in the chat.** Use markdown formatting. If the user requested a Standard Professional Report, strictly use the following structure:

```markdown
# Systematic Review: [Topic Name]

## 1. Executive Summary
[A high-level 3-5 sentence summary of the entire review, highlighting the most critical insights.]

## 2. Thematic Synthesis
### Themes
- **[Theme 1]**: [Explanation based on sources]
- **[Theme 2]**: [Explanation based on sources]
### Convergences & Disagreements
[Where do the documents align? Where do they conflict?]
### Knowledge Gaps
[What is missing from the current literature/documents?]

## 3. Source Annotations
[Briefly list each analyzed source with its core objective and key finding. E.g.:]
* **[Source Title/ID]**: [1-sentence objective]. Key finding: [1-2 sentences].

## 4. References / Analyzed Sources
[List of all URLs, arXiv IDs, or document names analyzed.]
```

If the user requested APA/IEEE format, adjust the formatting and citation style accordingly.
