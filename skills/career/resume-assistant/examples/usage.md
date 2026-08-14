# Resume / CV Assistant — Usage Examples
## 💬 Quick Start — Just Ask!

You don't need to memorize commands. Simply tell the assistant what you need in plain language:

| Just say... | What happens |
|-------------|-------------|
| 💬 "Create a resume for a software engineer position" | Asks for your background, then builds a tailored resume |
| 💬 "Polish my resume" *(then paste your resume)* | Runs 40+ item checklist review and rewrites |
| 💬 "Optimize my resume for ATS" | Checks ATS compatibility, optimizes keywords and formatting |
| 💬 "Tailor my resume for this job description: [paste JD]" | Analyzes JD, performs gap analysis, customizes your resume |
| 💬 "Convert my resume to PDF" | Exports to PDF using the professional template |
| 💬 "Score my resume and tell me how to improve" | Scores out of 100 with detailed improvement plan |
| 💬 "What's wrong with my resume?" | Evaluates and identifies key issues to fix |
| 💬 "Here's my resume, can you help?" | Scores first, then suggests next steps |

### Example Conversations

**Creating a new resume:**
```
You:   Create a resume for a frontend engineer position at a startup

Bot:   I'd be happy to help create a tailored resume! To get started, could you
       share the following:
       1. Your work experience (companies, roles, dates, key achievements)
       2. Education background
       3. Technical skills
       4. Any specific job posting you're targeting? (optional but recommended)

You:   I have 3 years of experience at Shopify working on React...
       [provides background details]

Bot:   Here's your tailored resume:
       [generates complete resume using customize workflow]

       Would you like me to:
       • Score this resume to see how it rates?
       • Export it to PDF or Word?
       • Polish it further for specific improvements?
```

**Quick improvement:**
```
You:   Here's my resume, what do you think?
       [pastes resume]

Bot:   📊 Resume Score: 68/100 (Grade: C)

       Top 3 Issues:
       1. ❌ No quantified achievements — add metrics
       2. ⚠️ Weak action verbs — replace "responsible for" with impact verbs
       3. ⚠️ Missing keywords for your target role

       Would you like me to polish it now? I can fix all these issues.

You:   Yes, polish it

Bot:   [runs full polish with 40+ checklist items]
```

**Job-specific tailoring:**
```
You:   Tailor my resume for this job description:
       Senior Backend Engineer at Stripe
       Requirements: Go, distributed systems, payment APIs...

Bot:   🎯 Job Analysis Complete
       📊 Current Match: 62% → After Optimization: 89%
       [generates tailored version with keyword optimization]
```

> 💡 **Tip:** You can also use slash commands for more precise control — see below.

---

## Quick Reference

| Command | Purpose | Required Input |
|---------|---------|----------------|
| `/resume polish` | Fix errors, improve wording, checklist review | resume content |
| `/resume customize` | Tailor for a specific job | resume content + job description |
| `/resume export` | Convert to Word/MD/HTML/LaTeX/PDF | resume content + format |
| `/resume score` | Evaluate and get improvement plan | resume content |

---

## Example 1: Polish a Resume
**Command:** `/resume polish`

**Input:**
```
resume_content: |
  I am a software engineer with 5 years experience. I worked at Google
  where I was responsible for building web applications. I helped improve
  the system performance. I also managed some team members.

  Education: BS Computer Science, Stanford University, 2018
  Skills: Python, Java, React, SQL, AWS

language: en
```

**What you get:**
- 📋 40+ item checklist review with ✅/❌/⚠️ for every item
- ✨ Fully polished resume with strong action verbs and metrics
- 📝 Categorized change list: 🔴 Critical → 🟡 Major → 🟢 Minor → 💡 Suggestions
- 📖 Action verb and quantification guidance

---

## Example 2: Customize for a Job

**Command:** `/resume customize`

**Input:**
```
resume_content: [your existing polished resume]

job_description: |
  Senior Frontend Engineer at Meta
  Requirements:
  - 5+ years of frontend development experience
  - Expert in React, TypeScript, and modern CSS
  - Experience with large-scale web applications
  - Strong web performance optimization skills
  - Experience with design systems and component libraries

language: en
```

**What you get:**
- 🎯 Detailed job requirements analysis
- 📊 Gap analysis table mapping each requirement to your resume
- ✨ Tailored resume with keywords optimized
- 🔑 Keyword coverage report (before vs. after)
- 💡 Cover letter talking points + interview prep notes

---

## Example 3: Export to Multiple Formats

**Command:** `/resume export`

**Input:**
```
resume_content: [your resume in Markdown]
format: html
template: modern
```

**Available formats:**

| Format | Extension | Best For |
|--------|-----------|----------|
| `markdown` | .md | Editing, version control, GitHub |
| `html` | .html | Web viewing, browser → PDF |
| `word` | .docx | ATS submission, recruiter preference |
| `latex` | .tex | Academic, professional typesetting |
| `pdf` | .pdf | Final submission, universal format |

**Available templates:**

| Template | Style | Industries |
|----------|-------|------------|
| `professional` | Classic navy, serif headings | Finance, consulting, law |
| `modern` | Teal accents, creative layout | Tech, startups, marketing |
| `minimal` | Clean monochrome, dense | Senior roles, engineering |
| `academic` | Formal serif, multi-page | Faculty, research, PhD |

---

## Example 4: Score an Existing Resume

**Command:** `/resume score`

**Input:**
```
resume_content: [your resume]
target_role: Senior Software Engineer at FAANG
language: en
```

**What you get:**
- 📊 Score out of 100 with letter grade (A+ to F)
- 📈 Breakdown across 5 dimensions:
  - Content Quality (30 pts)
  - Structure & Formatting (25 pts)
  - Language & Grammar (20 pts)
  - ATS Optimization (15 pts)
  - Impact & Impression (10 pts)
- ✅ Top 3 strengths with specific examples
- 🔧 Priority-ranked improvements with Before → After rewrites
- 🎯 Role fit assessment with competitive percentile estimate
- 📋 5-step action plan with effort estimates

---

## Recommended Workflow

```
┌─────────────┐
│ Start Here  │
└──────┬──────┘
       ▼
┌──────────────┐     Have a resume?
│ /resume score│◄─── YES ──────────────┐
└──────┬───────┘                       │
       ▼                               │
┌──────────────┐                       │
│/resume polish│◄─── NO (write first)──┘
└──────┬───────┘
       ▼
┌────────────────┐   Have a target job?
│/resume customize│◄── YES
└──────┬─────────┘
       ▼
┌──────────────┐
│/resume export │──► Word / Markdown / HTML / LaTeX / PDF
└──────────────┘
```

**Pro tips:**
1. **Start with scoring** if you have an existing resume — know where you stand
2. **Polish first** to fix all basic issues before customizing
3. **Customize per application** — don't use one resume for all jobs
4. **Score again** after polish + customize to see improvement
5. **Export last** — get content perfect before formatting
6. **Use Markdown** as your working format — it converts cleanly to all others
