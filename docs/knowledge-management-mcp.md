# 知识库管理 MCP

本实现由 Core 资源服务负责事务、权限、冲突和回执，页面与 capability 复用服务。MCP 只负责协议映射；Chat/Work 宿主负责可信身份、来源转换和业务确认。

## 工具与权限

内置管理连接仅加载 `knowledge.list`、`knowledge.document.list`、`knowledge.document.aggregate`、`knowledge.create`、`knowledge.folder.create`、`knowledge.resource.update_metadata`、`knowledge.resource.move`、`knowledge.resource.delete`、`knowledge.document.upload`。公开连接另保留原有 `knowledge.document.get` 和 `knowledge.search`。

工具继承账号功能权限、知识库 ACL、来源访问策略和可信执行约束。模型不能填写用户身份、批准状态或执行凭据。内置连接在实际派发出口校验来源与工具白名单；显式空白名单不加载任何工具。原生 KBToolkit 保留五项读取能力，固定历史 Workflow revision 通过实例级兼容路径读取原查询工具。

创建、上传、元数据修改和同库移动不额外审批。LazyMind 内删除和跨库移动必须确认具体资源、路径、数量和影响；确认绑定参数摘要与资源快照，提交事务内再次校验。公开 MCP 没有 `confirmed` 参数或额外确认工具，由调用方决定交互。

## 原件与结果

上传使用 `uploaded_file.file_ref`、`local_file.path`、`remote_file.url` 或 `work_artifact.artifact_id + revision_id`。每个来源是互斥结构。宿主本地路径必须由宿主授权读取并通过 multipart 传输实际字节；远程 Core 不读取客户端路径。CLI 需要客户端提供 roots，不能以路径字符串绕过范围限制。

获取原件与绑定入库分离。原件落盘且 Document 提交后即为 uploaded；解析提交失败仍返回真实 ID。独立绑定使用独立记录，不能反复改写已绑定 UploadedFile。Work 使用发布交付的固定版本，真实 blob MIME 与 file/text/json 分类分别处理。该来源依赖已有 Artifact V2 发布版本；本改动不修改 Artifact V2 的全局默认开关。不存在或不可访问的版本返回 `NOT_FOUND`，未发布版本返回 `INVALID_ARGUMENT`。

资源状态、处理状态和回收状态各自返回。未知处理阶段为 `unknown`，stored 为 `not_requested`，不能把 uploaded/moved 当成 ready。独立根目录分别提交、分别返回结果；单根整树失败不留下部分目录。确认之后新增的后代、源或目标变更会拒绝提交。

同一操作键绑定用户、租户、动作和参数摘要。已提交回执优先于源资源查询，删除/移动后仍能核对真实 ID；无法确认提交结果为 `unconfirmed`。超时、断线和取消不代表已经提交的项回滚，也不会自动整批重试。

## 分页与统计

保留原有 `items/page`、`items[].id`、默认列表行为及 path 上传相对目录含义。`logical_path` 表示逻辑目录路径。`parent_id` 省略表示全库范围，空字符串表示根目录。多库结果在 Core 统一排序和分页，游标绑定用户与查询。

统计扫描、分组维度和组数有界；超出返回 `QUERY_TOO_LARGE` 及实际值、上限和缩小范围建议。删除/移动使活动映射失效，原生检索、公开 get/search、扩展节点、图片和引用都校验当前映射；迟到发布与历史回填不能复活已移除文档。

## 发布开关与预算

P7 已完成本次隔离 8091 的必需验收，管理能力默认启用。设置 `LAZYMIND_KNOWLEDGE_MANAGEMENT_ENABLED=false` 可关闭新增管理入口和宿主加载；未知配置值也按关闭处理。跨库移动仍要求目标部署提供已校准预算，默认开放管理能力不会绕过此门槛。

完成目标部署的性能测量后，通过私有运行配置设置 `LAZYMIND_KNOWLEDGE_MOVE_BUDGET_JSON`：

```json
{"max_resources":100,"max_bytes":33554432,"prepare_seconds":30,"copy_bytes_per_second":1048576}
```

此处数值仅展示格式，不是测量结论。配置必须全部为正，准备时限为 4–300 秒；运行剩余时间更短时取较小值，并预留三秒提交与回执核对时间。校准应覆盖资源/空目录数量、实际原件复制、数据库事务与来源策略调用；直接 SQLite 测试不能代替部署中 SQLite proxy 的验证。

内置凭据有效期五分钟，账号与运行在调用时校验；慢准备之后、事务开始前刷新账号，事务内锁定运行归属并复核任务/attempt/lease。通知只提供真实调用开始/结束；最终结构化结果始终是资源与处理状态依据，不报告伪造的逐文件百分比。

## 验证与已知边界

阶段证据见 `.superpowers/sdd/knowledge-management-mcp/`。资源故障注入、竞态检测、双数据库迁移、真实 Work 双写产物、宿主白名单、确认卡、MCP 结构化结果及进度绑定有定向测试。整体 Core 套件仍有独立确认的基线失败及间歇 sidecar 超时，不能称为全绿。

外部 KB provision 成功而 Core 创建事务失败时，可能保留外部孤儿。没有做可能误删同键并发创建结果的补偿删除；需要独立核对后回收。大规模异步移动、原件替换和第三方 URL 自动 OAuth 不在本期。

8091 部署必须使用专用运行配置与隔离入口；先对受影响数据库进行原生一致性备份，并保存原件、索引和必要配置。不得触碰 8090，不能以健康检查代替登录、页面、Chat/Work、CLI 和检索端到端结果。

本次 8091 使用 128 个资源、32 MiB、15 秒准备时限和 4 MiB/s 预算估算。三次真实 121 资源 / 32 MiB 整树移动耗时 0.79–0.82 秒；这是本机串行测量，不是生产并发容量或 p95。同步删除和同库移动也受资源树规模限制；超限需缩小显式选择范围，本期不提供后台批量任务。
