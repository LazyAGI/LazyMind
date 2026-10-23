# 论文知识库 Zotero 模型设计

## 目标
将论文知识库从“文件列表”升级为“文献条目库”：Paper 是核心实体，PDF 与其他文件是可关联的附件；导入、去重、笔记、批注、集合、标签和导出围绕 Paper 工作。

## 数据模型
- `Paper`: 元数据、标识符、摘要、来源、字段置信度、阅读状态、重要性。
- `Attachment`: 文件名、类型、来源、大小、关联 Paper、主附件标记。
- `Note`: 关联 Paper 的独立文字记录。
- `Annotation`: 关联 Paper 与 Attachment 的页码、引用文字、评论和颜色。
- `Collection`: 组织 Paper 的集合关系。
- `Tag`: Paper 与标签的多对多关系。
- `PaperRelation`: 相关、引用、被引用等关系。
- `MergeSnapshot`: 合并前可撤销快照。

现有旧论文数据通过版本化迁移升级为 Paper + Attachment 结构，保留旧字段读取能力。

## Translator
所有入口输出统一的 `CanonicalPaperDraft`：
- Search：DOI、arXiv。
- Import：BibTeX、RIS、CSV、PDF。
- Web：网页 URL 的通用元数据解析，并预留站点适配器。
- Export：BibTeX、RIS、CSL JSON，以及完整库备份 JSON。

PDF 识别失败时仍创建待补全文献和附件，不阻塞导入。

## 去重与合并
优先匹配 DOI/arXiv，其次用标题、作者、年份生成潜在重复建议。用户选择主条目后合并元数据、标签、集合、附件、笔记和关系，写入快照并支持撤销最近合并。

## UI
导入面板采用上传文件主入口、DOI/arXiv 快速入口、外部来源折叠入口。列表按 Paper 展示附件/笔记摘要；详情视图分为概览、附件、笔记、批注、关系。导出分为引用交换和完整库备份。

## 验证
验证旧数据迁移、导入预览、失败保留附件、重复建议、合并撤销、导入面板视觉层级、引用导出和完整库备份。
