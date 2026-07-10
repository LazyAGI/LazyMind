# 用户 Plugin 接入 ChatAgent、RemoteFS 版本存储与默认启停方案

## 1. 背景与目标

当前内置 Plugin 由 Chat 服务启动时从固定目录扫描并完整加载到进程级 `_registry`。ChatAgent 冷启动时读取各 Plugin 的 `description` 和 `when_to_use`，注册 trigger tool；模型选择 Plugin 后，再进入完整的 `scenario.md`、状态机、SubAgent 和 DriverAgent 执行流程。

用户创建或由 Skill 转换得到的 Plugin 目前只保存在 `plugin_drafts`，没有进入 ChatAgent 的运行时发现链路；现有配置也只有 `enable_plugin` 总开关，不能分别配置内置和用户 Plugin 的默认启停状态。

本方案实现：

1. 用户 Plugin 必须显式发布，草稿不会直接影响 ChatAgent。
2. Plugin 复用 Skill 已有的 RemoteFS、VersionFS 和按 hash 去重的 Blob 存储。
3. 新发布的用户 Plugin 默认关闭；用户开启后才成为 ChatAgent 候选。
4. 冷启动只向模型提供轻量说明、`when_to_use` 和 trigger tool。
5. 模型调用 trigger 后，Chat 才通过 RemoteFS 读取并物化指定 revision 的完整 Plugin。
6. Session 固定 `revision_id`，重新发布不会改变正在执行的版本。
7. 普通用户只能引用平台白名单工具；只有管理员可以发布并同进程执行自定义 Python scripts。

总体流程：

```text
用户编辑 Plugin 草稿
  → RemoteFS draft overlay
  → 显式发布/commit，生成不可变 revision
  → 默认关闭
  → 用户在 Plugin 管理页开启
  → Core 在聊天请求中传轻量 Plugin Catalog
  → ChatAgent 读取 description + when_to_use + trigger tool
  → 模型调用 trigger
  → Chat 通过 RemoteFS materialize 指定 revision
  → 复用 PluginSpec 和现有 Plugin 工作流执行
```

## 2. 核心设计

### 2.1 RemoteFS 包结构

新增独立 Plugin namespace，不将 Plugin 混入 `remote://skills`：

```text
remote://plugins/<owner-scope>/<plugin-id>/
├── plugin.yaml
├── scenario/
│   ├── scenario.md
│   ├── state.yml
│   └── driver.md
└── scripts/
    └── tools.py
```

示例：

```text
remote://plugins/builtin/image-plugin
remote://plugins/u_a81f2c/paper-review
```

`owner-scope` 使用稳定的服务端标识，不接受客户端任意拼接。Plugin 的真实身份为 `plugin_ref`：

```text
builtin:<plugin-id>
user:<owner-user-id>:<plugin-id>
```

### 2.2 渐进式发现

冷启动 Catalog 只包含：

- `plugin_ref`
- `plugin_id`
- `name`
- `description`
- `when_to_use`
- `source_type`
- `remote_root`
- `revision_id`
- `revision_no`
- `tree_hash`

冷启动不得读取或传输完整 `scenario.md`、`state.yml`、`driver.md`、scripts、slots、steps 和 UI 定义。

模型调用 trigger 后，Chat 才通过 `remote_root + revision_id` 物化完整目录。

### 2.3 版本存储语义

复用 Skill VersionFS 的存储方式：

```text
Blob：按内容 hash 去重，只为发生变化的文件新增正文
Revision：保存父版本、revision_no 和 tree_hash
Revision entries：每个 revision 保存完整的“路径 → blob hash”清单
Draft entries：只保存相对基准 revision 的 add/update/delete overlay
```

假设 v2 只修改 `scenario.md`，只会新增该文件的新 Blob；其他文件正文不会重复存储。每个 revision 仍保留完整但轻量的 tree manifest，以保证指定版本读取、回滚、diff 和 GC 简单高效。

本期不将 revision entries 改成父链差异。Plugin 文件数量少，差异链会显著增加历史版本读取和容错复杂度，存储收益很小。

### 2.4 Skill 与 Plugin 的关系

Skill 与 Plugin 是两个独立业务域，不共用资源记录、业务表、Blob 表或权限模型，只复用文件版本管理代码和 RemoteFS 协议：

```text
                         versionfs.Engine
                          /            \
                 SkillVersionStore   PluginVersionStore
                        |                   |
                   skill_* tables      plugin_* tables
```

明确边界：

| 能力 | 共享 | 独立 |
|---|---:|---:|
| `versionfs.Engine`、tree hash、draft overlay 算法 | ✓ | |
| RemoteFS HTTP 协议、通用 Handler 框架 | ✓ | |
| Python `RemoteFS` 客户端 | ✓ | |
| 资源元数据、Blob、revision、entry、draft 表 | | ✓ |
| 权限、发布校验、搜索、市场和分享逻辑 | | ✓ |

从 Skill 转换得到的 Plugin 仅保留 `source_skill_id` 用于来源追溯。转换时文件内容从 Skill 包复制到 Plugin draft，此后两者独立演进，不建立运行时依赖。

### 2.5 Core 与 Chat 的职责

Core 负责发布、版本、权限、启停、Catalog 和 RemoteFS revision view。Chat 不直接查询 Plugin 数据表，也不增加 Plugin 专属 runtime-bundle API。

RemoteFS 底层仍通过现有 `/remote-fs/*` Core 接口读取；这里避免的是新增算法到后端的 Plugin 专属内容 API，并统一复用文件系统访问机制。

## 3. 数据模型与 VersionFS 适配

### 3.1 Plugin 资源元数据

新增 `plugins`：

```text
id
plugin_ref
plugin_id
owner_user_id
owner_scope
source_type              -- builtin | user | skill
relative_root            -- plugins/<owner-scope>/<plugin-id>
name
description
when_to_use
head_revision_id
version
status                   -- active | archived | revoked
contains_scripts
created_at
updated_at
```

`description` 和 `when_to_use` 作为可检索的发布元数据，在 commit 后从 head revision 的 `plugin.yaml` 提取并同步，供 Catalog 使用。

### 3.2 Plugin VersionStore

第一期不迁移现有 Skill 表，复用通用 `versionfs.Engine` 接口，为 Plugin 实现薄适配器：

```text
plugin_blobs
plugin_revisions
plugin_revision_entries
plugin_drafts
plugin_draft_entries
```

字段和约束与 Skill V2 对应表保持一致，使 commit、rollback、tree hash、Blob GC、draft overlay 和并发控制直接复用 `versionfs.Engine`。

Skill 和 Plugin 的 Blob 只在各自业务域内按 hash 去重，不进行跨域去重。这样可以保持权限、GC、迁移和故障域完全独立；本方案不引入 `versioned_resources` 或共享 `resource_blobs` 表。

`PluginVersionStore` 实现与 Skill adapter 相同的 `versionfs.Store` 接口，包括：

```text
LoadHead / LoadDraft / HasDraftChanges / ClaimDraft
DraftEntries / RevisionEntries / EnsureBlobs
NextRevisionNo / CreateRevision / UpdateHead
ResetDraftAfterCommit / ResetDraftAfterRollback
EnforceRevisionLimit / ListBlobHashes / BlobReferenced / DeleteBlob
```

### 3.3 用户逐 Plugin 配置

新增：

```text
user_plugin_settings
- user_id
- plugin_ref
- enabled
- updated_at

PRIMARY KEY(user_id, plugin_ref)
```

有效启用规则：

```text
effective_enabled =
  user_chat_settings.enable_plugin
  AND plugins.status = active
  AND 当前用户有权使用该 Plugin
  AND user_plugin_settings.enabled
```

默认值：

- 现有内置 Plugin 迁移后默认开启。
- 新增内置 Plugin 可由 manifest 的 `default_enabled` 提供初始值。
- 新发布或从 Skill 转换的用户 Plugin 默认关闭。
- 本期只支持用户级默认配置，不提供会话级逐 Plugin 覆盖。

### 3.4 Session 固定 Revision

在 `plugin_sessions` 增加：

```text
plugin_ref
plugin_revision_id
plugin_revision_no
plugin_tree_hash
```

旧 `plugin_id` 暂时保留。历史内置 Session 迁移为 `builtin:<plugin_id>`；无法确定历史 revision 时使用明确的内置兼容 revision。

运行中的 Session 固定 revision。关闭、归档或发布新 revision 不影响活跃 Session；只有安全管理员将 revision 标记为 revoked 时才禁止继续加载。

## 4. RemoteFS 改造

### 4.1 通用 Namespace 路由

当前 RemoteFS handler 和路径解析硬编码 `skills/<category>/<skill-name>`。将其拆分为：

```text
RemoteFS 通用层
├── list/info/exists/content/materialize
├── task draft view
├── immutable revision view
└── 路径安全、权限和 Blob 读取

资源适配器
├── SkillRemoteFSAdapter
└── PluginRemoteFSAdapter
```

Plugin adapter 负责将 `plugins/<owner-scope>/<plugin-id>` 解析为 Plugin 资源 ID、校验权限，并调用 Plugin VersionStore。

通用层通过 namespace registry 路由，不直接引用 `skills` 或 `plugins` 表：

```go
type NamespaceAdapter interface {
    ResolveResource(ctx context.Context, userID string, path RemotePath) (ResourceRef, error)
    LoadView(ctx context.Context, resource ResourceRef, view ReadView) (map[string]Entry, error)
    ReadBlob(ctx context.Context, resource ResourceRef, hash string) ([]byte, error)

    CreateDir(...)
    WriteFile(...)
    DeletePath(...)
    MovePath(...)
    CopyPath(...)
}

type RemoteFSRegistry struct {
    adapters map[string]NamespaceAdapter
}

registry.Register("skills", skillAdapter)
registry.Register("plugins", pluginAdapter)
```

职责划分：

- 通用 Handler：解析 HTTP 参数、选择 namespace、统一 view 规则、路径安全和错误响应。
- Skill adapter：解析 category/name、Skill 权限、`skill_*` 表和 Skill Blob Store。
- Plugin adapter：解析 owner-scope/plugin-id、Plugin 权限、`plugin_*` 表和 Plugin Blob Store。
- RemoteFS 不理解 `SKILL.md` 或 `plugin.yaml` 的业务规则；这些规则分别留在 Skill Service 和 Plugin Service。

### 4.2 指定 Revision 的只读视图

扩展 RemoteFS 读取接口：

```http
GET /remote-fs/list?path=plugins/...&revision_id=<uuid>
GET /remote-fs/info?path=plugins/...&revision_id=<uuid>
GET /remote-fs/exists?path=plugins/...&revision_id=<uuid>
GET /remote-fs/content?path=plugins/...&revision_id=<uuid>&encoding=raw
```

读取优先级：

```text
revision_id 存在 → immutable revision view
否则 task_id 存在 → 当前 task 的 draft/review/head view
否则 → published head view
```

`revision_id` view 必须只读；PUT、DELETE、MOVE、COPY 和 MKDIR 请求携带 `revision_id` 时返回 400。

权限校验必须同时验证资源所有权/授权和 revision 是否属于该资源，不能只按 revision ID 查询。

### 4.3 Python RemoteFS

扩展 `RemoteFS` 的读取方法：

```python
fs.ls(path, revision_id=revision_id)
fs.info(path, revision_id=revision_id)
fs.exists(path, revision_id=revision_id)
fs.open(path, 'rb', revision_id=revision_id)
fs.materialize_dir(path, local_dir, revision_id=revision_id)
```

`materialize_dir` 在递归 list 和每个 content 请求中透传同一个 revision ID，禁止某些文件意外从 head view 读取。

建议代码归属：

```text
backend/core/versionfs/                 通用版本引擎
backend/core/remotefs/                  通用 Handler、路径、view 和 namespace registry
backend/core/skillv2/remotefs/          Skill adapter
backend/core/plugin/remotefs/           Plugin adapter
algorithm/lazymind/chat/integrations/remote_fs.py
                                        通用 Python RemoteFS 客户端
```

## 5. 发布与配置接口

### 5.1 发布

```http
POST /api/plugin-drafts/{draft_id}:publish
```

发布过程：

1. 将现有 Plugin 草稿内容映射为 RemoteFS package draft；过渡期保留原 `plugin_drafts` 作者界面数据。
2. 校验 `plugin.yaml`、`state.yml`、`scenario.md`、transitions、steps、slots 和 UI 引用。
3. 校验工具白名单与 scripts 权限。
4. 通过 `versionfs.Engine.CommitDraft` 创建不可变 revision。
5. 从新 head 的 `plugin.yaml` 同步 `name/description/when_to_use/contains_scripts`。
6. 首次发布创建默认关闭的 `user_plugin_settings`。

重复发布生成新 revision，保持用户启停状态不变。删除草稿不删除已发布 revision；停止后续使用通过 archive 完成。

普通用户不得发布 `tool_scripts` 或任意 Python scripts；管理员可以发布含 scripts 的 Plugin。

### 5.2 默认启停接口

```http
GET /api/chat/settings/plugins
PATCH /api/chat/settings/plugins/{encoded_plugin_ref}
```

PATCH：

```json
{"enabled": true}
```

GET 统一返回内置和用户 Plugin，包括 `plugin_ref`、来源、head revision、状态和当前用户 enabled 值。

## 6. Core 到 Chat 的 Catalog

扩展 Chat 请求：

```python
class PluginCatalogEntry(BaseModel):
    plugin_ref: str
    plugin_id: str
    name: str
    description: str
    when_to_use: str
    source_type: str
    remote_root: str
    revision_id: str
    revision_no: int
    tree_hash: str


class ChatPluginOptions(BaseModel):
    enable_plugin: Optional[bool] = None
    plugin_context: Optional[Dict[str, Any]] = None
    catalog: List[PluginCatalogEntry] = []
```

示例：

```json
{
  "plugin": {
    "enable_plugin": true,
    "catalog": [
      {
        "plugin_ref": "user:u123:paper-review",
        "plugin_id": "paper-review",
        "name": "论文评审",
        "description": "分析论文并生成结构化评审意见",
        "when_to_use": "当用户要求评审、分析或批判一篇论文时使用",
        "source_type": "skill",
        "remote_root": "remote://plugins/u_a81f2c/paper-review",
        "revision_id": "rev-uuid",
        "revision_no": 3,
        "tree_hash": "sha256:..."
      }
    ]
  }
}
```

总开关关闭时 Catalog 为空。未启用、无权限、无 head revision、已归档或 revoked 的 Plugin 不得进入 Catalog。

客户端传入的 Catalog 不可信；Core 在转发到 Chat 前必须重新生成或完整过滤，不能直接透传浏览器字段。

## 7. Chat 加载机制改造

### 7.1 可原样复用

RemoteFS package 物化为现有目录格式后，继续复用：

- `PluginSpec` 的 YAML、scenario、driver、scripts 和校验逻辑。
- `StateMachine` 的可达性、rewind 和 retry。
- `_trigger_plugin_step`、advance tools 和查询工具。
- dynamic/auto 模式、SubAgent、DriverAgent。
- artifact、intent、step status 和任务上下文注入。

最终仍调用：

```python
PluginSpec(plugin_id=runtime_id, plugin_dir=runtime_dir)
```

### 7.2 PluginResolver

新增：

```python
@dataclass(frozen=True)
class PluginRuntimeKey:
    plugin_ref: str
    remote_root: str
    revision_id: str
    tree_hash: str


class PluginResolver:
    def resolve(self, key: PluginRuntimeKey, user_id: str) -> PluginSpec:
        ...
```

`resolve`：

1. 以 `(plugin_ref, revision_id, tree_hash)` 查询 LRU 缓存。
2. 内置 Plugin 可从静态 registry 返回；上层仍使用相同 RuntimeKey。
3. 用户 Plugin 调用 `RemoteFS.materialize_dir(..., revision_id=...)`。
4. 物化到 `<runtime-cache>/<plugin-ref-hash>/<revision-id>/` 临时目录。
5. 校验物化文件树 hash 与 Catalog 的 `tree_hash` 一致。
6. 原子 rename 后构造 `PluginSpec`。
7. 成功后写入 LRU 缓存；失败时清理临时目录且不污染全局 registry。

用户 Plugin 不写入进程级 `_registry`，避免同名覆盖和跨用户串用。

### 7.3 冷启动工具与 Prompt

`build_cold_start_tools()` 改为遍历 Catalog，不预加载状态机。每个 trigger 的 docstring 直接使用 `description + when_to_use`。

工具名：

```text
trigger_<normalized_plugin_id>_<stable_plugin_ref_hash>
```

模型调用 trigger 后：

```python
spec = resolver.resolve(runtime_key, user_id)
first_steps = spec.state_machine.get_reachable_steps('__start__')
_trigger_plugin_step(execution_context, first_steps[0], user_input, is_cold_start=True)
```

完整 `scenario.md` 仍仅在 Plugin Session 启动后注入。

### 7.4 统一执行上下文与 Session 恢复

```python
@dataclass(frozen=True)
class PluginExecutionContext:
    plugin_ref: str
    plugin_id: str
    remote_root: str
    revision_id: str
    revision_no: int
    tree_hash: str
    user_id: str
```

`get_plugin/get_state_machine/get_step_config/get_scenario/get_driver` 改为基于执行上下文解析。保留字符串兼容层，将旧 `image-plugin` 映射为 `builtin:image-plugin`，分阶段迁移现有调用点。

活跃 Session 恢复时从 `plugin_context` 读取固定 revision，先调用 resolver，再执行现有 scenario、state machine、driver 和 advance 逻辑。

## 8. 前端与权限

Plugin 管理列表统一展示内置和用户 Plugin，并增加用户默认 Switch：

- 用户 Plugin 列表显示是否已发布以及当前 head revision 版本号。
- 每个用户 Plugin 只有一份可变草稿；`(created_by, plugin_id)` 唯一约束继续生效。
- 详情页默认加载草稿，可切换查看任意历史 revision；历史 revision 始终只读。
- 用户点击“编辑此版本”并确认后，用选定 revision 的完整文件树覆盖唯一草稿，不修改任何已发布 revision。
- 用户 Plugin 首次发布后默认关闭。
- 详情页展示草稿状态、head revision、版本历史、来源和启停状态。
- 提供发布新 revision、回滚和归档操作。
- 总开关关闭时保留逐项配置，但 ChatAgent 不获得任何 Plugin。
- 活跃 Session 不因默认开关变化立即终止。

安全规则：

- 普通用户不得发布或执行 scripts，只能使用平台白名单工具。
- 管理员 scripts 沿用当前同进程加载方式，并记录发布人、revision、tree hash、加载异常和调用审计。
- RemoteFS 拒绝绝对路径、`..`、符号链接逃逸和跨 owner-scope 访问。
- script module name 包含 `plugin_ref` hash 和 revision ID，避免 `sys.modules` 冲突。

## 9. 实施顺序

1. 为 Plugin 增加资源、Blob、revision、entry、draft 和 draft overlay 表，并实现 `PluginVersionStore`。
2. 将 RemoteFS handler 抽出通用 namespace registry 和 adapter 接口，现有 Skill 逻辑迁入 Skill adapter，保持外部行为不变。
3. 增加 `plugins/...` namespace 和指定 `revision_id` 的只读视图。
4. 扩展 Python `RemoteFS` 的 revision 参数和 `materialize_dir`。
5. 将现有 Plugin 草稿接入 RemoteFS draft 与 VersionFS commit 发布。
6. 增加用户逐 Plugin 设置和 Core Catalog 解析。
7. 扩展 Chat 请求结构，新增 `PluginResolver`。
8. 将冷启动 prompt/trigger 改为 Catalog 驱动。
9. 将 active session、DriverAgent 和 getter 迁移到 `PluginExecutionContext`。
10. 上线前端发布、回滚、归档和逐 Plugin Switch。
11. 通过 feature flag 先验证内置 Plugin，再开放用户 Plugin。

## 10. 测试与验收

### 10.1 VersionFS 与 RemoteFS

- v2 只修改一个文件时，只新增该文件 Blob，未修改正文不重复存储。
- Skill 和 Plugin 分别写入 `skill_blobs` 与 `plugin_blobs`，不存在跨域引用或 GC 影响。
- 每个 revision 的完整 tree manifest 可独立读取并产生稳定 tree hash。
- 指定 revision 的 list/info/content/materialize 始终读取同一不可变版本。
- revision 不属于目标 Plugin、越权 owner-scope 或 revision view 写操作均被拒绝。
- draft overlay 的 add/update/delete、commit、rollback、并发冲突和 Blob GC 正常。
- 现有 Skill RemoteFS 契约和测试全部保持通过。

### 10.2 发布、权限与启停

- 合法草稿成功发布为新 revision；非法状态机、step/slot 引用被拒绝。
- 普通用户不能发布 scripts；管理员可以。
- 新用户 Plugin 默认关闭，开启后才进入下一次聊天请求的 Catalog。
- 总开关关闭时 Catalog 为空。
- 用户不能读取、启用或执行其他用户的私有 Plugin。

### 10.3 渐进加载与版本固定

- 冷启动只包含轻量 Catalog，不物化完整 Plugin。
- 未调用 trigger 时不读取 state、scenario 或 scripts。
- trigger 后才 materialize 指定 revision，并加载首个 step。
- 不同用户同名 Plugin 的工具名、缓存、目录和 Session 相互隔离。
- 新 Session 使用最新 head revision，旧 Session 继续使用固定旧 revision。
- tree hash 不一致、revision 缺失或权限失效时拒绝启动且不污染缓存。

### 10.4 回归

- 内置 image/writer Plugin 的触发、dynamic/auto、advance、rewind、retry 和 DriverAgent 行为不变。
- `enable_plugin=false` 仍回退到纯 QA。
- 历史内置 Session 可通过兼容映射恢复或返回明确错误。

## 11. 最终模块边界

| 模块 | 职责 |
|---|---|
| VersionFS | revision、tree hash、draft overlay、commit、rollback、GC |
| RemoteFS 通用层 | HTTP 协议、namespace 路由、view 规则、路径安全和指定 revision 读取 |
| Skill RemoteFS adapter | Skill 路径、权限及 `skill_*` 表访问 |
| Plugin RemoteFS adapter | Plugin 路径、权限及 `plugin_*` 表访问 |
| Core Plugin | 草稿、发布、元数据、启停和轻量 Catalog |
| Chat 请求 | 携带当前用户已启用 Plugin 的轻量 Catalog |
| Cold-start ChatAgent | 读取说明与 `when_to_use`，暴露轻量 trigger |
| PluginResolver | trigger 后通过 RemoteFS 物化、校验并缓存完整 Plugin |
| PluginLoader / PluginSpec | 继续解析目录、状态机、scenario、driver 和 scripts |
| PluginManager | 继续负责 trigger、advance、rewind、Session 和任务执行 |

Skill 和 Plugin 各自维护完整独立的数据表与业务逻辑，只共享 VersionFS 引擎、RemoteFS 协议与通用实现。核心改造集中在 RemoteFS namespace、指定 revision 读取、Plugin VersionStore、Plugin 身份和 `PluginSpec` 查找层；现有状态机、SubAgent、DriverAgent、artifact 和工作流执行机制继续复用。
