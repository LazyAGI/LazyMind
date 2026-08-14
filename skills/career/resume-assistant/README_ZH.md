
<p align="center">
  <h1 align="center">📝 简历助手 (Resume / CV Assistant)</h1>
  <p align="center">
    <strong>AI 驱动的 clawbot 技能 —— 简历（Resume / CV）润色、职位定制、多格式导出、专业评分，一站式搞定。</strong>
  </p>
  <p align="center">
    <a href="./LICENSE"><img src="https://img.shields.io/badge/许可证-MIT-blue.svg" alt="许可证: MIT"></a>
    <img src="https://img.shields.io/badge/版本-1.0.0-green.svg" alt="版本: 1.0.0">
    <img src="https://img.shields.io/badge/平台-clawbot-purple.svg" alt="平台: clawbot">
    <img src="https://img.shields.io/badge/语言-中文%20%7C%20English-orange.svg" alt="语言: 中文 | English">
  </p>
  <p align="center">
    <a href="./README.md">🇺🇸 English</a> · <a href="./SKILL_ZH.md">📖 技能规范</a> · <a href="./examples/usage.md">💡 使用示例</a>
  </p>
</p>

---

## ✨ 核心功能

| 功能 | 描述 |
|------|------|
| 🔍 **简历润色** | 40+ 项检查清单，涵盖语法、动词、量化指标、排版等全方位审查 |
| 🎯 **职位定制** | 针对目标职位进行差距分析和关键词优化，提升匹配度 |
| 📤 **多格式导出** | 支持 5 种格式（Word、Markdown、HTML、LaTeX、PDF）× 4 种专业模板 |
| 📊 **专业评分** | 100 分制 5 维度评估，明确优势、短板和改进计划 |

---

## 🚀 快速开始

### 直接对话即可！

不需要记忆任何命令 —— 直接描述你的需求：

```
💬 "帮我写一份软件工程师的简历"
💬 "润色我的简历，修复所有问题"
💬 "优化我的简历，让它通过 ATS 筛选"
💬 "根据这个职位描述定制我的简历：[粘贴 JD]"
💬 "把我的简历转成 PDF"
💬 "给我的简历打个分，告诉我怎么改进"
💬 "我的简历有什么问题？"
💬 "这是我的简历，帮我看看"
```

English works too:
```
💬 "Create a resume for a software engineer position"
💬 "Optimize my resume for ATS"
💬 "Tailor my resume for this job description: [paste JD]"
💬 "Score my resume and tell me how to improve"
```

助手会理解你的意图，自动路由到正确的工作流：

| 你说 | 助手做什么 |
|------|-----------|
| "帮我写一份 [职位] 的简历" | 询问你的背景信息 → 生成定制简历 |
| "润色 / 修改 / 优化我的简历" | 执行 40+ 项检查 → 返回润色后的版本 |
| "优化 ATS" | 检查 ATS 兼容性 → 优化关键词和格式 |
| "根据这个 JD 定制：..." | 分析 JD → 差距分析 → 定制简历 |
| "转成 PDF / Word / ..." | 用专业模板导出为指定格式 |
| "给简历打分 / 评估 / 怎么样" | 100 分制评分 → 优势与改进方案 |
| "这是我的简历，帮我看看" | 先评分 → 再建议后续步骤 |

### 斜杠命令（精确控制）

如果需要更精细的控制，也可以直接使用斜杠命令：

```
/resume polish
请帮我润色以下简历：

张三
高级前端工程师 | 5年经验
技能：JavaScript, React, Vue, Node.js
...
```

### 命令一览

| 命令 | 功能 | 必需输入 |
|------|------|----------|
| `/resume polish` | 润色简历，修复错误 | 简历内容 |
| `/resume customize` | 针对目标职位定制 | 简历内容 + 职位描述 |
| `/resume export` | 导出为 Word/MD/HTML/LaTeX/PDF | 简历内容 + 目标格式 |
| `/resume score` | 评分并给出改进方案 | 简历内容 |

---

## 📖 工作原理

简历助手是一个 **clawbot 技能（skill）**—— 一套结构化的提示词包，可以被任何 AI Agent 加载和执行。它支持 Resume 和 CV 两种格式，包含：

- **角色定义文件** —— 设定 AI 的角色与质量标准
- **命令提示词** —— 每个功能的详细指令
- **模板文件** —— 简历样式和导出格式模板
- **技能清单** （`skill.json` / `skill.yaml`）—— 描述命令、参数和配置

```
用户输入 ─┬─ 斜杠命令 ──────────► 命令路由 ──► 加载提示词 ──► LLM 处理 ──► 结构化输出
          │                          │
          └─ 自然语言 ──► 意图识别 ──┘
                                     │
              ├── polish / "润色简历"     → prompts/polish.md
              ├── customize / "定制简历"  → prompts/customize.md
              ├── export / "转成PDF"     → prompts/export.md
              └── score / "给简历打分"    → prompts/score.md
```

---

## 🏗️ 项目结构

```
resume-assistant/
├── skill.json              # 技能清单（JSON 格式）
├── skill.yaml              # 技能清单（YAML 格式）
├── SKILL.md                # 技能规范（英文）
├── SKILL_ZH.md             # 技能规范（中文）
├── LICENSE                 # MIT 开源协议
│
├── prompts/                # AI 提示词文件
│   ├── system.md           # 系统级提示词（始终加载）
│   ├── polish.md           # 润色命令提示词
│   ├── customize.md        # 定制命令提示词
│   ├── export.md           # 导出命令提示词
│   └── score.md            # 评分命令提示词
│
├── templates/              # 简历和导出模板
│   ├── professional.md     # 经典专业风格
│   ├── modern.md           # 现代创意风格
│   ├── minimal.md          # 简洁极简风格
│   ├── academic.md         # 学术研究风格
│   └── export/
│       ├── resume.html     # HTML 导出模板
│       └── resume.tex      # LaTeX 导出模板
│
└── examples/               # 示例简历和使用指南
    ├── usage.md            # 详细使用示例
    ├── sample-resume-en.md # 英文示例简历
    ├── sample-resume-zh.md # 中文示例简历
    └── sample-resume-weak.md # 弱简历示例（用于评分演示）
```

---

## 🔧 集成指南

### 方式一：在 AI Agent 中注册为技能

```json
{
  "skills": [
    {
      "name": "resume-assistant",
      "path": "./skills/resume-assistant",
      "manifest": "skill.json"
    }
  ]
}
```

### 方式二：通过代码构建提示词

```python
ROLE_SYS = "system"    # LLM message role constant
ROLE_USR = "user"      # LLM message role constant

def build_prompt(command, args):
    persona_prompt = load_file("prompts/system.md")
    command_prompt = load_file(f"prompts/{command}.md")

    combined = persona_prompt + "\n\n" + command_prompt
    messages = [
        {"role": ROLE_SYS, "content": combined},
        {"role": ROLE_USR, "content": args["resume_content"]}
    ]
    return messages
```

### 方式三：通过 REST API 调用

```bash
curl -X POST https://your-agent-api.com/skills/resume-assistant/polish \
  -H "Content-Type: application/json" \
  -d '{
    "resume_content": "你的简历内容...",
    "language": "zh"
  }'
```

### 方式四：在 LangChain / LlamaIndex 中使用

```python
from langchain.tools import Tool

resume_tools = [
    Tool(
        name="resume_polish",
        description="润色和优化简历，40+ 项检查清单",
        func=lambda input: agent.run_skill(
            "resume-assistant", "polish",
            {"resume_content": input, "language": "zh"}
        )
    ),
    # ... 更多工具：score, customize, export
]
```

> 📖 完整集成细节请查看 [技能规范文档](./SKILL_ZH.md)

---

## 📋 推荐工作流

```
┌─────────────────┐
│    从这里开始     │
└────────┬────────┘
         ▼
   已有简历？ ──是──► /resume score（了解当前水平）
         │                     │
         否                    ▼
         │             /resume polish（修复问题）
         ▼                     │
  用模板写一份                  ▼
         │             有目标职位？
         ▼                │        │
  /resume polish          是       否
         │                │        │
         │                ▼        │
         │        /resume customize│
         │                │        │
         └────────────────┼────────┘
                          ▼
                   /resume export ──► Word / Markdown / HTML / LaTeX / PDF
```

**实用建议：**

1. 🎯 **先评分** —— 如果已有简历，先了解现状
2. ✨ **先润色** —— 先修复基础问题，再做职位定制
3. 🔄 **每个职位都定制** —— 不要一份简历投所有岗位
4. 📊 **再次评分** —— 润色和定制后检查提升效果
5. 📤 **最后导出** —— 先把内容打磨好，再处理格式
6. 📝 **用 Markdown 工作** —— 可以无缝转换为所有其他格式

---

## 🎨 模板风格

| 模板 | 风格 | 适用场景 |
|------|------|----------|
| `professional` | 经典深蓝，衬线标题 | 金融、咨询、法务 |
| `modern` | 青色强调，创意布局 | 科技、初创、市场 |
| `minimal` | 简洁单色，内容紧凑 | 资深岗位、工程师 |
| `academic` | 正式衬线，支持多页 | 高校教职、科研、博士 |

---

## 🌏 语言支持

- **英语 (en)** —— 默认语言，针对国际求职市场优化
- **中文 (zh)** —— 完整 CJK 支持，适配中国求职习惯

中文特色功能：
- 📸 HTML/LaTeX 导出中的 CJK 字体处理
- 🇨🇳 中国简历惯例（照片、年龄、学历优先展示）
- ✏️ 半角/全角标点符号自动规范化
- 🔤 中英文混排间距优化

---

## 📊 评分维度

使用 `/resume score` 时，你的简历将从 5 个维度进行评估：

| 维度 | 权重 | 评估内容 |
|------|------|----------|
| 内容质量 | 30 分 | 成果展示、量化指标、相关性 |
| 结构与排版 | 25 分 | 布局层次、留白、视觉效果 |
| 语言与语法 | 20 分 | 动词使用、时态、语法规范 |
| ATS 优化 | 15 分 | 关键词、可解析性、标准标题 |
| 影响力与印象 | 10 分 | 整体印象、独特价值主张 |

评级标准：**A+ (90-100)** · **A (80-89)** · **B (70-79)** · **C (60-69)** · **D (50-59)** · **F (<50)**

---

## 🤝 参与贡献

欢迎贡献！你可以通过以下方式参与：

- 🐛 通过 Issues 报告问题或建议新功能
- 📝 优化提示词以提升 AI 输出质量
- 🎨 添加新的简历模板
- 🌍 增加更多语言支持
- 📖 完善文档

---

## 📄 开源协议

本项目基于 [MIT 许可证](./LICENSE) 开源。
