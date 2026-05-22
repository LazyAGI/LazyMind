# 快速开始

本文档只涵盖两件事：

- 如何配置环境变量
- 如何启动服务

所有命令默认在仓库根目录下执行。

## 前置条件

- 已安装 Docker 和 Docker Compose
- 当前目录为仓库根目录
- 如使用公有云 API 模型，请提前准备好对应的 API Key
- 如使用内网模型，请确保当前机器能访问到内网服务

## 环境变量

### 1. 模型配置

通过 `LAZYMIND_MODEL_CONFIG_PATH` 选择模型配置。三个内置简写值：

| 值 | 说明 |
|----|------|
| `dynamic`（默认） | 每次请求从前端动态注入 Key |
| `online` | 公有云 API（静态配置） |
| `inner` | 内网 / 私有化部署 |

也可以直接传入配置文件的绝对路径。

使用公有云 API 时，需要导出对应的 API Key。变量名须与配置文件中的占位符一致。例如，配置文件中引用了 `${LAZYLLM_SILICONFLOW_API_KEY}`，则需要导出该变量：

```bash
export LAZYLLM_SILICONFLOW_API_KEY=your-key
export LAZYMIND_MODEL_CONFIG_PATH=online
```

如果配置文件引用了多个服务商，需要同时导出所有对应的 Key。`docker-compose.yml` 已透传常见的 LLM API Key 变量（`LAZYLLM_OPENAI_API_KEY`、`LAZYLLM_DEEPSEEK_API_KEY`、`LAZYLLM_SILICONFLOW_API_KEY` 等）。

使用内网模型时：

```bash
export LAZYMIND_MODEL_CONFIG_PATH=inner
```

### 2. OCR

OCR 默认关闭（使用内置 PDFReader）：

```bash
export LAZYMIND_OCR_SERVER_TYPE=none   # 默认值，可省略
```

启用本地 MinerU：

```bash
export LAZYMIND_OCR_SERVER_TYPE=mineru
# 未设置 LAZYMIND_OCR_SERVER_URL 时自动推导为 http://mineru:8000
```

复用已部署在 ECS / 内网的 MinerU：

```bash
export LAZYMIND_OCR_SERVER_TYPE=mineru
export LAZYMIND_OCR_SERVER_URL=http://your-inner-mineru:port
```

当 `LAZYMIND_OCR_SERVER_URL` 指向外部地址时，`make up` 不会启动本地 `mineru` profile。

启用 PaddleOCR（需要 GPU）：

```bash
export LAZYMIND_OCR_SERVER_TYPE=paddleocr
# 未设置 LAZYMIND_OCR_SERVER_URL 时自动推导为 http://paddleocr:8080
```

### 3. 向量 / 分段存储

默认情况下，Milvus 和 OpenSearch 随栈部署。如需使用外部服务：

```bash
export LAZYMIND_MILVUS_URI=http://your-milvus:19530
export LAZYMIND_OPENSEARCH_URI=https://your-opensearch:9200
export LAZYMIND_OPENSEARCH_USER=admin
export LAZYMIND_OPENSEARCH_PASSWORD=your-password
```

当 URI 保持默认值 `http://milvus:19530` 和 `https://opensearch:9200` 时，内置服务会自动部署。

### 4. 前端端口

前端默认使用端口 **8090**。如端口被占用可覆盖：

```bash
export LAZYMIND_FRONTEND_PORT=8080
```

### 5. 鉴权凭据（生产环境）

生产部署前请修改以下变量：

```bash
export LAZYMIND_JWT_SECRET=your-strong-secret
export LAZYMIND_BOOTSTRAP_ADMIN_USERNAME=admin
export LAZYMIND_BOOTSTRAP_ADMIN_PASSWORD=your-password
```

### 6. 使用 `.env` 文件

以上所有变量均可写入仓库根目录的 `.env` 文件，Makefile 会自动加载：

```bash
# .env
LAZYMIND_MODEL_CONFIG_PATH=online
LAZYLLM_SILICONFLOW_API_KEY=your-key
LAZYMIND_OCR_SERVER_TYPE=none
LAZYMIND_FRONTEND_PORT=8090
```

---

## 启动服务

### 标准启动

```bash
make up
```

在后台启动所有服务，Milvus 和 OpenSearch 自动部署。

### 构建镜像并启动

```bash
make up-build
```

首次运行或修改了 Dockerfile / 依赖后使用此命令。

### 只启动指定服务

```bash
make up SERVICES=chat,core
```

### 启用 MinerU OCR

```bash
export LAZYMIND_OCR_SERVER_TYPE=mineru
make up
```

### 启用 PaddleOCR（GPU）

```bash
export LAZYMIND_OCR_SERVER_TYPE=paddleocr
make up
```

### 使用外部 Milvus / OpenSearch

```bash
make up \
  LAZYMIND_MILVUS_URI=http://your-milvus:19530 \
  LAZYMIND_OPENSEARCH_URI=https://your-opensearch:9200
```

### 开启存储 Dashboard

```bash
make up LAZYMIND_ENABLE_STORE_DASHBOARDS=1
```

- Attu（Milvus）：http://127.0.0.1:3000
- OpenSearch Dashboards：http://127.0.0.1:5601（登录：`admin` / `LAZYMIND_OPENSEARCH_PASSWORD`）

Dashboard 仅绑定 `127.0.0.1`，且对应存储为外部服务时不会启动。

---

## 启动后访问

| 地址 | 说明 |
|------|------|
| http://localhost:8090 | 前端（默认端口） |
| http://localhost:8000 | Kong API 网关 |
| http://localhost:8090/docs.html | 统一 Swagger UI |
| http://localhost:8048 | evo API（自进化服务） |

默认账号：`admin` / `admin`

---

## 常用操作

不重新构建，直接重启容器：

```bash
docker compose up -d --force-recreate
```

停止服务：

```bash
make down
```

停止指定服务：

```bash
make down SERVICES=chat,core
```

查看服务状态：

```bash
docker compose ps
```

查看日志：

```bash
docker compose logs --tail=200 -f
```

---

## 数据重置

### 只重置知识库

清除 Milvus、OpenSearch、上传文件及知识库相关的 PostgreSQL 表。用户账号、鉴权 Token、Redis、对话记录和 Prompt **保留**。

```bash
make reset-kb
make up LAZYMIND_RESET_ALGO_ON_STARTUP=true
```

`reset-kb` 之后必须加 `LAZYMIND_RESET_ALGO_ON_STARTUP=true`，算法服务才会在下次启动时重建 schema 表。

### 全新启动（标准清理重启）

等价于 `reset-kb` + 重新构建 + 带 algo 重置启动：

```bash
make fresh-start
```

### 完全重置（清除所有数据）

删除所有持久化数据，包括用户账号、鉴权 Token、Redis 及所有 volume，等价于全新首次运行状态：

```bash
make reset-all
make up-build
```

### 清理容器和 volume

停止服务、删除所有 volume 并清理 Python 缓存（保留已构建的镜像）：

```bash
make clear
make up-build
```

---

## 完整启动示例

### 公有云 API 模型

```bash
export LAZYLLM_SILICONFLOW_API_KEY=your-key
export LAZYMIND_MODEL_CONFIG_PATH=online
export LAZYMIND_OCR_SERVER_TYPE=none

make up-build
```

### 内网模型 + 本地 MinerU

```bash
export LAZYMIND_MODEL_CONFIG_PATH=inner
export LAZYMIND_OCR_SERVER_TYPE=mineru

make up-build
```

### 内网模型 + 外部 MinerU

```bash
export LAZYMIND_MODEL_CONFIG_PATH=inner
export LAZYMIND_OCR_SERVER_TYPE=mineru
export LAZYMIND_OCR_SERVER_URL=http://your-inner-mineru:port

make up-build
```

### 内网模型 + 外部 Milvus / OpenSearch

```bash
export LAZYMIND_MODEL_CONFIG_PATH=inner
export LAZYMIND_MILVUS_URI=http://your-milvus:19530
export LAZYMIND_OPENSEARCH_URI=https://your-opensearch:9200
export LAZYMIND_OPENSEARCH_USER=admin
export LAZYMIND_OPENSEARCH_PASSWORD=your-password

make up-build
```
