# 外部 Agent 接入 LazyMind Skill -> Workflow

本文介绍如何通过外部 Agent 提交 Skill 来源和任务描述，由 LazyMind 完成 Skill 安装/复用、Skill -> Workflow 转换、执行和结果返回。

支持的外部 Agent 包括 Codex、TraeWork、WorkBuddy、Cursor、小浣熊（`raccoon-work`）和 DeepSeek Harness。不同 Agent 使用各自的接入配置，但对 LazyMind 的调用流程和任务语义保持一致。

示例 Skill：[find-skill-skillhub](https://skillhub.cn/skills/user_290ac21c/find-skill-skillhub)

## 1. 任务示例

把下面这段发给已接入 LazyMind 的外部 Agent 即可：

```text
请使用 LazyMind 提供的外部 Agent Skill -> Workflow 能力完成下面的任务。

Skill 信息：
- name: find-skill-skillhub
- url: https://skillhub.cn/skills/user_290ac21c/find-skill-skillhub

任务：
在 SkillHub 中查找适合 PDF 文本提取、文档摘要、表格提取的技能。请使用多个相关中英文关键词进行搜索，合并并去重结果，按匹配程度推荐 3～5 个技能。

每个推荐结果需要包含：
- 技能名称
- 主要用途
- 匹配理由
- 分类
- 下载量
- 安装量
- SkillHub 主页链接

要求：
- 不要编造不存在的信息。
- 字段缺失时注明“未提供”。
- 这里只需要推荐技能，不要安装搜索结果中的候选技能。
- 请通过 LazyMind 执行 Skill -> Workflow 流程，并返回任务状态、最终结果和 LazyMind 查看链接。
```

用户只需指定 Skill 来源和任务目标，无需提供 LazyMind 内部的 `skill_id`、Workflow 草稿或手动操作发布流程。

## 2. 外部 Agent 应该如何调用

外部 Agent 收到上面的自然语言任务后，应通过 LazyMind 提供的统一调用框架做四件事：

1. 调用连接检查能力，确认 LazyMind 外部任务服务可用。
2. 调用 `start_skill_workflow_task` 提交 Skill 来源和任务描述。
3. 使用返回的 `task_id` 调用 `get_skill_workflow_task` 查询状态。
4. 任务完成后调用 `get_skill_workflow_result` 获取结果。

Agent 不应该自行搜索 SkillHub 来冒充执行结果，也不应该要求用户提供 LazyMind 内部 Skill ID。Skill 安装、转换、执行和结果收集都由 LazyMind 负责。

## 3. 最小接入配置

如果外部 Agent 支持 MCP，可通过 LazyMind 外部任务 MCP 接入。下面以 Codex 为例；请将 Python 和仓库路径替换为实际绝对路径，Python 环境需包含 LazyMind 算法依赖。其他 Agent 使用各自的 MCP 配置方式，但传入同样的 `--mode external` 和对应 `--agent-type`。

```toml
[mcp_servers.lazymind-external]
command = "/absolute/path/to/lazy-env/bin/python"
args = [
  "/absolute/path/to/LazyMind/scripts/lazymind_workflow_mcp.py",
  "--mode", "external",
  "--agent-type", "codex"
]
startup_timeout_sec = 60
tool_timeout_sec = 60
env_vars = ["LAZYMIND_WORKFLOW_TOKEN", "LAZYMIND_WORKFLOW_USER_ID"]

[mcp_servers.lazymind-external.env]
LAZYMIND_SERVER_URL = "http://localhost:8090"
LAZYMIND_PUBLIC_URL = "http://localhost:8090"
```

注意事项：

- `--agent-type` 按实际接入端设置，例如 `codex`、`traework`、`workbuddy`、`cursor`、`raccoon-work`、`deepseek-harness`。
- `LAZYMIND_SERVER_URL` 使用当前前端所在的 LazyMind 服务地址，SDK 在此地址后追加 `/api/core`。上面的 `8090` 示例适用于本仓库 Docker 前端网关。
- 如果直接访问 Core，改用 `LAZYMIND_WORKFLOW_BASE_URL` 指定完整地址；SDK 不会给直连 Core 地址追加网关前缀。此项优先级高于 `LAZYMIND_SERVER_URL`。
- Docker 和 Desktop 同时运行时，不要将一套实例的前端地址、另一套实例的 Core 地址或登录身份混用。外部调用使用任务所属用户在该实例“模型与服务 → 系统默认设置”中的模型。
- `workflow_connection_status` 返回 `model_configuration`：包含 `user_id`、`provider`、`model`、`ready` 和缺失配置的错误码，不包含密钥。提交前应核对它与前端系统默认模型一致。
- 本机开发可通过 `LAZYMIND_WORKFLOW_USER_ID` 走受信任身份；需要认证的部署使用有效 token。
- Core 需要加载当前分支代码、数据库迁移，并启用后台 worker。
- 外部模式只应暴露四个工具：`workflow_connection_status`、`start_skill_workflow_task`、`get_skill_workflow_task`、`get_skill_workflow_result`。
- 如果某个 Agent 不走 MCP，也应在自己的适配层里映射到同一套后端任务接口，保持请求结构、状态语义和结果格式一致。

## 4. 可选：实际 MCP / HTTP 参数示例

下面是 `start_skill_workflow_task` 的请求参数示例，供接入开发者参考。通过自然语言发起任务时，由外部 Agent 构造请求，用户无需手动填写 JSON。

上述任务对应的 MCP 工具 `start_skill_workflow_task` 或 HTTP `POST /external-agent/workflow-tasks` 请求如下：

```json
{
  "agent_type": "codex",
  "skill": {
    "name": "find-skill-skillhub",
    "url": "https://skillhub.cn/skills/user_290ac21c/find-skill-skillhub"
  },
  "task_description": "在 SkillHub 中查找适合 PDF 文本提取、文档摘要、表格提取的技能。请使用多个相关中英文关键词进行搜索，合并并去重结果，按匹配程度推荐 3～5 个技能。每个推荐结果需要包含：技能名称、主要用途、匹配理由、分类、下载量、安装量、SkillHub 主页链接。不要编造不存在的信息，字段缺失时注明“未提供”，不要安装搜索结果中的候选技能。",
  "external_conversation_id": "codex-conversation-id",
  "external_thread_id": "codex-thread-id",
  "input_bindings": {},
  "input_files": [],
  "config": {
    "reuse_workflow": true
  },
  "idempotency_key": "stable-request-id"
}
```

字段含义：

- `agent_type`：只允许 `codex`、`trae-work`、`workbuddy`、`cursor`、`raccoon-work`、`deepseek-harness`
- `skill`：外部 Agent 提供 Skill 来源，目前支持 `url`、`zip_base64`，MCP/SDK 额外支持本地 `zip_path`
- `task_description`：本次要 LazyMind 完成的具体任务
- `external_conversation_id` / `external_thread_id`：外部 Agent 自己的会话标识，用来排查和区分来源
- `input_files`：小文件输入，base64 传给 LazyMind
- `input_bindings`：已有 LazyMind input resource 的绑定
- `config.reuse_workflow`：是否复用已有可复用 workflow，目前只有这个配置
- `idempotency_key`：幂等重试用

LazyMind 在后台完成以下流程：

```text
安装/复用 Skill
→ Skill2Workflow
→ 发布/复用 Workflow
→ 设置 Workflow 可调用
→ 创建 LazyMind 会话
→ 执行 Workflow
→ 收集结果
→ 返回状态、结果、LazyMind 链接
```

## 5. 执行状态与结果

任务执行过程中可查看以下信息和行为：

| 项目 | 行为说明 |
| --- | --- |
| 提交方式 | 外部 Agent 只提交 Skill 名称、Skill URL 和任务描述。 |
| Skill 隔离 | 用户和外部 Agent 不需要知道 LazyMind 内部 `skill_id`。 |
| 转换链路 | LazyMind 完成 Skill 安装/复用、Workflow 生成或匹配、发布准备。 |
| 执行结果 | 最终结果来自 LazyMind 执行记录，而不是外部 Agent 自行完成业务搜索。 |
| 状态可见 | 外部 Agent 能展示任务状态、失败原因或下一步动作。 |
| 用户边界 | 需要人工确认时不自动跳过，返回 LazyMind 查看链接。 |
| 最终体验 | 用户能看到推荐结果、结果摘要和 LazyMind 详情入口。 |

本示例仅推荐技能，不安装候选技能。如果执行中遇到必须由用户选择或确认的步骤，任务进入 `waiting_user_action` 状态，并返回 LazyMind 跳转入口。外部 Agent 应停止自动轮询，引导用户前往 LazyMind 处理；等待用户处理不表示任务执行成功。
