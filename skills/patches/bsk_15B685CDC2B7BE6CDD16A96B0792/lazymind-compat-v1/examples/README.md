# 可独立复现的知识笔记

这些材料是自包含演示输入，已由 Codex 按 karpathy-llm-wiki 执行；无需网页、私有数据或前文。

## 项目问答

使用 project-qa/prompt.txt 的完整提示词。project-qa/knowledge-wiki/ 是实际生成的知识库：sources/ 保留3份原始笔记；wiki/ 包含范围、验收、运行、答案4页，以及索引和追加式日志。请勿修改原始来源。需要新版本时新增来源，再维护知识层。

## 现有 Wiki 审计

使用 wiki-audit/prompt.txt 的完整提示词，它包含全部种子文件文本。也可以将 wiki-audit/before/ 复制到一个新的 knowledge-wiki/ 目录作为输入，不要把审计后的日志当作旧日志重复使用。wiki-audit/knowledge-wiki/ 是审计后的状态；内容页和索引故意保留缺陷，只有日志追加。wiki-audit/audit-report.md 是审计报告，不是修复结果。

## 查看结果

对应 assets/project-qa.html 与 assets/wiki-audit.html 为无需网络的知识笔记预览，可下载 Markdown 答案或报告及 JSON 文件集合。JSON 键是文件相对路径，值是完整原文；本 examples/ 目录已经提供展开的文件。复用时将对应 knowledge-wiki/ 放到新的可访问工作区；不要依赖跨会话自动保存。
