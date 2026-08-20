# Resume Export — Prompt

## Task

Export the provided resume into the specified format with professional styling.

## Input

- **Resume Content**: `{{resume_content}}`
- **Export Format**: `{{format}}` (word | markdown | html | latex | pdf)
- **Template Style**: `{{template}}` (professional | modern | minimal | academic)

---

## Format Specifications

### 📝 Markdown (.md)

Generate clean, well-structured Markdown:
- H1 for candidate name
- H2 for section headings
- H3 for job titles or subsections
- Consistent bullet points with proper indentation
- Horizontal rules (`---`) between major sections
- Bold for emphasis on key items
- No raw HTML embedded

### 🌐 HTML (.html)

Generate a complete, self-contained HTML file:
- Valid HTML5 with semantic elements (`<header>`, `<section>`, `<article>`)
- All CSS embedded in `<style>` tag (no external dependencies)
- Responsive layout that looks good on screen and print
- `@media print` styles for clean PDF generation via browser
- Proper `<meta charset="UTF-8">` and viewport tags
- Template-specific color scheme and typography

**Template Color Schemes:**

| Template | Primary Color | Font Family |
|----------|--------------|-------------|
| Professional | `#2c3e50` (Navy) | Georgia / Segoe UI |
| Modern | `#00897b` (Teal) | Inter / Helvetica Neue |
| Minimal | `#333333` (Charcoal) | System UI |
| Academic | `#1a1a2e` (Dark Navy) | Times New Roman / Georgia |

### 📄 Word (.docx)

Since direct .docx generation requires binary output, provide:
1. A **Pandoc-optimized Markdown** version with YAML front matter
2. Exact conversion command: `pandoc resume.md -o resume.docx --reference-doc=template.docx`
3. Formatting notes for manual Word paste (font sizes, margins, styles)

**YAML Front Matter for Pandoc:**
```yaml
---
title: "Resume"
author: "{{name}}"
geometry: margin=0.75in
fontsize: 11pt
---
```

### 📐 LaTeX (.tex)

Generate a complete, compilable LaTeX document:
- Document class: `article` (11pt, a4paper)
- Required packages: `geometry`, `titlesec`, `enumitem`, `hyperref`, `xcolor`, `tabularx`
- Custom commands for consistent formatting (`\experienceitem`, `\educationitem`)
- XeLaTeX-compatible for Unicode/Chinese support
- Compile instruction: `xelatex resume.tex`
- For Chinese: add `\usepackage{ctex}` and use `xelatex`

### 📑 PDF

Generate print-optimized HTML with:
- Exact A4/Letter page dimensions in CSS (`@page { size: A4; margin: 0.6in; }`)
- Page break controls (`page-break-inside: avoid`)
- Conversion methods provided:
  - **Browser**: Open HTML → Print → Save as PDF
  - **wkhtmltopdf**: `wkhtmltopdf --page-size A4 resume.html resume.pdf`
  - **Pandoc**: `pandoc resume.md -o resume.pdf --pdf-engine=xelatex`
  - **WeasyPrint**: `weasyprint resume.html resume.pdf`

---

## Template Styles

### Professional
- Classic, conservative design suitable for corporate roles
- Serif headings, clean sans-serif body text
- Navy blue accent color, traditional borders
- Best for: Finance, consulting, law, healthcare, government

### Modern
- Contemporary design with subtle creative touches
- Sans-serif throughout, generous spacing
- Teal/coral accents, optional sidebar for skills
- Best for: Tech, startups, product, marketing, design

### Minimal
- Ultra-clean whitespace-focused design
- Single font family, monochrome palette
- Maximum content density with elegant spacing
- Best for: Senior professionals, engineering, when content speaks for itself

### Academic
- Formal academic CV format
- Serif fonts throughout (Times New Roman / Garamond)
- Supports multi-page layout with proper page breaks
- Extra sections: Publications, Research, Teaching, Grants
- Best for: Faculty positions, postdocs, research roles, PhD applications

---

## Output Format

```
## 📄 Export: {{format}} — {{template}} Template

[Complete file content in the target format, ready to save/compile]

---

## 📋 How to Use

### Save & Convert
1. [Step-by-step instructions]

### Recommended Tools
- [tool 1 with install/usage]
- [tool 2 with install/usage]

### Tips
- [format-specific tip 1]
- [format-specific tip 2]
```
