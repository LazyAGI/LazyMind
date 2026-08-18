# API 参考

**API Base**：`https://api.skillhub.cn`，无需鉴权。查找统一走 `GET /api/skills`（关键词为**分词搜索**）；**不要**用 `/api/v1/search`。

## GET /api/skills（搜索 / 列表）

| 参数 | 说明 | 默认 |
|------|------|------|
| `keyword` | 关键词，**分词搜索**（标题/描述等命中即返回） | - |
| `category` | 一级标签 key（见 categories.md） | - |
| `source` | 来源（`community`/`enterprise`/`official`/`clawhub`） | - |
| `labels` | 属性标签过滤，`key:value` 逗号分隔，否定用 `key:!value` | - |
| `sortBy` | `updated_at`/`downloads`/`stars`/`installs`/`score` | `updated_at` |
| `order` | `asc`/`desc` | `desc` |
| `page` / `pageSize` | 分页（pageSize 1~100） | `1` / `20` |

- 找技能建议 `sortBy=score`（带 `keyword` 时启用智能打分，SkillHub 自有来源优先）；纯浏览分类用 `sortBy=downloads` 看热度。
- `labels` 常见 key：`requires_api_key`（是否需要 API Key）、`pricing_type`（`free`/`paid`）。

返回：`{"code":0,"message":"success","data":{"total":<n>,"skills":[ {...} ]}}`。每项关键字段：
`slug`、`name`、`description`/`description_zh`、`category`、`subCategories`（二级，仅展示）、`tags`、`labels`、`downloads`/`stars`/`installs`、`ownerName`、`homepage`、`version`、`source`。

### 示例

```bash
# 关键词分词搜索 + 智能排序，取前 5
curl -s "https://api.skillhub.cn/api/skills?keyword=周报&sortBy=score&pageSize=5"

# 一级标签浏览：办公效率，按下载量排序
curl -s "https://api.skillhub.cn/api/skills?category=office-efficiency&sortBy=downloads&pageSize=10"

# 分类内关键词检索
curl -s "https://api.skillhub.cn/api/skills?category=data-analysis&keyword=excel&sortBy=score&pageSize=5"

# 只看免费、不需要 API Key 的开发编程类
curl -s "https://api.skillhub.cn/api/skills?category=dev-programming&labels=pricing_type:free,requires_api_key:false&sortBy=downloads"

# 多候选词循环搜索，提取关键字段
for kw in 周报 工作汇报 "weekly report"; do
  curl -s "https://api.skillhub.cn/api/skills?keyword=${kw}&category=office-efficiency&sortBy=score&pageSize=5" \
    | jq '.data.skills[] | {name, slug, downloads, installs, category, desc: .description_zh}'
done
```

> 主页 URL 由 slug 拼接：`https://skillhub.cn/skills/<slug>`。

## 其他接口

```bash
# 单个 Skill 详情
curl -s "https://api.skillhub.cn/api/v1/skills/<slug>"

# 一级标签 / 二级类目实时列表
curl -s "https://api.skillhub.cn/api/v1/categories"
curl -s "https://api.skillhub.cn/api/v1/subcategories?parent=<一级key>"
```

技能主页统一用 `https://skillhub.cn/skills/<slug>`（如 `https://skillhub.cn/skills/wxa-skills-validate`）。不要用接口返回的 `homepage` 字段（那是 `api.skillhub.cn/<owner>/<slug>` 格式）。

## 一键安装（用户选定后）

官方方式走 **SkillHub CLI**（详见 https://skillhub.cn/install/skillhub.md）。

### 1. 检查 / 安装 CLI

```bash
command -v skillhub && skillhub --version
```

未安装则**仅装 CLI**（沙箱限网时需以允许联网权限执行）：

```bash
curl -fsSL https://skillhub-1388575217.cos.ap-guangzhou.myqcloud.com/install/install.sh | bash -s -- --cli-only
```

> 完整安装（CLI + 默认 Skill）去掉 `-s -- --cli-only`。
> 仅纯安装某技能且 CLI 已存在时，不要询问「是否设为优先源」；只有首次安装或用户明确要求时才询问一次。

### 2. 安装技能（必须用 `--dir` 指向当前 Agent 的 skills 目录）

```bash
skillhub install <slug> --dir <skills目录>
```

不加 `--dir` 会装到 `./skills/` 不被识别。常用 Agent 的 skills 目录：

| Agent | skills 目录 |
|-------|-------------|
| Cursor | `~/.cursor/skills/` |
| Claude Code | `~/.claude/skills/` |
| Codex | `~/.codex/skills/`（或项目 `.agents/skills/`） |
| Windsurf | `~/.codeium/windsurf/skills/`（或项目 `.windsurf/skills/`） |
| Gemini CLI | `~/.gemini/skills/` |
| workbuddy | `~/.workbuddy/skills/` |

- 也可用 `skillhub search <kw>` 在 CLI 内搜索。
- 安装完成后提示用户：「✅ {name} 已安装」。

> 备用（无 CLI / 仅取 zip）：下载接口 302 跳转 `GET /api/v1/download?slug=<slug>[&version=<version>]`，解压到上面对应的 skills 目录。
