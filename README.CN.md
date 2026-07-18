# LazyMind

**[English](README.md)** | **中文**

> **让 AI 按照你的资料、标准和偏好，稳定完成真实任务。**

LazyMind 是面向知识密集型任务的 **AI Skill Runtime**。它把少数深度用户手动组织模型、资料、Skill、工具和流程的经验，封装成普通用户可以直接使用的产品。

你不必反复上传资料、调 Prompt、配置 CLI 或全程盯着 Agent：LazyMind 会基于你的知识推进任务，展示每一步和中间产物，并从反馈中持续贴近你的交付标准。既可以通过 **Desktop Mode** 在本机使用，也可以部署为团队共享的企业服务。

---

## 为什么需要 LazyMind？

真实任务一旦超出简单问答，用户通常会遇到四个障碍：

- **资料散落**：项目材料、历史报告和业务知识分布在本地文件与多个协作平台，每次都要重新寻找、上传和解释。
- **标准难以沉淀**：AI 能生成内容，却不了解业务口径、表达偏好、格式要求和交付边界，结果总要反复修改。
- **长任务容易失控**：资料和工具一多，Agent 就可能跳步、漏掉约束或中途跑偏，用户只能全程监督。
- **高阶能力门槛太高**：模型、Skill、插件和工作流都在快速发展，但组合、配置与长期维护仍然依赖少数深度用户。

LazyMind 用三个相互配合的系统吸收这些复杂度：

| 系统 | 解决的问题 | 用户得到什么 |
|------|------------|--------------|
| **知识底座** | AI 看得到什么 | 资料无需反复搬运，任务可以持续调用自己的知识并追溯原文 |
| **状态大脑** | 长任务怎么稳定跑 | 过程可见、结果可改、异常可恢复，关键决策仍由用户掌控 |
| **AI 成长引擎** | 下一次能不能更好 | 偏好、术语、反馈和评测结果持续沉淀，系统越用越贴合标准 |

三者共同组成产品化的 Skill Runtime：不是再提供一个 AI 入口，而是把模型能力稳定转化为交付结果。

---

## 核心亮点

### 1. 从“问一个问题”到“交付一个结果”

选择自己的知识和一个 Skill，LazyMind 会继续完成资料整理、结构规划、内容生成、检查与交付，而不是在给出一段回答后停下来。

仓库内置的场景包括：

- **AI Writer**：整理素材 → 生成大纲 → 分章节写作 → 局部修改 → 全文审阅 → 最终稿
- **AI Image**：理解需求 → 收集参考 → 优化 Prompt → 生成或编辑图片，也可以制作动态表情包

**How：** 本地目录、对象存储、飞书和 Notion 等数据源进入统一知识库；PDFReader、MinerU 或 PaddleOCR-VL 完成解析，再通过多路 Embedding、混合检索和重排为 Agent 提供依据。

**Why：** 模型能力不应停留在聊天窗口。只有连接真实资料与交付流程，AI 才能从“看起来会做”走向“真正把事情做完”。

![数据源管理 — 新建数据源](docs/assets/datasource-create.png)

### 2. 不用全程盯着 Agent，但始终保有掌控感

长任务会持续显示当前状态、工具调用、耗时和中间结果。用户可以在关键节点审批，直接修改 Artifact，或者从出问题的步骤重新执行，而不必推倒重来。

**How：** Plugin 使用状态机定义步骤、工具、输入输出和流转条件；Artifact 保留版本，Driver 负责自动验收，并根据结果继续、重试或回退。

**Why：** 复杂 Agent 最难接受的不是偶尔失败，而是过程不可见、错误不可控。LazyMind 把黑盒执行变成用户与 Agent 可以共同完成的工作台。

### 3. 把少数人的 AI 使用经验，变成人人可用的能力

一个好用的调研方法、写作流程或行业经验，不必永远停留在个人 Prompt、脚本和配置里。它可以作为 Skill 管理，也可以进一步变成团队反复使用的可执行 Plugin。

**How：** LazyMind 可以分析 Skill 的文件、脚本和工具依赖，判断它是否适合生成工作流，映射平台工具并生成 Plugin；生成后支持诊断、修复、发布、版本追踪和回滚。

**Why：** Prompt 解决一次问题，稳定工作流解决一类问题。LazyMind 让 AI 生产力不再依赖某个会搭环境、会调工具的人，而是成为可复用、可追溯的团队资产。

插件格式与开发方式见 [插件格式规范](docs/plugin-format.md)。

### 4. 下载到本机就能用，敏感知识无需离开设备

个人用户和小团队可以通过 Desktop Mode 在本机管理知识、运行 Agent，不必先搭建完整的云端基础设施；需要多人共享时，再切换到企业服务栈。

**How：** 本地模式使用原生进程、SQLite 和 Milvus Lite，并统一管理 Go、Python、Node 服务及应用数据目录；当前提供 macOS arm64 桌面构建和 Windows x64 ZIP/安装包构建。

**Why：** 知识管理产品的第一道门槛往往是部署与数据安全。Desktop Mode 降低体验成本，也让本地资料和运行数据保持在用户掌控之中。

团队部署支持 Kong、JWT/RBAC、Core ACL、外部 Milvus/OpenSearch 和私有化 OCR。详情见 [Desktop 文档](desktop/README.md)。

### 5. 每一个人工干预和差评，都能帮助下一次做得更好

用户改过的内容、否定过的答案、补充过的规则和给出的差评，不应该随着一次对话结束而消失。LazyMind 会把这些信号转化为可以审核、复用和验证的成长素材，让系统逐渐理解用户偏好，也让同一类问题持续得到修复。

这个成长过程包含两个相互配合的闭环：

- **智积阅累——沉淀“用户想要什么”**：将人工修改、评价和历史使用中形成的偏好、术语、经验与 Skill 作为可运营资产统一管理，支持审核、版本追踪和回滚。用户不必在每次任务中重新说明口径、习惯与交付标准。
- **evo——验证“系统怎样做得更好”**：将差评和 badcase 变成评测样例，定位知识、召回、Prompt、工具或算法策略中的问题，再运行“基线评测 → 生成修复方案 → A/B Test → 合并与部署”，用数据确认改动是否真的有效。

**How：** 一次人工干预既可以成为智积阅累中的长期记忆与规则，也可以进入 evo 的评测和优化链路；两个闭环都保留来源、版本和验证过程，并允许用户在关键节点审阅。

**Why：** 真正的自进化不是模型擅自改变，而是系统能够记住人的标准、找到失败原因，并在验证后采用更好的方案。每一次使用因此不再是孤立任务，而是在为下一次交付积累知识和证据。

![智积阅累 — Skill、词表与工具](docs/assets/knowledge-ops.png)

![evo 自进化执行路径](docs/assets/evo-pipeline.png)

![evo 实时执行编排](docs/assets/evo-run.png)

---

## 快速开始

### 本机运行

前置条件：Go、Python 3、uv、pnpm 和 Node.js。

```bash
make local-up
```

Windows PowerShell 使用：

```powershell
make local-win-up
```

启动后访问：

- LazyMind：http://localhost:8090
- API 文档：http://localhost:8090/docs.html
- 默认账号：`admin` / `admin`

登录后在模型设置中配置 LLM、Embedding、Reranker，以及按需配置 VLM、图片或视频模型。高质量 PDF 解析可额外配置 MinerU API Key：

```bash
export LAZYLLM_MINERU_API_KEY=你的_mineru_key
```

停止本地运行：

```bash
make local-down
```

Windows 使用 `make local-win-down`。完整配置见 [快速开始](docs/quick_start.CN.md)。

### 构建桌面应用

| 平台 | 命令 | 产物 |
|------|------|------|
| macOS arm64 | `make desktop-darwin-arm64` | macOS 桌面应用 |
| Windows x64 | `make desktop-windows-x64` | 便携 ZIP |
| Windows x64 | `make desktop-windows-x64-installer` | 安装程序 |

### 容器部署

```bash
make up
```

常用部署选项：

### macOS 使用 Colima 启动容器栈

如果本机无法使用 Docker Desktop，可以使用 [Colima](https://github.com/abiosoft/colima) 提供 Docker 运行环境。未安装 Homebrew 时，先按官方方式安装：

```bash
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
```

安装完成后按终端提示配置 `PATH`。Apple Silicon 通常使用：

```bash
echo 'eval "$(/opt/homebrew/bin/brew shellenv)"' >> ~/.zprofile
eval "$(/opt/homebrew/bin/brew shellenv)"
brew --version
```

安装 Colima 和 Docker 命令行工具：

```bash
brew install colima docker docker-compose docker-buildx
```

启动 Colima：

```bash
colima start --runtime docker --vm-type vz --mount-type virtiofs --cpu 4 --memory 6 --disk 80
```

验证环境：

```bash
colima status
docker version
docker compose version
```

环境就绪后，在项目根目录启动容器栈：

```bash
make up
```

使用完毕后，先停掉容器栈，再关闭 Colima：

```bash
make down
colima stop
```

### 启动命令速查

除 Colima 容器栈外，项目还支持宿主机本地运行等多种方式，常用命令如下：

| 场景 | 命令 |
|------|------|
| 构建镜像并启动 | `make up-build` |
| 私有化 MinerU OCR | `make up LAZYMIND_DEPLOY_MINERU=1` |
| 私有化 PaddleOCR | `make up LAZYMIND_DEPLOY_PADDLEOCR=1` |
| 外接 Milvus/OpenSearch | `make up LAZYMIND_MILVUS_URI=http://your-milvus:19530 LAZYMIND_OPENSEARCH_URI=https://your-opensearch:9200` |

服务架构、环境变量和鉴权链路见 [架构文档](docs/architecture.md)。

---

## 当前已具备的能力

| 领域 | 当前能力 |
|------|----------|
| 知识库 | 多数据源、OCR、向量化、混合检索、重排、同步管理 |
| Agent | RAG 对话、工具调用、子任务、Artifact、任务中心 |
| Plugin | 状态机、动态路由、自动验收、重试/回退、可视化执行、版本化产物 |
| Skill | 安装、组织、审核、版本、回滚、Skill → Plugin |
| 自进化 | 评测集、评测、badcase 分析、修复、部署、A/B Test |
| 本地体验 | macOS/Windows 本地运行时、Desktop 构建、平台规范数据目录 |
| 企业能力 | Kong、JWT/RBAC、ACL、OAuth 数据源、可选外部存储 |

这份列表描述的是仓库中已经实现的能力，不是未来 Roadmap。具体模块的设计与实现状态见 [docs](docs/)。

---

## Roadmap

LazyMind 接下来的重点不是继续堆叠孤立功能，而是让知识库、Skill、Plugin 和自进化能力在真实任务中形成完整闭环。

### 近期：打磨可直接体验的旗舰场景

- **知识到交付物**：围绕客户解决方案、产品手册和产品调研，提供从知识检索、结构规划、分段生成到审阅交付的完整流程。
- **更好的局部修改**：支持选区改写、基于知识库补充、Diff、接受/拒绝修改，以及从受影响步骤局部重跑。
- **结果交付**：完善 Markdown、DOCX、PDF 导出和可分享结果页，优先支持飞书、Notion 等内容发布目标。
- **开箱即用的 Demo**：提供示例知识包、任务模板和完成结果，让新用户无需准备私有数据即可体验完整工作流。
- **Desktop 体验**：继续降低安装、模型配置、数据导入和本地运行时诊断成本。

### 中期：建设知识与能力分发网络

- **知识库与 Skill/Plugin 广场**：支持精选内容发现、一键安装、版本更新、依赖检查和可信来源展示。
- **可复用场景模板**：将流程、知识包、审阅规则和输出格式组合成可安装的行业方案。
- **外部 Agent 接入**：通过 MCP、CLI、OpenAPI 和 SDK，让 Codex、Cursor、Hermes Agent、OpenClaw 等使用 LazyMind 的知识与工作流能力。
- **更多数据连接器**：围绕周报、调研和内容生产，逐步接入协作、邮件、日历、代码和任务系统。
- **团队协作**：增强工作流分享、审批、权限、运行记录和组织级模板治理。

### 长期：从执行工作流走向自进化工作系统

- 根据用户修改、步骤重跑、知识引用和最终采纳结果，自动发现流程与知识缺口。
- 对检索策略、Prompt、模型、工具和 Plugin 版本进行持续评测与 A/B Test。
- 将成功经验沉淀为可复用的 Skill、模板和组织记忆，并保留完整来源与版本记录。
- 通过“横向任务模板 + 纵向行业知识包”覆盖更多行业，而不是为每个行业重复开发产品。

Roadmap 会根据真实场景的完成率、结果质量、人工干预次数、执行时间和成本持续调整；具体版本内容以仓库 Issue、里程碑和发布说明为准。

---

## 项目结构

```text
LazyMind/
├── frontend/                   # Web UI 与桌面前端
├── backend/
│   ├── auth-service/           # 鉴权、OAuth 与用户服务
│   ├── core/                   # 数据、任务、检索、Plugin 与 ACL
│   └── scan-control-plane/     # 数据源扫描与同步控制
├── algorithm/
│   └── lazymind/               # 对话、解析、检索与 Agent 运行时
├── plugins/                    # 内置 Plugin
├── skills/                     # 内置及精选 Skill
├── evo/                        # 自进化与评测闭环
├── desktop/                    # Electron 桌面应用与打包
├── local/                      # 本地运行时管理
├── api/                        # OpenAPI 规范
├── docs/                       # 架构、使用与设计文档
└── tests/                      # 跨服务测试
```

---

## 开发与测试

```bash
make lint              # Python + Go + 文档等静态检查
make lint-only-diff    # 只检查变更文件
make test              # 使用宿主机环境运行测试
make test-hermetic     # 使用项目管理的隔离环境运行同范围测试
```

- Python 3.11+
- Go 1.24.0
- Node.js 20
- OpenAPI 规范集中维护在 `api/`

---

## License

见 [LICENSE](LICENSE)。
