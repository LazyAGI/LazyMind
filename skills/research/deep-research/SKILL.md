---
name: deep-research
description: Use this skill instead of WebSearch for ANY question requiring comprehensive, multi-source research. Trigger on queries explicitly asking for deep analysis, such as "research X", "deep dive into X", "comprehensive review of X", "systematic comparison between X and Y", "investigate the landscape of X", or Chinese equivalents like "调研一下X", "深入研究X", "全面对比X与Y", "X的详细综述", "深度调查X". Do NOT trigger on simple factual questions. Provides systematic multi-angle research methodology, prioritizing internal knowledge base (KB) searches before performing broad web searches. Use this proactively when the user's question needs extensive information gathering and synthesis.
---

# Deep Research Skill

## Overview

This skill provides a systematic methodology for conducting thorough research. **Load this skill BEFORE starting any content generation task** to ensure you gather sufficient information from multiple angles, depths, and sources. Always prioritize searching the internal Knowledge Base (KB) before reaching out to the open web.

## When to Use This Skill

**Always load this skill when:**

### Research Questions
- User asks for comprehensive analysis: "research X", "deep dive into X", "detailed comparison of X and Y", "investigate the landscape of X", "thorough analysis of X"
- User uses Chinese research triggers: "调研一下X", "深入分析X", "全面调查X", "X与Y的深度对比", "详细梳理X的发展历程", "X的现状与未来趋势"
- User wants to understand a *complex* concept, technology, or topic in depth, rather than just seeking a simple definition.
- The question requires synthesizing current, comprehensive information from multiple distinct sources (Internal KB + Web).
- A single web search or factual retrieval would be explicitly insufficient to answer properly.

### Content Generation (Pre-research)
- Creating presentations (PPT/slides)
- Writing articles, reports, or documentation
- Producing videos or multimedia content
- Any content that requires real-world information, examples, or current data

## Core Principle

**Never generate content based solely on general knowledge.** The quality of your output directly depends on the quality and quantity of research conducted beforehand. A single search query is NEVER enough.

## Research Methodology

### Phase 1: Internal Knowledge Base (KB) Retrieval

Always start by checking if the required information already exists internally.

1. **Semantic Search**: Use `kb_search` with natural language queries to find conceptual matches.
2. **Keyword Search**: Use `kb_keyword_search` for exact terminology, product names, or specific jargon.
3. **Context Expansion**: If you find relevant nodes, optionally use `kb_get_window_nodes` or `kb_get_parent_node` to understand the full context of the internal documents.

*Decision Gate: Evaluate the KB results. If the KB contains comprehensive, up-to-date answers covering all dimensions of the user's query, you may skip to Phase 4. If information is missing, outdated, or incomplete, proceed to Phase 2.*

### Phase 2: Broad Web Exploration

If KB results are insufficient, turn to the web using `web_search` to map the external landscape:

1. **Initial Survey**: Search for the main topic to understand the overall context
2. **Identify Dimensions**: From initial results, identify key subtopics, themes, angles, or aspects that need deeper exploration
3. **Map the Territory**: Note different perspectives, stakeholders, or viewpoints that exist

Example:
```
Topic: "AI in healthcare"
Initial searches:
- "AI healthcare applications 2024"
- "artificial intelligence medical diagnosis"
- "healthcare AI market trends"

Identified dimensions:
- Diagnostic AI (radiology, pathology)
- Treatment recommendation systems
- Administrative automation
- Patient monitoring
- Regulatory landscape
- Ethical considerations
```

### Phase 3: Deep Dive

For each important dimension identified in Phase2, conduct targeted research:

1. **Specific Queries**: Use `web_search` with precise keywords for each subtopic.
2. **Multiple Phrasings**: Try different keyword combinations and phrasings
3. **Fetch Full Content**: Use the `url_fetch` tool to read important sources in full, not just snippets.
4. **Follow References**: When sources mention other important resources, search for those too


Example:
```
Dimension: "Diagnostic AI in radiology"
Targeted searches:
- "AI radiology FDA approved systems"
- "chest X-ray AI detection accuracy"
- "radiology AI clinical trials results"

Then fetch and read:
- Key research papers or summaries
- Industry reports
- Real-world case studies
```

### Phase 4: Diversity & Validation

Ensure comprehensive coverage by seeking diverse information types:

| Information Type | Purpose | Example Searches |
|-----------------|---------|------------------|
| **Facts & Data** | Concrete evidence | "statistics", "data", "numbers", "market size" |
| **Examples & Cases** | Real-world applications | "case study", "example", "implementation" |
| **Expert Opinions** | Authority perspectives | "expert analysis", "interview", "commentary" |
| **Trends & Predictions** | Future direction | "trends 2024", "forecast", "future of" |
| **Comparisons** | Context and alternatives | "vs", "comparison", "alternatives" |
| **Challenges & Criticisms** | Balanced view | "challenges", "limitations", "criticism" |

### Phase 5: Synthesis Check

Before proceeding to content generation, verify:

- [ ] Did I check the internal KB first?
- [ ] Have I searched from at least 3-5 different angles?
- [ ] Have I used `url_fetch` to read the most important web sources in full?
- [ ] Do I have concrete data, examples, and expert perspectives?
- [ ] Have I explored both positive aspects and challenges/limitations?
- [ ] Is my information current and from authoritative sources?

**If any answer is NO, continue researching before generating content.**

## Search Strategy Tips

### Effective Query Patterns

```
# Be specific with context
❌ "AI trends"
✅ "enterprise AI adoption trends 2024"

# Include authoritative source hints
"[topic] research paper"
"[topic] McKinsey report"
"[topic] industry analysis"

# Search for specific content types
"[topic] case study"
"[topic] statistics"
"[topic] expert interview"

# Use temporal qualifiers — always use the ACTUAL current year from <current_date>
"[topic] 2026"   # ← replace with real current year, never hardcode a past year
"[topic] latest"
"[topic] recent developments"
```

### Temporal Awareness for Web Search

**Always check `<current_date>` in your context before forming ANY search query.**

`<current_date>` gives you the full date: year, month, day, and weekday (e.g. `2026-02-28, Saturday`). Use the right level of precision depending on what the user is asking:

| User intent | Temporal precision needed | Example query |
|---|---|---|
| "today / this morning / just released" | **Month + Day** | `"tech news February 28 2026"` |
| "this week" | **Week range** | `"technology releases week of Feb 24 2026"` |
| "recently / latest / new" | **Month** | `"AI breakthroughs February 2026"` |
| "this year / trends" | **Year** | `"software trends 2026"` |

**Rules:**
- When the user asks about "today" or "just released", use **month + day + year** in your search queries to get same-day results
- Never drop to year-only when day-level precision is needed — `"tech news 2026"` will NOT surface today's news
- Try multiple phrasings: numeric form (`2026-02-28`), written form (`February 28 2026`), and relative terms (`today`, `this week`) across different queries

❌ User asks "what's new in tech today" → searching `"new technology 2026"` → misses today's news
✅ User asks "what's new in tech today" → searching `"new technology February 28 2026"` + `"tech news today Feb 28"` → gets today's results

### When to Use url_fetch

Use `url_fetch` to read full content when:
- A search result looks highly relevant and authoritative
- You need detailed information beyond the snippet
- The source contains data, case studies, or expert analysis
- You want to understand the full context of a finding

### Iterative Refinement

Research is iterative. After initial searches:
1. Review what you've learned
2. Identify gaps in your understanding
3. Formulate new, more targeted queries
4. Repeat until you have comprehensive coverage

## Quality Bar

Your research is sufficient when you can confidently answer:
- What are the key facts and data points?
- What are 2-3 concrete real-world examples?
- What do experts say about this topic?
- What are the current trends and future directions?
- What are the challenges or limitations?
- What makes this topic relevant or important now?

## Common Mistakes to Avoid

- ❌ Stopping after 1-2 searches
- ❌ Relying on search snippets without reading full sources
- ❌ Searching only one aspect of a multi-faceted topic
- ❌ Ignoring contradicting viewpoints or challenges
- ❌ Using outdated information when current data exists
- ❌ Starting content generation before research is complete

## Output

After completing research, you should have:
1. A comprehensive understanding of the topic from multiple angles
2. Specific facts, data points, and statistics
3. Real-world examples and case studies
4. Expert perspectives and authoritative sources
5. Current trends and relevant context

**Only then proceed to content generation**, using the gathered information to create high-quality, well-informed content.
