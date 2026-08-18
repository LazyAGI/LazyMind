---
name: find-skill-skillhub
description: "在 SkillHub 平台查找/搜索 Skill 技能。基于 skills 列表接口，支持关键词分词搜索、一级标签（一级分类）筛选、以及二者组合检索。当用户说『找个 xxx 技能』『有没有处理 PDF 的 skill』『SkillHub 上搜一下 xxx』『按分类看技能』『办公效率类有哪些技能』『推荐一个做数据分析的 skill』『这个需求有现成技能吗』等需要在 SkillHub 上发现/检索/推荐 Skill 的场景时使用本技能。"
---

# 在 SkillHub 查找 Skill

通过公开接口 `GET https://api.skillhub.cn/api/skills` 查找平台 Skill（无需鉴权，关键词为**分词搜索**，**不要**用 `/api/v1/search`）。

接口参数 / 返回字段 / 更多示例 / 安装命令见 [references/api.md](references/api.md)。

## 核心流程

关键词分词召回有限，必须走完五步，**不要拿用户原话搜一次就结束**。

### Step 1 · 理解场景
从用户自然语言中提取：**任务意图** + **领域标签**（映射到 `category`）+ **中英文关键词**（同义/上位词扩展，2~4 个）。
- 例："帮我自动写周报发给老板" → `office-efficiency`；`周报`/`工作汇报`/`日报`/`weekly report`

### Step 2 · 多次搜索
每个候选词调 `GET /api/skills?keyword=..&sortBy=score`（必要时叠加 `category`/`labels`），合并结果并按 `slug` 去重。

```bash
curl -s "https://api.skillhub.cn/api/skills?keyword=周报&sortBy=score&pageSize=5" \
  | jq '.data.skills[] | {name, slug, downloads, installs, desc: .description_zh}'
```

### Step 3 · 意图匹配排序
不要把原始列表直接丢给用户。结合 `name`+`description` 判断契合度，**过滤不相关项**，挑出**最契合的 3~5 个**：契合度优先，同档按热度（`downloads`/`installs`）降序。命中过多→`category`/`labels` 收窄；为空→去掉 `category`、换同义/上位词放宽。

### Step 4 · 输出推荐
统一格式，每条必须给**匹配理由**，并请用户选择。**只能输出下列字段**，⛔ 严禁在推荐列表里出现安装命令 / `curl` / `skillhub install` / `sh -c` 等任何命令行或代码块——这是给用户看的，命令属于 Step 5 且由你执行而非展示：

```
🔍 为你找到 {N} 个相关技能：

1. {name} — {一句话用途（description_zh 优先）}
   匹配理由：{为什么适合这个场景}
   分类：{category 中文名} | 下载：{downloads} | 安装：{installs}
   主页：https://skillhub.cn/skills/{slug}

需要我帮你安装第几个？（回复"安装第N个"即可；都不合适我再换词搜）
```

### Step 5 · 一键安装
**用户选定后**才进入本步，且命令由你**直接执行**（不要把命令行贴给用户看）：`command -v skillhub` 检查 → 未装则 `install.sh --cli-only` 仅装 CLI → `skillhub install <slug> --dir <当前 Agent 的 skills 目录>`（必须带 `--dir`，否则不被识别）。装完只回一句「✅ {name} 已安装」。各 Agent 目录与备用 zip 方式见 [references/api.md](references/api.md#一键安装用户选定后)。

## 一级标签（category）

12 个一级标签（`?category=<key>`），映射意图时按需打开对应详情文件（渐进式披露）。完整索引与说明见 [references/categories.md](references/categories.md)：

`office-efficiency` 办公效率 · `content-creation` 内容创作 · `dev-programming` 开发编程 · `data-analysis` 数据分析 · `design-media` 设计多媒体 · `ai-agent` AI Agent · `knowledge-management` 知识管理 · `business-ops` 商业运营 · `education` 教育学习 · `professional` 行业专业 · `it-ops-security` IT 运维与安全 · `life-service` 生活服务
