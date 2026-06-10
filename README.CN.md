# LazyMind

**[English](README.md)** | **中文**

> **企业级 RAG 知识库平台，内置自进化能力** — 不只是搭一个问答系统，而是让它能自己发现问题、自己修复、自己验证效果。

---

## 这是什么？

LazyMind 是一个**开箱即用的企业知识库 + RAG 对话平台**，同时内置了一套**自动化 RAG 质量优化闭环（evo）**。

你可以用它：

- 接入本地文件、飞书文档等多种数据源，构建企业知识库
- 提供基于知识库的 RAG 对话能力，支持多路召回与重排
- 通过**智积阅累**模块管理技能、词表、使用习惯等运营资产
- 用 **evo 自进化模块**自动评测 RAG 效果、分析 badcase、生成代码优化方案、A/B 测试验证收益，形成完整的质量提升闭环

---

## 核心亮点

### 1. RAG 自进化闭环（evo）

这是 LazyMind 最独特的能力。传统 RAG 系统上线后质量好不好全靠人工排查，evo 模块让系统**自己跑通优化流程**：

```
生成评测集 → 首轮评测 → 分析 badcase → 生成代码优化方案 → A/B 测试验证 → 合并部署
```

整个流程既可以全自动运行，也支持人工在关键节点介入审核。

![evo 自进化执行路径](docs/assets/evo-pipeline.png)

**实时执行编排视图** — 追踪每个优化步骤的进度与状态：

![evo 执行编排](docs/assets/evo-run.png)

### 2. 多数据源接入

统一管理本地目录、对象存储与 OAuth 云端知识源（飞书等）的接入、同步与运行状态。

![数据源管理 — 新建数据源](docs/assets/datasource-create.png)

### 3. 知识运营资产管理（智积阅累）

集中管理词表、系统工具、技能（操作模板）与使用习惯，构建可运营、可追溯的记忆中枢。

![智积阅累 — 技能、词表、系统工具](docs/assets/knowledge-ops.png)

### 4. 灵活的 OCR 与向量存储

- **OCR**：内置 PDFReader / MinerU / PaddleOCR-VL（GPU）三档可选
- **向量存储**：Milvus + OpenSearch，支持随栈部署或外接
- **多路 Embedding**（embed_1~3）支持混合检索；只配置 embed_1 时自动切换单路模式

### 5. 企业级权限体系

Kong API Gateway + JWT/RBAC 四层鉴权：前端 → Kong RBAC → Core ACL → 算法服务，每一层都有独立的权限校验。

---

## 架构概览

```
┌──────────────────────────────────────────────────────┐
│                    前端 (8090）                       │
│           nginx SPA — 知识库 / 对话 / 管理模块          │
└─────────────────────┬────────────────────────────────┘
                      │
             ┌────────▼──────-──┐
             │   Kong (8000)    │  API 网关 + RBAC
             └──┬───────-───┬─-─┘
                │           │
       ┌────────▼─-─┐  ┌────▼──────────┐
       │auth-service│  │  core (Go)    │  数据集 / 文档 / 任务 / 检索
       │  FastAPI   │  │  HTTP API     │
       └────────────┘  └──────┬────────┘
                              │ 代理
             ┌────────────────┼───────────────┐
             │                │               │
    ┌────────▼──────┐  ┌──────▼──────┐  ┌─────▼──────┐
    │   parsing     │  │    chat     │  │    evo     │
    │    预处理/     │  │ 知识问答/对话 │  │   自进化    │
    │    向量化      │  │             │  │   自闭环    │
    └───────────────┘  └─────────────┘  └────────────┘
             │
    ┌────────┴──────────────┐
    │  Milvus + OpenSearch  │  向量 + 分段存储
    └───────────────────────┘
```

完整的服务依赖图、环境变量说明和请求鉴权链路，见 [`docs/architecture.md`](docs/architecture.md)。

---

## 快速开始

**前置条件：** Docker & Docker Compose

### 第一步 — 申请 MinerU API Key（高质量 PDF 解析）

前往 [https://mineru.net](https://mineru.net/apiManage/token) 申请 MinerU API Key。

```bash
export LAZYLLM_MINERU_API_KEY=你的mineru_key
```

> **注意：** 同样是 `LAZYLLM_` 前缀，不是 `LAZYMIND_`。

> **重要提示：** 由于 OCR 模型在服务启动时即完成初始化，**OCR 供应商的 API Key 必须在启动前配置好**。我们正在开发在前端配置 OCR Key 的功能，下个版本即可支持，敬请期待。

### 第二步 — 启动服务

```bash
make up-build
```

启动后访问：
- 前端：http://localhost:8090
- API 文档：http://localhost:8090/docs.html
- 默认账号：`admin` / `admin`

### 第三步 — 在前端配置模型

登录后进入模型设置页面，使用第一步申请的 API Key 配置**大模型（LLM）**、**视觉模型（VLM）** 和 **Reranker 模型**。

环境变量配置与完整示例见 [`docs/quick_start.CN.md`](docs/quick_start.CN.md)。

---

## 测试

兼容旧行为的快速测试命令仍然是：

```bash
make test
```

`make test` 会继续使用宿主机当前可用的 Python、Node/npm 和 Go 环境。

如果希望使用项目专用的宿主机测试环境，运行：

```bash
make test-hermetic
```

`make test-hermetic` 要求宿主机安装 `uv`、`fnm` 或 `nvm`，以及 Go `1.24.0`。它会在仓库内创建 Python 3.11 虚拟环境 `.venv-test/`，通过可用的 Node 版本管理器选择 Node 20，使用 `npm ci` 安装前端测试依赖，并运行与 `make test` 相同范围的 frontend、auth-service、backend/core 和 algorithm 测试。

## 常用启动配置

| 场景 | 命令 |
|------|------|
| 标准启动 | `make up` |
| 构建镜像并启动 | `make up-build` |
| 私有化部署 MinerU OCR | `make up LAZYMIND_DEPLOY_MINERU=1` |
| 私有化部署 PaddleOCR  | `make up LAZYMIND_DEPLOY_PADDLEOCR=1` |
| 外接 Milvus/OpenSearch | `make up LAZYMIND_MILVUS_URI=http://your-milvus:19530 LAZYMIND_OPENSEARCH_URI=https://your-opensearch:9200` |
| 开启存储 Dashboard | `make up LAZYMIND_ENABLE_STORE_DASHBOARDS=1` |

---

## 模型配置

所有算法服务统一通过 `LAZYMIND_MODEL_CONFIG_PATH` 选择配置文件。默认值是 `dynamic`，
以前端的用户级模型/API-key 选择可以随请求注入。仅在需要强制静态配置时设置为 `online` 或 `inner`。

| 值 | 说明 |
|----|------|
| `online` | 公有云 API |
| `inner` | 内网/私有化部署 |
| `dynamic`（默认） | 动态注入，key 随请求传入 |

可配置 `llm`、`reranker`、`embed_1~embed_3`。只配置 `embed_1` 时自动启用单路 embedding 模式。

---

## evo 自进化模块

evo 是一个独立的 FastAPI 服务（端口 8047，对外暴露 8048），实现完整的 RAG 质量优化闭环：

```
dataset_gen → eval → run（分析）→ apply（代码修改）→ merge → deploy → abtest
```

**支持两种运行模式：**
- **auto** — 全自动，无需人工干预
- **interactive** — 在关键节点暂停，等待人工 approve / revise / cancel

**自然语言驱动：**

```bash
curl -sX POST "$BASE/v1/evo/threads/$THREAD_ID/messages" \
  -H "Content-Type: application/json" \
  -d '{"content":"从知识库 KB_ID 生成评测集，分析报告后修改代码，做 ABTest 验证效果"}'
```

完整 API 文档见 [`evo/README.md`](evo/README.md)。

---

## 可选服务

| 服务 | Profile | 用途 |
|------|---------|------|
| **mineru** | `mineru` | MinerU PDF 解析（布局分析） |
| **paddleocr** | `paddleocr` | PaddleOCR-VL PDF 解析（需 GPU） |
| **milvus** | `milvus` | 向量存储 |
| **opensearch** | `opensearch` | 分段存储 |
| **attu** | `milvus-dashboard` | Milvus 可视化管理 |
| **opensearch-dashboards** | `opensearch-dashboard` | OpenSearch 可视化管理 |

---

## 项目结构

```
LazyMind/
├── kong.yml                    # Kong 声明式配置
├── docker-compose.yml          # 全服务编排
├── Makefile                    # lint / 启动快捷命令
├── backend/
│   ├── auth-service/           # FastAPI 鉴权服务（JWT、RBAC）
│   ├── core/                   # Go HTTP API（数据集 / 文档 / 任务 / 检索）
│   └── scripts/
├── frontend/                   # nginx + SPA
├── algorithm/
│   ├── chat/                   # RAG 对话（lazyllm）
│   ├── parsing/                # 文档解析（lazyllm + MinerU/PaddleOCR）
│   └── processor/              # 文档任务队列
├── evo/                        # 自进化闭环服务
├── api/                        # OpenAPI 规范（集中管理）
├── docs/                       # 快速开始、CLI、架构文档
└── tests/
    ├── backend/
    └── algorithm/
```

---

## 开发

```bash
make lint              # Python（flake8）+ Go（gofmt）
make lint-only-diff    # 只 lint 变更文件
```

- Go 模块：`backend/core` 使用 `module lazymind/core`
- Python：3.11+，依赖 `algorithm/requirements.txt`（`lazyllm[rag-advanced]`）
- OpenAPI 规范统一维护在 `api/` 目录，新增路由时需同步更新

---

## 许可证

详见仓库中的许可证信息。
