# 计划 2：Scenario 完整展示 + 版本管理发布 + 系统集成 + Skill 生成入口

> 在计划 1（双向编辑器 + 校验）基础上，补全插件从"草稿"到"线上运行"的完整生命周期：
> 全文件展示、版本管理与发布、系统热重载集成、以及从 Skill 一键生成插件草稿。

---

## 背景与范围

本计划覆盖 target.md 中以下条目：

- §一（条目 1–2）：权限与访问控制
- §二（条目 3–6）：状态机读写接口 + 持久化存储
- §六（条目 17–23）：版本管理与发布
- §七（条目 24–27）：从 Skill 生成插件

同时补充 target.md 未单独列节、但完整交付必须覆盖的内容：

- Scenario 多文件完整展示（`plugin.yaml`、`scenario.md`、`driver.md`、`scripts/`、`state.yml`）
- 动态创建的插件如何被系统加载并在 Chat 中生效（热重载机制）

---

## 一、权限与访问控制（target §一）

### 1.1 管理员限定（条目 1）

| 角色 | 权限 |
|------|------|
| 管理员 | 可进入编辑器、新建/修改/发布插件 |
| 普通用户 | 只读展示状态机（同阶段 5 只读模式），无编辑入口 |

开发调试环境支持通过配置项 `DEV_ALLOW_NON_ADMIN_EDIT=true` 临时放开限制。

### 1.2 接口层权限（条目 1–2）

所有写操作接口（Draft 保存、发布、版本回滚）在 Go core 层校验 `admin` 角色，普通用户调用返回 403。

---

## 二、Scenario 完整文件展示

### 2.1 展示范围

插件 Detail 页展示该插件完整的 scenario 文件树，包括：

| 文件 | 说明 |
|------|------|
| `plugin.yaml` | 插件元数据、UI slot 声明、trigger 配置 |
| `scenario.md` | 意图识别描述（自然语言，供 AI 路由使用）|
| `driver.md` | 驱动器行为说明、system prompt 模板 |
| `state.yml` | 状态机定义（可在图形/YAML 编辑器中编辑，计划 1）|
| `scripts/` | 各步骤关联的脚本文件（只读展示，后续可扩展在线编辑）|

### 2.2 文件树 UI 结构

```
PluginDetail/
├── FileTree（左侧导航）
│   ├── plugin.yaml
│   ├── scenario.md
│   ├── driver.md
│   ├── state.yml          ← 点击打开双向编辑器（计划 1）
│   └── scripts/
│       ├── step_collect.py
│       └── step_generate.py
└── FileContent（右侧主区）
    ├── MarkdownViewer      ← 展示 .md 文件
    ├── YamlViewer          ← 展示 .yaml / .yml（只读）
    ├── StateGraphEditor    ← state.yml 专用（来自计划 1）
    └── CodeViewer          ← 展示 scripts/
```

### 2.3 文件读写接口

| 接口 | 方法 | 说明 |
|------|------|------|
| `GET /plugins/:id/files` | 返回文件树结构 | 列出所有文件路径 |
| `GET /plugins/:id/files/*path` | 返回单文件内容 | 支持 `?version=draft|v1|v2` |
| `PUT /plugins/:id/files/*path` | 更新单文件内容（仅 Draft）| 非 `state.yml` 的其他文件编辑入口 |

> `state.yml` 的写入走计划 1 的 Draft 保存接口，保证校验逻辑统一。

---

## 三、状态机读写接口与持久化（target §二）

### 3.1 接口设计（条目 3–5）

| 接口 | 方法 | 说明 |
|------|------|------|
| `GET /plugins/:id/stategraph` | 读取状态机 | `?version=draft|v1|v2|latest`；返回 YAML 文本 + 布局 + 图结构摘要 |
| `POST /plugins/:id/draft` | 保存 Draft | 接收 YAML 文本，执行校验，写入 `plugin_versions` 表（draft 记录）|
| `POST /plugins/:id/publish` | 发布 | 将当前 Draft 版本号自增发布为正式版本，触发热重载 |

### 3.2 持久化存储（条目 6）

**表：`plugin_versions`**

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | uuid | 主键 |
| `plugin_id` | uuid | 关联插件 |
| `version` | varchar | `draft` / `v1` / `v2` / … |
| `status` | enum | `draft` / `published` |
| `content` | jsonb | 完整 scenario 文件内容（所有文件的 map）|
| `stategraph_yaml` | text | `state.yml` 全文（冗余存储，便于查询和校验）|
| `created_at` | timestamp | |
| `updated_at` | timestamp | |
| `created_by` | uuid | 操作者 user_id |
| `note` | text | 发布备注（可选）|

- 不依赖容器本地文件系统（条目 6）
- 文件系统中已有的插件目录在首次访问时自动导入为 `v1`（初始化迁移脚本）

---

## 四、版本管理与发布（target §六）

### 4.1 版本状态模型（条目 17）

```
Draft（可多次覆盖保存）
  └─► 发布 ──► v1（published，不可变）
                └─► 再次编辑保存为新 Draft
                      └─► 发布 ──► v2（published，不可变）
                                    └─► ...
```

- 每个插件同时最多一份 Draft
- Draft 不可被 Chat 会话加载

### 4.2 版本绑定与选择（条目 18–19）

- Chat 会话启动时绑定当前 `latest` 已发布版本（快照绑定，后续发布不影响该会话）
- 新建 Chat 时支持显式从版本列表中选择特定版本（条目 19）
- 版本绑定关系存入 `chat_sessions.plugin_version_id`

### 4.3 版本列表与回滚（条目 20–21）

| 接口 | 说明 |
|------|------|
| `GET /plugins/:id/versions` | 返回所有已发布版本列表（含版本号、发布时间、发布者、备注）|
| `POST /plugins/:id/versions/:version/rollback` | 基于历史版本内容创建新版本号并发布；版本历史不可变 |

### 4.4 版本差异对比（条目 22）

- 后端接口：`GET /plugins/:id/diff?from=v1&to=v2`，返回节点/边的增删改列表
- 前端 UI：Diff 视图，左右对比两个版本的图形画布（节点/边高亮标注变更类型）

### 4.5 版本清理（条目 23）

- 默认保留最近 50 个已发布版本（可配置）
- 清理时跳过仍被活跃 Chat 会话引用的版本（查询 `chat_sessions` 表）
- 清理任务由定时 Job 执行（或在新版本发布时触发）

---

## 五、系统热重载：动态创建的插件如何被用起来

> 这是 target.md §二条目 5 所述"运行中服务无需重启即可加载新版本"的实现路径。

### 5.1 现有插件加载机制回顾

目前 scenario/plugin 由文件系统目录在服务启动时静态加载。动态创建后需要让 Python chat 服务在运行期感知到新插件/新版本。

### 5.2 热重载方案

```
发布接口（Go core）
  └─► 写入 plugin_versions 表（status=published）
        └─► 发布 Redis channel "plugin.published" 事件
              └─► Python chat 服务订阅该 channel
                    └─► 收到事件后从 DB 拉取新版本内容
                          └─► 重新注册 scenario router / driver
                                └─► 新建 Chat 会话即可使用新版本
```

**关键设计点：**

| 点 | 方案 |
|----|------|
| 多副本一致性 | 通过 Redis Pub/Sub 广播，所有副本同时收到事件并各自从 DB 刷新 |
| 加载失败处理 | 加载失败时保持旧版本继续服务，记录错误日志并告警 |
| 版本隔离 | Chat 会话携带 `plugin_version_id`，Python 侧按版本 ID 加载对应快照 |
| 冷启动 | 服务启动时从 DB 加载所有 `latest` published 版本，不依赖文件系统 |

### 5.3 Chat 会话版本绑定流程

```
用户发起新建 Chat（POST /conversations，带 plugin_id）
  └─► Go core 查询该 plugin 的 latest published version_id
        └─► 写入 chat_sessions.plugin_version_id
              └─► 后续所有对话请求携带该 version_id 到 Python chat
                    └─► Python 按 version_id 加载对应 scenario/driver 快照
```

---

## 六、从 Skill 生成插件（target §七）

### 6.1 Skill → 插件草稿（条目 24）

**入口：**

- 本地 Skill：用户在插件列表页选择"从 Skill 新建"，上传或选择已有 `SKILL.md` 文件
- 网络 Skill：输入 URL，系统 fetch 该 URL 的 `SKILL.md` 内容

**生成流程：**

```
SKILL.md 内容
  └─► Go core 调用 Python AI 分析服务（POST /ai/generate-plugin-draft）
        └─► LLM 分析工作流描述，生成：
              ├─ scenario.md（意图识别描述）
              ├─ state.yml（步骤列表 + 状态机转移）
              └─ plugin.yaml（插件元数据 + UI slot 声明）
                    └─► 写入 plugin_versions（draft 记录）
                          └─► 前端打开 StateGraphEditor 展示草稿
```

**AI Prompt 设计要点：**

- 输入：SKILL.md 全文
- 输出：structured JSON，包含三个文件的内容
- 约束：要求 LLM 输出的 `state.yml` 必须通过 V1–V10 校验规则（在生成后后端自动校验，失败则返回错误让用户手动修正）

### 6.2 草稿预览与交互修改（条目 25）

- 生成结果在 PluginDetail 页以 Draft 状态展示
- 用户可用计划 1 的图形/YAML 编辑器对草稿进行交互式调整
- 多次保存 Draft 均覆盖上一次 Draft，不产生版本号

### 6.3 发布为插件首个版本（条目 26）

- 用户确认草稿后点击"发布"，触发发布接口
- 生成 `v1`，插件立即通过热重载机制注册到系统
- 新建 Chat 即可使用该插件

### 6.4 从零新建（条目 27）

- 插件列表页支持"从空白新建"入口
- 创建空插件记录（无 Draft 内容），打开 StateGraphEditor 空白画布
- 用户手动定义步骤与状态机，保存为 Draft 后按需发布

---

## 七、接口汇总

| 接口 | 方法 | 权限 | 说明 |
|------|------|------|------|
| `GET /plugins/:id/files` | GET | 所有登录用户 | 文件树 |
| `GET /plugins/:id/files/*path` | GET | 所有登录用户 | 文件内容，支持 version 参数 |
| `PUT /plugins/:id/files/*path` | PUT | 管理员 | 更新非 state.yml 文件 |
| `GET /plugins/:id/stategraph` | GET | 所有登录用户 | 读取状态机（含布局）|
| `POST /plugins/:id/draft` | POST | 管理员 | 保存 Draft（含校验）|
| `POST /plugins/:id/publish` | POST | 管理员 | 发布新版本，触发热重载 |
| `GET /plugins/:id/versions` | GET | 所有登录用户 | 版本列表 |
| `POST /plugins/:id/versions/:v/rollback` | POST | 管理员 | 回滚到历史版本 |
| `GET /plugins/:id/diff` | GET | 管理员 | 版本差异对比 |
| `POST /plugins/generate-from-skill` | POST | 管理员 | Skill → 草稿生成 |
| `POST /plugins` | POST | 管理员 | 从零新建空插件 |

---

## 八、数据库变更汇总

| 表 | 操作 | 说明 |
|----|------|------|
| `plugin_versions` | 新建 | 版本存储（草稿 + 发布版本）|
| `plugins` | 新增字段 `latest_version_id` | 指向最新发布版本 |
| `chat_sessions` | 新增字段 `plugin_version_id` | 会话版本绑定 |

---

## 九、不在本计划范围内

- YAML 双向编辑器、校验逻辑、图形画布组件 → 计划 1
- `state.yml` 的 Draft 保存接口具体校验实现 → 计划 1（本计划调用其结果）
