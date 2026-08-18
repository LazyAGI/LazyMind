# 一级标签（category）总览

平台一期标签体系共 **12 个一级标签**，用 `?category=<key>` 过滤。每个标签的覆盖范围、边界与其下 8 个二级类目（共 96 个）按类别拆分，**映射意图 / 推断 `category` 时按需打开对应文件**（二级类目中文名也是很好的 `keyword` 候选词）。

| key | 中文 | English | 详情 |
|-----|------|---------|------|
| `office-efficiency` | 办公效率 | Office Efficiency | [详情](office-efficiency.md) |
| `content-creation` | 内容创作 | Content Creation | [详情](content-creation.md) |
| `dev-programming` | 开发编程 | Development | [详情](dev-programming.md) |
| `data-analysis` | 数据分析 | Data Analysis | [详情](data-analysis.md) |
| `design-media` | 设计多媒体 | Design & Media | [详情](design-media.md) |
| `ai-agent` | AI Agent | AI Agent | [详情](ai-agent.md) |
| `knowledge-management` | 知识管理 | Knowledge Management | [详情](knowledge-management.md) |
| `business-ops` | 商业运营 | Business Operations | [详情](business-ops.md) |
| `education` | 教育学习 | Education | [详情](education.md) |
| `professional` | 行业专业 | Professional | [详情](professional.md) |
| `it-ops-security` | IT 运维与安全 | IT Ops & Security | [详情](it-ops-security.md) |
| `life-service` | 生活服务 | Life Service | [详情](life-service.md) |

> key 不区分大小写，`_`/`-` 等价；旧 key（如 `ai-intelligence`、`productivity`）会自动归一化。
> 实时列表：`curl -s "https://api.skillhub.cn/api/v1/categories"`、`curl -s "https://api.skillhub.cn/api/v1/subcategories?parent=<一级key>"`。
