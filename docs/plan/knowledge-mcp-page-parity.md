# 知识库 MCP 与页面操作对齐

2026-10-20，实施分支 `dya/knowledge-mcp-page-parity`。

## 边界与契约

本轮覆盖原有九个管理工具、`knowledge.document.get`、`knowledge.search`。后续按确认范围移除了曾新增的 MCP `knowledge.options`，全部管理工具的可选值与默认规则直接写入参数说明；前端配置查询接口保留。Agent 和 Core 按同机、共享文件系统部署。没有新增复制、云盘创建、成员管理工具，也没有改变论文检索策略或新增场景阅读框架。

`knowledge.document.upload` 的公开输入直接改为 `files[].path`。调用方先将附件、下载文件或 Artifact 准备成本地文件。Core 校验目标权限后读取普通文件，通过同一打开句柄检查和流式复制到独立原件存储；不接受旧 `source`、`file_ref`、URL 或 Artifact 版本字段。Chat 保留宿主文件授权，CLI 和外部 MCP 不要求 MCP roots、聊天会话或附件 ID。

MCP 每批最多 20 个文件，单文件 500 MiB、单批 1 GiB。格式目录与页面共用；支持保存不等于该部署具备对应解析器。基础内容校验会拒绝空文件、明显错误 PDF、错误容器和不匹配内容；复杂格式是否能完整解析由实际解析器确定。

知识库场景、标签、处理级别和阅读能力在一次本地事务中保存。`paper_collection` 输入规范化为 `academic_papers`；历史 `general` 不猜测迁移。内置场景使用既有能力目录，自定义预设校验访问权。仅更新能力保留场景，更新场景应用场景默认能力；显式能力覆盖默认值。解析服务交互与本地事务分开反馈。

## 页面—工具—公共服务对照

| 页面操作 | MCP | 公共实现 | 断言 |
|---|---|---|---|
| 直接创建、编辑知识库 | `knowledge.create`、`knowledge.resource.update_metadata` | `ResourceService`、`knowledge_configuration.go`、Learning 能力服务 | 标签、场景、算法、处理级别、能力一致；无第二次能力补写 |
| 创建选项、上传限制 | MCP 参数说明；前端 `GET /knowledge/options` 保留 | 既有 Learning 目录、算法目录、模型可用性和统一上传常量 | 同一场景枚举、默认值和限制 |
| 知识库列表 | `knowledge.list` | `DatasetCatalogService` | 来源、排序、ACL、标签和可选实际配置一致 |
| 文档列表、统计 | `knowledge.document.list`、`knowledge.document.aggregate` | `KnowledgeQueryService.filteredQuery`、聚合服务 | 同一目录、名称、路径、权限过滤；多标签分组可重复计入组，总数按文档去重 |
| 创建目录 | `knowledge.folder.create` | `ResourceService.CreateFolders` | 父目录、名称冲突、层级及批次预算 |
| 改名、标签 | `knowledge.resource.update_metadata` | `ResourceService.UpdateMetadata` | 按资源类型校验；目录递归标签作用于后代文档 |
| 页面移动任务、文档移动 | `knowledge.resource.move` | `ResourceService.Move` | 同库、跨库权限与整树事务；页面任务保留回执和可重放请求 |
| 删除 | `knowledge.resource.delete` | `ResourceService.Delete` | 删除立即生效，绑定失效；物理清理单列状态 |
| 浏览器字节上传、本地路径上传 | `knowledge.document.upload` | `OriginalService`、`bindPageOriginal`、`BindOriginalTx` | 原件独立、哈希一致、保存与处理提交分开 |
| 阅读、检索 | `knowledge.document.get`、`knowledge.search` | 现有读取、查询服务与有效资源过滤 | 当前归属和删除状态正确；文本及分块返回有界 |

聚合沿用扫描上限 10,000 文档、1,000 分组、最多 3 个维度，超过上限返回明确错误，不伪装完整结果。保留原有查询参数名和操作预算。已有处理级别修订与任务失效机制继续复用，不建设新的后台任务调度器。

## Chat 行为

- 已保存且已提交解析：立即报告“已加入，已提交后台解析”；仅保存时报告“已加入，已保存原件”。
- 移动提交后报告“已移动”；删除生效与后台物理清理分开表达。
- 保存成功但解析提交失败时保留成功资源，同时明确失败原因。
- 不固定 sleep、不自动循环查询；用户要求查看进度时才查询。结果不确定时先核对，不能重复执行写操作。
- 附件定位工具明确区分原件 `path`、阅读 `file_id` 和下载 URL。速览可使用附件文本，不依赖知识库解析完成。
- MCP 结果不序列化页面 `entry_url`。Chat 从可信工具结果生成资源入口卡片，以 `call_id` 去重并保存到会话历史。卡片使用当前前端路由，展示调用时快照；删除结果不提供失效链接。

## 兼容与部署

原有资源 ID、原件和阅读配置保留，不批量重新解析。上传是已确认的破坏性契约变化；Core、Chat、CLI、工具说明和生成类型同时更新。Chat 内置管理工具按运行实例发现 schema；外部客户端需重新连接并执行 `tools/list`，不能继续使用缓存的旧上传参数。

仅部署隔离 8091。必须从本仓库使用专用运行配置：

```sh
make -f Makefile \
  -f /Users/dengyuang/workspace/lazymind-runtime-8091/runtime-isolated.mk \
  LOCAL_CONFIG_ENV=/Users/dengyuang/workspace/lazymind-runtime-8091/runtime-8091.make.env \
  local-up
```

停止使用相同命令的 `local-down`。不操作 8090。

部署前停止 8091 写入，使用 SQLite backup API 备份 10 个数据库并执行完整性检查，单独保存原件、会话工作目录、索引和必要配置。备份位于仓库外私有目录，不包含依赖和编译缓存。原有未提交修改另存补丁。恢复须先停止 8091，按备份清单恢复对应数据库及数据目录，恢复上一版本代码及原有补丁，再使用同一专用命令启动；不得在运行中的 SQLite 主文件上直接覆盖。

## 验收记录

定向自动化：

- Go：`doc`、`capability/...`、`knowledgeruntime`、`learning` 回归通过；Chat 结果持久化、schema 和发布目录测试通过。
- Python：知识库管理运行时 36 项通过。
- 前端：创建、编辑、卡片路由合并共 15 项通过，包括自定义预设 settings 保留；Vite 构建通过。
- CLI：`mcpbridge` 测试通过；生成 OpenAPI 检查通过。

8091 实际验收：

- 公开认证 MCP 创建与页面 API 创建互相读取，对比场景、标签、级别和能力，无页面补写。
- 本地文件路径上传、删除源文件后下载原件并核对哈希。
- 损坏 PDF、旧来源契约拒绝；目录过滤与聚合数量一致。
- 目录后代追加标签、同库移动、跨库移动、删除后的旧引用失效。
- 浏览器字节上传经过共享原件绑定，页面展示已存储文档。
- CLI stdio 代理直接路径上传成功，未提供 roots。
- 页面创建表单实际选择论文集并创建专用 `KM-Parity-UI-20261020` 验收库。
- 所有内置场景逐项切换并核对默认能力；自定义预设的设置与 `profile_id` 可读回。
- 页面跨库移动与同一任务重放通过，目标文档不重复。验收发现并修复页面未加载部署移动预算、未注入原件服务两个调用链差异；补充依赖接入回归。
- 页面一次 PATCH 同时保存描述、标签、处理级别，并返回操作回执。超过 500 MiB 的稀疏文件、目录和无效目标被拒绝。
- 真实 Chat PDF 附件在 `stored` 和 `parsed` 两种知识库中均只上传一次；未调用固定 sleep 或解析状态轮询，速览使用附件内容。两轮耗时分别约 46 秒、34 秒（包含模型、工具发现及聊天附件预解析），不代表上传接口自身耗时。
- 两轮均产生一张持久化入口卡片；重启后历史恢复，在 `127.0.0.1` 和 `localhost` 下分别跳转当前站点文档详情。`parsed` 样例报告“已提交后台解析”，随后在详情页确认正文可读。
- 核实前端、Core、Chat 的仓库与运行根目录均属于 8091；8090 的 Caddy 进程仍为原 PID，未重启或修改。

完整 TypeScript 检查仍有项目类型错误，未宣称通过；Chat 全量包存在已记录的项目工作区注册基线失败，定向知识库测试与其区分。以上保存、提交验收不代表所有格式解析、向量检索就绪、完整 500 MiB/1 GiB 成功传输压力测试或 Windows 原生端到端验收。复杂文件的深层损坏仍可能在后台解析阶段才被发现，必须保留原件已保存与解析失败的区别。

详细请求、响应、运行日志、备份清单保存在本机私有验收目录；不将会话凭据写入仓库。
