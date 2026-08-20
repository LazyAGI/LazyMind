# Resume Polish — Prompt

## Task

Polish and improve the provided resume. Run a comprehensive checklist, fix all issues, and deliver an enhanced version.

## Input

- **Resume Content**: `{{resume_content}}`
- **Language**: `{{language}}`

---

## Step 1 — Comprehensive Checklist Review

Evaluate EVERY item below. Mark each as ✅ Pass, ❌ Fail, or ⚠️ Needs Attention.

### A. Contact & Personal Information

- [ ] Full name clearly displayed
- [ ] Professional email address (no nicknames)
- [ ] Phone number with correct format and area code
- [ ] City/State location (no full street address)
- [ ] LinkedIn URL present and customized
- [ ] Portfolio/GitHub link (if applicable for role)
- [ ] No unnecessary personal info (age, photo, marital status)

### B. Professional Summary

- [ ] Present and concise (2-3 sentences)
- [ ] Tailored to target role (not generic)
- [ ] Contains top 2-3 qualifications
- [ ] Includes relevant keywords
- [ ] Free of clichés ("hard-working team player", "passionate self-starter")

### C. Work Experience

- [ ] Reverse chronological order
- [ ] Consistent date format throughout
- [ ] Each entry: job title, company, location, date range
- [ ] 3-6 bullet points per role
- [ ] Bullets start with strong action verbs
- [ ] Achievements quantified with metrics (numbers, %, $)
- [ ] Results-focused (not just responsibilities)
- [ ] Past tense for former roles, present for current
- [ ] No personal pronouns (I, me, my)
- [ ] No unexplained gaps > 6 months
- [ ] No overlapping employment dates

### D. Education

- [ ] Reverse chronological order
- [ ] Degree, major, institution, graduation date included
- [ ] GPA only if notable (> 3.5/4.0)
- [ ] Relevant coursework only for recent graduates
- [ ] Honors/awards mentioned
- [ ] No high school if college degree obtained

### E. Skills

- [ ] Categorized (Technical, Tools, Languages, etc.)
- [ ] Relevant to target position
- [ ] No self-rating scales (e.g., "Python: 8/10")
- [ ] Both hard and soft skills included
- [ ] Matches common industry keywords

### F. Grammar, Spelling & Language

- [ ] Zero spelling errors
- [ ] Zero grammatical errors
- [ ] Consistent tense usage
- [ ] Professional tone throughout
- [ ] No abbreviations used without context
- [ ] Consistent punctuation (periods at end of bullets or not)
- [ ] For zh: proper spacing between Chinese and English/numbers

### G. Formatting & Layout

- [ ] Appropriate length (1-2 pages)
- [ ] Consistent font style and sizing
- [ ] Adequate margins (0.5"-1")
- [ ] Clear section headings with visual hierarchy
- [ ] Consistent bullet style
- [ ] Sufficient white space
- [ ] No decorative graphics or images

### H. ATS Compatibility

- [ ] Standard section headings used
- [ ] No content in headers/footers
- [ ] No tables, text boxes, or multi-column layout
- [ ] No images or graphics
- [ ] Industry keywords present naturally
- [ ] Simple, parseable formatting

---

## Step 2 — Polish & Rewrite

Using the checklist results, produce an improved version:

1. Fix all ❌ items
2. Improve all ⚠️ items
3. Strengthen weak bullets: add action verbs + quantified results
4. Replace vague phrases with specific, impactful statements
5. Ensure consistent formatting throughout
6. Optimize keywords for the apparent target industry

---

## Step 3 — Change Summary

Organize all changes by priority:

| Priority | Meaning |
|----------|---------|
| 🔴 Critical | Errors that could cause immediate rejection |
| 🟡 Major | Changes that significantly improve competitiveness |
| 🟢 Minor | Polish and refinement for extra impact |
| 💡 Suggestion | Optional improvements for consideration |

---

## Output Format

```
## 📋 Checklist Results

### A. Contact & Personal Info
[results per item]

### B. Professional Summary
[results per item]

... (all sections)

---

## ✨ Polished Resume

[Complete improved resume in Markdown]

---

## 📝 Change Summary

### 🔴 Critical Fixes
- [fix description]

### 🟡 Major Improvements
- [improvement description]

### 🟢 Minor Enhancements
- [enhancement description]

### 💡 Suggestions
- [optional suggestion]
```

---

## Action Verb Reference

Use these to strengthen bullet points:

| Category | Verbs |
|----------|-------|
| **Leadership** | Led, Directed, Managed, Supervised, Spearheaded, Orchestrated, Championed |
| **Achievement** | Achieved, Exceeded, Delivered, Surpassed, Generated, Secured |
| **Technical** | Developed, Engineered, Architected, Implemented, Deployed, Automated, Optimized |
| **Analysis** | Analyzed, Evaluated, Identified, Diagnosed, Forecasted, Measured |
| **Communication** | Presented, Negotiated, Collaborated, Facilitated, Influenced |
| **Improvement** | Improved, Streamlined, Modernized, Transformed, Accelerated, Reduced |
| **Creation** | Created, Designed, Established, Launched, Pioneered, Innovated |

## Quantification Guide

Always try to add numbers:

- **Revenue**: Generated $X in revenue / Grew revenue by X%
- **Efficiency**: Reduced processing time by X% / Saved X hours per week
- **Scale**: Managed team of X / Served X,000+ users daily
- **Quality**: Achieved X% uptime / Reduced error rate by X%
- **Growth**: Increased user base by X% / Expanded to X markets
