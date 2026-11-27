# Resume delivery in LazyMind

## Templates

Respect the user's template selection. These are layout choices, not different candidate facts.

| ID | Name | Intended appearance |
| --- | --- | --- |
| `business` | 商务蓝 / Business | Single column, blue section rules, aligned dates |
| `modern` | 现代色带 / Modern | Prominent colored header and contemporary sections |
| `sidebar` | 专业侧栏 / Sidebar | Compact profile and skills sidebar with experience in the main column |
| `editorial` | 创意版式 / Editorial | Editorial typography and stronger visual hierarchy |
| `campus` | 校园清新 / Campus | Education-forward layout for graduate and internship applications |
| `minimal` | 黑白 ATS / Minimal | Restrained monochrome single-column layout |

All templates can include an explicitly selected photo. No-photo output must reflow without an empty placeholder. Do not treat photos or complex layouts as mandatory for recruitment systems.

## Tool input

Finish the positioning, evidence selection, bullet editing, and source-based review in [resume-editing.md](resume-editing.md) before filling the tool input. Send only the final resume text: reviewer explanations and suggestions belong outside the document. Template choice changes the layout, not the facts or responsibility level.

Create a resume with `render_resume`. Pass actual plain text, not Markdown headings, HTML, or arbitrary CSS, in the structured fields:

```json
{
  "resume": {
    "basics": {
      "name": "User-supplied name",
      "title": "Target role",
      "email": "Only if supplied",
      "phone": "Only if supplied",
      "location": "Only if supplied",
      "links": ["Only user-supplied portfolio or profile links"]
    },
    "summary": "A concise, evidence-backed summary; omit when unnecessary.",
    "sections": [
      {
        "title": "实习经历",
        "items": [
          {
            "title": "Actual employer / project",
            "subtitle": "Actual role or technical stack",
            "date": "Actual dates",
            "bullets": ["Supported action, method, and result"]
          }
        ]
      }
    ]
  },
  "template": "business",
  "photo_fit": "contain",
  "filename": "姓名_目标岗位_简历"
}
```

Omit unavailable optional fields. Use flexible sections for education, skills, work, internships, projects, certificates, publications, and other genuinely supported facts. Skills can be grouped into items with a category title and plain-text bullets. A subtitle is optional: omit it if it repeats the title or adds no distinct information. Do not pad missing sections with invented examples, repeated labels, or an unsupported learning channel.

For a photo, add `photo_path` using the real local attachment locator provided by the available file tools. Read or resolve the attachment through those tools when necessary. Never guess a local path, turn an arbitrary remote URL into a portrait, or pass a `file_id:` label as if it were a resolved path. Use `photo_fit="cover"` only for an explicit cropping choice. Use `include_photo=false` for the user's no-photo setting.

For missing identity/contact essentials, use a filename ending in `_草稿` / `_draft` and explain what must be completed in the chat. Do not describe a draft as ready to submit. An optional missing homepage, certificate, or award does not require an empty section or a blocking question.

## Template and photo changes

When the previous renderer result supplies `resume_data_path`, reuse it rather than rewriting the content:

```json
{
  "source_path": "<actual resume_data_path returned in this conversation>",
  "template": "sidebar",
  "photo_fit": "contain"
}
```

To remove the portrait, add `include_photo=false`. To replace it, add the newly selected `photo_path`. Do not reuse a saved source path from a different conversation or candidate. If no saved data path is available, reconstruct only from the current candidate's confirmed content and ask when that content cannot be recovered reliably.

## Result contract

The renderer returns the actual generated files with `file_markdown` download links and a `resume_data_path` for later changes. Prefer the PDF as the finished preview because it is the downloaded document's actual layout. Word remains editable and may paginate differently in different office applications. HTML is an additional view; do not claim all three formats are pixel-identical.

Return the tool's PDF and Word links verbatim, with a short status and any essential missing information. Keep optimization notes separate from the document. Report any partial delivery accurately and do not manufacture download links.
