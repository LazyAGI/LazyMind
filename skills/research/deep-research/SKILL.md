---
name: deep-research
description: Use only when the user explicitly requests comprehensive multi-source research, such as "research X", "deep dive into X", "comprehensive review of X", "systematic comparison between X and Y", "investigate the landscape of X", or Chinese equivalents like "调研一下X", "深入研究X", "全面对比X与Y", "X的详细综述", "深度调查X". Do NOT trigger for simple questions, how-to guidance, ordinary recommendations, or content creation merely because research could improve the answer. Follow the source priorities supplied by the system and the user.
---

# Deep Research Skill

## Overview

This skill provides a systematic methodology for genuinely comprehensive research. Load it only when the user explicitly asks for deep, systematic, multi-source investigation or when the requested deliverable inherently requires such research. Do not load it for a normal answer, introductory explanation, how-to question, or ordinary content-generation request.

## When to Use This Skill

**Always load this skill when:**

### Research Questions
- User asks for comprehensive analysis: "research X", "deep dive into X", "detailed comparison of X and Y", "investigate the landscape of X", "thorough analysis of X"
- User uses Chinese research triggers: "调研一下X", "深入分析X", "全面调查X", "X与Y的深度对比", "详细梳理X的发展历程", "X的现状与未来趋势"
- User explicitly wants to understand a *complex* concept, technology, or topic in depth, rather than seeking normal guidance.
- The question requires synthesizing current, comprehensive information from multiple distinct sources.
- A single web search or factual retrieval would be explicitly insufficient to answer properly.

Do **not** infer deep-research intent merely because the topic is broad or because the user wants to create a presentation, article, report, video, or other content.

## Core Principle

**Never generate content based solely on general knowledge.** The quality of your output directly depends on the quality and quantity of research conducted beforehand. A single search query is NEVER enough.

## Research Methodology

### Phase 1: Material Retrieval and Source Planning

Identify the evidence needed, then retrieve material from the sources prioritized or permitted by the system instructions and the user's request. Sources may include internal documents, public web pages, academic collections, user-provided files, or specific URLs. Do not assume a source priority, probe for sources that were not offered, or override the host system's routing rules.

1. **Define Evidence Needs**: Break the question into facts, examples, viewpoints, and time-sensitive claims that require support.
2. **Choose Appropriate Sources**: Match each evidence need to the source types and retrieval capabilities made available for this task.
3. **Retrieve Initial Material**: Use precise semantic, keyword, or document-scoped queries as appropriate.
4. **Assess Coverage**: Record which dimensions are supported and which still contain gaps or conflicting evidence.

*Decision Gate: If the retrieved material is sufficiently current, diverse, and complete, proceed to Phase 4. Otherwise continue with broader exploration using other permitted sources.*

### Phase 2: Broad Exploration

Use the permitted search or retrieval capabilities to map the broader landscape:

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

1. **Specific Queries**: Use the selected search or retrieval capability with precise keywords for each subtopic.
2. **Multiple Phrasings**: Try different keyword combinations and phrasings
3. **Read Full Content**: Read important sources in full when summaries or snippets are insufficient.
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

- [ ] Did I follow the source priorities established by the system and the user?
- [ ] Have I searched from at least 3-5 different angles?
- [ ] Have I read the most important sources in full rather than relying only on snippets?
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

### When to Read Full Sources

Read the full content of a source when:
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

- ❌ Loading this skill for a simple how-to or ordinary content-creation question
- ❌ Overriding the source routing or priorities supplied by the system or the user
- ❌ Probing framework-specific sources that were not made available for the task
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
