# Resume Score — Prompt

## Task

Evaluate the provided resume comprehensively, assign a 100-point score, highlight strengths, and provide prioritized, actionable improvement suggestions with before/after examples.

## Input

- **Resume Content**: `{{resume_content}}`
- **Target Role** (optional): `{{target_role}}`
- **Language**: `{{language}}`

---

## Scoring Rubric (100 Points)

### Dimension 1 — Content Quality (30 pts)

| Criteria | Max Pts | What to Evaluate |
|----------|---------|------------------|
| Achievement Focus | 8 | Quantified, results-driven bullets vs. vague duties |
| Action Verbs | 5 | Strong, varied verbs at the start of each bullet |
| Relevance | 7 | Content relevance to target role / industry |
| Completeness | 5 | All essential sections present and well-developed |
| Differentiation | 5 | Unique content that stands out from generic resumes |

### Dimension 2 — Structure & Formatting (25 pts)

| Criteria | Max Pts | What to Evaluate |
|----------|---------|------------------|
| Layout & Flow | 7 | Clean, logical, easy-to-scan visual structure |
| Consistency | 6 | Uniform dates, bullets, capitalization, fonts |
| Appropriate Length | 5 | Right page count for experience level |
| Section Order | 4 | Optimal ordering for maximum impact |
| White Space | 3 | Balanced readability and density |

### Dimension 3 — Language & Grammar (20 pts)

| Criteria | Max Pts | What to Evaluate |
|----------|---------|------------------|
| Grammar | 7 | Grammatical correctness throughout |
| Spelling | 5 | Zero spelling errors |
| Tone | 4 | Professional, confident, appropriate voice |
| Clarity | 4 | Clear, concise, unambiguous writing |

### Dimension 4 — ATS Optimization (15 pts)

| Criteria | Max Pts | What to Evaluate |
|----------|---------|------------------|
| Keywords | 6 | Industry-relevant keywords present |
| Standard Headings | 4 | ATS-recognizable section titles |
| Format Compatibility | 5 | No tables, images, or complex layouts that break ATS |

### Dimension 5 — Impact & First Impression (10 pts)

| Criteria | Max Pts | What to Evaluate |
|----------|---------|------------------|
| 6-Second Test | 4 | Does the resume grab attention in 6 seconds? |
| Career Story | 3 | Clear narrative and career progression |
| Professionalism | 3 | Overall professional presentation |

---

## Grade Scale

| Grade | Score | Meaning |
|-------|-------|---------|
| A+ | 95–100 | Exceptional — ready for top-tier applications |
| A  | 90–94 | Excellent — very competitive |
| B+ | 85–89 | Good — minor refinements needed |
| B  | 80–84 | Solid — some areas to strengthen |
| C+ | 75–79 | Average — notable improvement needed |
| C  | 70–74 | Below average — significant work needed |
| D  | 60–69 | Weak — major overhaul recommended |
| F  | < 60 | Critical — near-complete rewrite needed |

---

## Evaluation Process

### Step 1 — Section-by-Section Analysis

For each resume section:
- What works well (specific examples from the resume)
- What needs improvement (specific examples)
- Concrete rewrite suggestion with **Before → After**

### Step 2 — Score Each Dimension

For each of the 5 dimensions:
- Assign numeric score with brief justification
- Call out the top issue holding the score back

### Step 3 — Prioritize Improvements

Rank all suggestions:
- 🔴 **Critical** — Must fix; could cause immediate rejection
- 🟡 **Important** — Should fix; significantly boosts competitiveness
- 🟢 **Nice to Have** — Optional polish for extra edge

### Step 4 — Role Fit (if target_role provided)

- Fit score out of 10
- Estimated competitive percentile ("Likely in the top X% of applicants")
- 3 biggest strengths for this specific role
- 3 biggest gaps compared to an ideal candidate

---

## Output Format

```
## 📊 Resume Score: XX / 100 — Grade: [Letter]

---

## 📈 Dimension Breakdown

| Dimension | Score | Max | Key Issue |
|-----------|-------|-----|-----------|
| Content Quality | X | 30 | [one-line summary] |
| Structure & Formatting | X | 25 | [one-line summary] |
| Language & Grammar | X | 20 | [one-line summary] |
| ATS Optimization | X | 15 | [one-line summary] |
| Impact & Impression | X | 10 | [one-line summary] |
| **Total** | **XX** | **100** | |

---

## ✅ Top Strengths

1. **[Strength]** — [specific example from resume and why it's effective]
2. **[Strength]** — [specific example]
3. **[Strength]** — [specific example]

---

## 🔧 Improvement Suggestions

### 🔴 Critical

**1. [Issue Title]**
- **Problem**: [what's wrong]
- **Before**: "[exact text from resume]"
- **After**: "[suggested rewrite]"
- **Why**: [impact on the reader/ATS]

### 🟡 Important

**1. [Issue Title]**
- **Before**: "[current text]"
- **After**: "[improved text]"

### 🟢 Nice to Have

**1. [Issue Title]**
- [suggestion with rationale]

---

## 🎯 Role Fit Assessment

*(only if target_role is provided)*

| Metric | Value |
|--------|-------|
| **Fit Score** | X / 10 |
| **Competitive Position** | Top ~X% of applicants (estimated) |

### Strengths for This Role
1. [strength]
2. [strength]
3. [strength]

### Gaps to Address
1. [gap + how to address it]
2. [gap + how to address it]
3. [gap + how to address it]

---

## 📋 5-Step Action Plan

1. **[Action]** — [expected impact] ⏱ [estimated effort]
2. **[Action]** — [expected impact] ⏱ [estimated effort]
3. **[Action]** — [expected impact] ⏱ [estimated effort]
4. **[Action]** — [expected impact] ⏱ [estimated effort]
5. **[Action]** — [expected impact] ⏱ [estimated effort]
```
