---
name: tailored-resume-generator
description: Analyze job descriptions and improve truthful resumes, with selectable layouts, optional user-provided photos, and ready-to-use PDF and editable Word deliverables. Use for resume tailoring, career transitions, ATS review, and changing a resume's template or photo.
---

# Tailored Resume Generator

## 投递稿编辑规则：先执行，再渲染

以下规则用于最终简历正文，优先于通用润色建议。交付的是招聘方能直接阅读的投递稿，不是事实审计记录。

- **常规应届／在读、求职目标明确时，默认省略摘要。** 姓名、岗位和实习时长已在头部、学历已在教育背景交代时，不再重复概述。转型、复杂履历或用户明确要求时才保留 1–2 句定位与核心经验；不写“习惯”“善于”“具有……能力”等自评，不罗列整份简历的工具和数字。日志、数据核对、证据记录等具体做法写入对应经历。
- **一条 bullet 只承担一个主要贡献，通常 1 句，至多 2 句。** 一段经历通常用 2–4 条，不是每条写 2–4 句。同段先写最能证明岗位要求的技术贡献与已知结果，再写辅助工作；不要照原材料“评审→联调→写用例→报告”的流水顺序。合并重复流程和工具，不串成长篇工作流水账。保留所有相关角色和项目，不靠删整段经历压缩篇幅。
- **用准确、正面的本人行动自然表达责任边界。** 例如“复现缺陷、采集诊断证据并验证修复”。不要把“修复代码由谁完成”“并非本人开发”“不是谁负责”等审校解释复制进正式简历；不能因此把参与写成主导、把验证写成实现。团队成果、实验环境、检索指标含义等必要限定仍须保留，可自然写入主句，避免反复加“团队整体结果”“非本人全部贡献”一类括号说明。
- **不增加身份、来源或学习渠道。** “独立”“主导”“课程项目”“生产系统”“开源项目”“自学”“培训”等都必须来自材料；修过课或学习过工具，不足以推断自学阶段、培训经历、扎实基础或独立交付能力。材料稀疏时简短、克制，不为填版面增强事实。
- **主标题与副标题各提供不同信息。** `item.title` 与 `item.subtitle` 不得相同或仅换同义词；没有新增信息时省略副标题。技能按少量清楚的组别归类，减少重复列工具和反复写相同套话，同时保留原有“学习过／了解／熟悉”等程度。
- **渲染前逐项检查。** 摘要有无自评或工具堆砌；每条有无多个无主次贡献；有无代码归属等审校解释；标题副标题有无重复；有无未经支持的渠道、身份、责任或能力标签。改好文本再调用 `render_resume`，不把问题交给排版器处理。

Adapted from ComposioHQ/awesome-claude-skills for LazyMind. Preserve the upstream workflow of job analysis, evidence mapping, targeted rewriting, and strategic recommendations. Use the following delivery and truthfulness rules for every resume produced in LazyMind.

## Establish the facts

- Use the user's supplied resume, attachments, and explicitly confirmed background. Never reuse unrelated candidates or demonstration data from earlier conversations.
- Extract the target role, must-have qualifications, important requirements, and optional skills from the job description. Map each requirement to actual candidate evidence and identify genuine gaps.
- Keep employers, titles, dates, education, projects, tools, numbers, and contribution boundaries faithful to the source. Do not invent contact details, rankings, certificates, metrics, or project experience. Identity, learning channel, and setting labels are facts too: do not add "independent", "lead", "course project", "production", "open-source", "self-taught", "training", or proficiency levels unless the source supports them.
- Preserve proficiency levels: "studied" or "familiar with" must not become "proficient" or "can independently deliver". A job requirement is not evidence of a candidate's ability.
- Distinguish participation from leadership, the candidate's work from teammates' work, and team results from individual results. Retain the original meaning of metrics such as Recall@5, accuracy, test concurrency, and production SLA.
- Reorder and sharpen supported material using action, method, and outcome. Include every relevant supplied role or project; do not remove whole experiences to force a one-page layout.
- Ask only for missing information that blocks a useful result. If the user requests a draft with incomplete information, produce a clearly named draft using the facts available. Keep missing optional sections out of the resume instead of adding empty boilerplate. List missing contact or essential identity information separately in the chat and mark the filename as a draft.

## Edit for a submission-ready document

Read [references/resume-editing.md](references/resume-editing.md) and follow this sequence before rendering:

1. **Position for the role.** Identify the target level and the strongest supported reason this candidate fits. Let that positioning determine section order and emphasis.
2. **Check facts and boundaries.** Make a compact working checklist of source-backed identities, dates, actions, tools, metrics, scope, and qualifiers. Treat the job description as requirements, not candidate evidence.
3. **Choose the main story.** Give the most relevant and demanding work the most space. Within an experience, lead with the technical contribution or known result that best proves the job requirements, then supporting work. Do not preserve the source's process chronology of reviews, integration, implementation, and reports. Preserve every relevant supplied experience, while combining routine or repeated detail within each experience.
4. **Write achievement bullets.** Usually write 2–4 high-information bullets per experience, adjusting to the available evidence. Each bullet conveys one main contribution in one sentence, or at most two. State the candidate's concrete action, technical method or scope, and supported result. Avoid a chronological task diary, generic claims, and repeated numbers across the summary, skills, and experience sections.
5. **Review independently against the original material.** After drafting, compare every identity label, responsibility verb, proficiency claim, date, number, and metric meaning with the source. Remove unsupported strengthening, repetition, and reviewer-facing explanations. Keep qualifications that change a metric's meaning, such as team attribution, experiment setting, or a retrieval metric's name.
6. **Render the reviewed content.** Only pass the resulting resume body to `render_resume`. Keep the working checklist, questions, risks, and revision explanations in the chat or review process, not in the printable resume.

For a current student or recent graduate with a clear target, omit the summary by default: the header and education already establish that context. Retain a summary only when a career transition, complex history, or explicit user request warrants 1–2 short positioning sentences. Do not repeat the document's tools or numbers, describe personal habits, or add self-evaluative claims such as "good at" or "possesses the ability to" in an opening paragraph.

Write the actual positive responsibility accurately: "reproduced defects, collected request evidence, and verified fixes" is preferable to filling the resume with "did not write the backend code". This is editing, not erasing boundaries: retain necessary team, environment, and measurement qualifications, and never replace a limited contribution with ownership.

## Select layout and photo

Read [references/resume-delivery.md](references/resume-delivery.md) before creating files. Follow the user's selected template and photo settings, including preferences supplied by the LazyMind composer.

- Offer or use six layouts: `business`, `modern`, `sidebar`, `editorial`, `campus`, and `minimal`. Use `business` when no choice is supplied. Use `minimal` for an explicitly requested ATS/plain layout unless the user chooses another layout.
- Support an optional real photo in every layout. Only use a photo the user explicitly provides or selects for this resume. If several images are ambiguous, ask which is the portrait. Never use a resume screenshot, certificate image, random attachment, generated face, or past candidate photo as the portrait.
- Respect an explicit "no photo" selection. Do not leave an empty photo frame when there is no photo. Default to `photo_fit="contain"` to preserve the full image; use `cover` only when the user chooses cropping.

## Generate and deliver the finished resume

1. Build one structured resume from the reviewed, edited content. Organize education, skills, experience, projects, and optional sections around the role and career stage. Use natural reverse chronology within sections. Keep the wording suitable for submission, rather than copying all source notes or audit caveats into the resume.
2. Call `render_resume` with this structured content, the selected template, and any explicitly selected photo. This tool produces the actual PDF, editable Word, and HTML files and publishes them as conversation attachments. Do not substitute Markdown, a renamed file extension, an HTML code block, or generic formatting advice for these deliverables.
3. Present the PDF first for preview and submission, then the editable Word file. Copy each returned `file_markdown` verbatim. Only describe files as ready when the tool reports that they were created successfully. If rendering fails, explain the actual failure and keep any successful outputs available; do not claim PDF or Word delivery succeeded.
4. Keep change summaries, evidence gaps, ATS notes, and interview suggestions in the chat, outside the printable resume. Do not add review instructions or unexplained placeholders to a document described as ready to submit.

The fixed renderer controls fonts, spacing, columns, photo placement, and pagination. Let long resumes paginate naturally instead of deleting content or making the text unreadably small. Keep standard section names and selectable text. Do not promise a particular ATS score or guaranteed parser compatibility.

## Revise without losing content

- For a template-only or photo-only change, reuse the last successful `resume_data_path` as `source_path` in `render_resume`. Do not rewrite or summarize the candidate's content again.
- Reuse the saved confirmed photo when appropriate; pass `include_photo=false` to remove it. Pass a new `photo_path` only for a newly confirmed replacement.
- Apply factual edits only to the facts the user changes. Preserve other content, language, numbers, dates, and contribution boundaries, then render again.
- For an explicit analysis-only request, return the requested review without forcing file generation. When a revised resume is requested or offered as a deliverable, use the renderer.
