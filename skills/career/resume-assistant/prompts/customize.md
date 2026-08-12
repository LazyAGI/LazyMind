# Resume Customize — Prompt

## Task

Tailor the provided resume for a specific job position. Analyze the job description, identify keyword gaps, and produce a customized resume that maximizes relevance.

## Input

- **Resume Content**: `{{resume_content}}`
- **Job Description / Target Role**: `{{job_description}}`
- **Language**: `{{language}}`

---

## Step 1 — Job Description Analysis

Extract and organize:

1. **Required Skills** — Must-have technical and soft skills
2. **Preferred Skills** — Nice-to-have qualifications
3. **Key Responsibilities** — Core duties of the role
4. **Industry Keywords** — ATS-critical terms and phrases
5. **Experience Level** — Years and seniority expected
6. **Culture Signals** — Values, tone, and team dynamics clues

---

## Step 2 — Gap Analysis

Map every requirement to the resume:

| # | Requirement | Status | Evidence in Resume |
|---|-------------|--------|--------------------|
| 1 | [skill/qualification] | ✅ Strong Match / ⚠️ Partial / ❌ Gap | [where it appears or "Not found"] |

Calculate an initial match score: `matched / total requirements × 100`

---

## Step 3 — Customization Actions

### 3a. Professional Summary Rewrite

- Mirror the job description's language and keywords
- Lead with the 2-3 most relevant qualifications
- Address the role directly (e.g., "Senior Backend Engineer with...")

### 3b. Experience Optimization

- **Reorder** bullets within each role to put the most relevant first
- **Strengthen** bullets that match key responsibilities (add metrics if missing)
- **Add keywords** naturally into existing achievement descriptions
- **De-emphasize** irrelevant experience (fewer bullets, less detail — never delete honest history)

### 3c. Skills Alignment

- **Reorder** skills by relevance to the target role
- **Surface** skills the candidate has but didn't explicitly list
- **Match terminology** to the job description (e.g., "CI/CD" vs "continuous integration")
- **Group** by relevance: "Core Skills" → "Additional Skills"

### 3d. Additional Sections

- Suggest adding relevant certifications, courses, or projects
- For career transitions: highlight transferable skills prominently
- For academic roles: add publications, teaching, grants sections

---

## Step 4 — Keyword Optimization Report

| Metric | Value |
|--------|-------|
| **Keywords Matched** | X / Y (list them) |
| **Keywords Added** | X (list where they were added) |
| **Keywords Still Missing** | X (with suggestions to address) |
| **Overall Keyword Coverage** | X% |

---

## Output Format

```
## 🎯 Job Analysis

**Target**: [Job Title] at [Company]
**Level**: [Junior / Mid / Senior / Lead / Executive]
**Key Requirements**: [top 5 bullet points]

---

## 📊 Gap Analysis

| # | Requirement | Status | Evidence |
|---|-------------|--------|----------|
| ... | ... | ... | ... |

**Initial Match Score**: X%

---

## ✨ Customized Resume

[Complete tailored resume in Markdown format]

---

## 🔑 Keyword Report

### ✅ Matched Keywords
- keyword1, keyword2, keyword3...

### ➕ Added Keywords
- keyword → added to [section/bullet]

### ❌ Missing Keywords (with recommendations)
- keyword → [suggestion to address]

### Coverage: X% → Y% (after customization)

---

## 💡 Additional Recommendations

### Cover Letter Talking Points
1. [point to emphasize]
2. [point to emphasize]

### Interview Prep Notes
1. [likely question based on gaps]
2. [talking point to prepare]

### Skills to Develop
1. [skill to learn for stronger candidacy]
```
