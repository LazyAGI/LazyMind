# 技能广场中文展示

本次覆盖普通技能广场卡片、只读广场详情、搜索、标签和响应式布局。现有 89 个内置广场技能已补齐中文名称、简介和 79 个标签／分类显示词。已安装技能的编辑与运行、私有云资源及 `SKILL.md` 全文翻译不在本次范围。

## 数据边界

- `name`、`description`、`tags`、UID、文件、版本及包散列仍表示原始技能。安装、升级和模型技能发现继续使用它们。
- `display_name`、`display_description`、`display_locale` 和 `tag_labels` 只供广场展示。`search_text` 提供跨语言检索内容，确保英文界面也能搜索中文名称。
- 标签显示中文 label，筛选和图标仍使用原始 value。
- 卡片对完整中文简介做两行截断；只读详情显示完整简介。界面切换语言时会刷新内置展示缓存。

## 维护内置技能

`skills/builtin-sources.yaml` 中每个普通来源维护独立且唯一的 `presentation_id`。即使更新上游版本导致 UID 变化，也应保留该展示 ID。不要使用可能重复的原始目录 key 代替它。

`skills/marketplace/locales.yaml` 保存中文名称、简介和标签词典：

```yaml
schema_version: 1
locales:
  zh-CN:
    tags:
      research: 研究调研
    skills:
      deep-research:
        name: 深度调研
        description: 针对明确的深度研究需求开展多来源调查、系统比较和综合分析，整理可追溯的研究结论。
```

新增普通技能时同时添加展示 ID、中文文案和所需标签。简介应说明用户能完成什么，避免把面向模型的触发策略直接粘贴到卡片中。保留产品名，依据原包能力写文案。

构建器分别输出原始 `catalog.json` 与独立 `presentation.json`。两种构建模式都会生成展示文件；`--catalog-only` 不为翻译下载远程包。来源缺少 ID、译文缺失或绑定重复会在编译时失败。旧目录没有展示文件时回退原始文案。

## 管理员上架

上架表单要求填写中文展示名称和简介，URL 与 ZIP 导入共用该表单。服务端将它们保存到市场条目的 `display_i18n`，不写入源技能 revision 或 ZIP。旧客户端仍可省略新增字段，已有条目缺少中文时回退原始文案。

管理员编辑接口的 `display_name`、`display_description` 和 `tag_labels` 只更新市场展示资料。原来的 `name`、`description`、`source` 编辑仍表示修改源技能。已安装副本独立存在，不随展示文案修改而改变。

管理员市场搜索在计算总数、分页之前匹配原名、原简介及翻译；内置技能在前端使用同样的双语匹配语义。

## 验证记录（2026-10-22）

- 前端 8 个相关测试文件、50 项测试通过。
- Go 的 bundler、builtin、market、handler、service、distribution、showcase 7 个相关包通过；新增 OpenAPI 契约测试通过。
- PostgreSQL 与 SQLite 的新增迁移 up/down、已有行保留、完整 dev／aggregate 路径验证通过。
- Skill lock 验证模式的 6 项 Python 测试通过。
- OpenAPI 客户端 freshness 检查、Vite 生产构建通过。客户端使用官方 OpenAPI Generator Docker 镜像生成，并运行仓库自带后处理与缓存更新。
- 离线构建生成 131 个原始技能预览、89 份中文展示资料及 48 个精选能力，不下载远程技能。按 UID 对比，原始目录条目与 lock 一致；原 lock 文件字节未变。
- Playwright 使用真实卡片、工具栏、导航组件和实际 89 份展示数据的隔离夹具，检查 1440px、1140px、1440px 窗口内的 760px 容器、390px 四种情况。列数分别为 4／2／2／1；标题、推荐标题和卡片左边缘一致；左右空白为 24／24／24／16px，未发现横向溢出或控件裁切。中文搜索和完整简介展示通过。该检查不代替带账号的完整应用端到端验收。

仓库已有两类基线问题未纳入本次修改：全量 TypeScript 检查在基线与本分支均有相同的 197 条诊断；`common` 的两个错误目录检查在基线与本分支均报告相同的模型供应商未登记错误。与基线对比，本次没有新增这些诊断。
