# macOS RAG 安装后启动失败：旧注册指纹冲突与本机恢复

日期：2026-10-22。本文记录一次 Mac ARM64 桌面客户端实测，供开发、测试和支持排障使用。

> 下文第 1–9 节保留首次诊断与清库恢复的历史事实；后续代码修复、配置用法及新验证结果见第 10 节。清库不是新版本的默认解决方式。

## 1. 结论与适用范围

本次有两个不同阶段，不能笼统称为“RAG 下载失败”：

1. RAG ZIP 下载、完整性校验、解压和安装验证已完成，用户数据目录存在与清单 SHA 对应的 `.ready`。
2. 安装后的本地服务重启失败：新客户端提交的读取器和切片规则指纹与持久化的旧注册信息不同，算法注册被拒绝，`lazyllm-algo` 退出。

最终在用户明确同意放弃现有知识库后，备份数据，清空知识库元数据、算法/节点组注册及旧索引，重启后成功重新注册。日志确认全部算法服务就绪、`local runtime ready`。聊天记录、模型配置、共享上传文件和已经安装的 RAG 组件保留。

这是**本机数据重置恢复**，不是已经完成的升级兼容代码修复。未验证旧知识库无损迁移，也未在恢复后完成新文档上传、解析、索引和检索的端到端测试。

## 2. 四类问题必须区分

| 层级 | 典型现象 | 含义与处理 |
| --- | --- | --- |
| macOS 应用启动检查 | Apple 无法验证应用是否含恶意软件 | Gatekeeper 对未公证测试包的启动限制。正式发布需 Developer ID 签名、公证；可信测试包可由用户明确允许打开。 |
| 原生库加载校验 | `mapping process and mapped file (non-platform) have different Team IDs` | 宿主 Python 与原生扩展的签名身份不兼容。正式包需配套签名后重新生成 ZIP 和清单；取消 SHA 校验不能解决。 |
| 下载完整性 | SHA、大小或 manifest 不匹配 | 下载内容与声明不一致。核对发布产物和清单，保持完整性校验。 |
| RAG 业务注册兼容 | `reader signature mismatch` / `Node group ... different signature` | 已保存的读取器/切片规则与新进程提交的定义冲突。本次实际阻塞点。这里的 signature 不是 Apple 签名或 ZIP SHA。 |

另一个独立 CI 问题是 macOS Bash 3.2 在 `set -u` 下展开空的 `signature_args` 数组时报 `unbound variable`。它让 CI 验证命令无法执行，不是本次客户端业务注册失败的原因。该问题已在 `cst/sig_test_fix` 的 `00a34711` 中通过非空参数数组修复，并有 53 项 Node 测试通过；不等于正式签名安装验收已通过。

## 3. 本次安装产物与证据

### 3.1 安装产物

- 平台：macOS ARM64，CPython 3.11。
- 模式：ad-hoc 测试包，不是 Developer ID 正式公证包。
- 文件：`lazymind-mac-arm64-rag-cp311-adhoc-20261022T023028Z-5126df1ce9799def.zip`。
- revision/tag：`5126df1ce9799def6530389ee17703d4e18ec721b1ea8d4f05448f52fe0213df`。
- ZIP 大小：66,794,259 字节。
- SHA-256：`a055fc726b138520ec3e661304b4bf16d2fab9f50a41644b9da8b7f0c746ae21`。
- 清单指向 ModelScope：`CarlosShaoting/lazymind-cst`，`master/RAG/tags/<tag>/`。
- 基础 fingerprint：`5b5439b677d7259f61145809658631ebe5694cacab17e8f1ec365ffd512d209f`。

本次观察到安装回执与 `.ready` 匹配，未另行重新下载 ZIP 做独立哈希复验。安装代码在验证 payload 和真实导入成功后才写 `.ready`，因此结合实际服务日志，可确认问题已越过安装验证阶段。

### 3.2 第一层错误：读取器注册

`lazyllm-algo.log` 中的关键报错：

```text
ValueError: [DocumentProcessor] update policy: reader signature mismatch
(existing=9c94500f535fcea4, incoming=f6115614ce89b7c7).
Set LAZYLLM_ALGO_REGISTER_POLICY=force to override.
lazyllm-algo exited: exit status 1
```

此时 Milvus、文档服务、解析服务可以已经 ready，但 `lazyllm-algo` 和依赖链没有完全就绪。不能仅依据某个健康检查或 `.ready` 宣称知识库业务可用。

### 3.3 第二层错误：节点组注册

首次只清理 `lazyllm_algorithm` 中唯一的 `general_algo` 注册记录后，重启不再停在旧读取器检查，而在 11:08:15（UTC+8）出现：

```text
Node group 'block' already registered with different signature
(existing=628ceaa9889d22d9, new=54a052f055f479c6).
Use a different name or version.
```

说明冲突并不局限于一条读取器记录。节点组仍保留旧定义，不能简单删除一行就完成恢复。该次尝试后已从备份恢复原 `general_algo`，没有继续擅自删除知识库。

## 4. 为什么重装 App 后仍然冲突

将 `.app` 移到废纸篓只移除程序本体，不会清空下面的用户运行数据：

```text
~/Library/Application Support/LazyMind/
  data/stores/sqlite/core/core.db       # 知识库元数据、对话等
  data/stores/sqlite/lazyllm/app.db     # 算法、读取器、节点组及解析状态
  data/homes/lazymind/sqlite/           # 切片存储
  data/stores/milvus/lazymind-v3/       # 向量索引
  deps/python-components/rag/          # 已安装 RAG 组件及回执
```

新应用继续使用同一数据目录，就会读到旧注册定义。这也是本地开发环境和桌面安装包共享用户数据时需要特别注意的边界。

代码链路（LazyLLM 子模块）：

1. `tools/rag/parsing_service/server.py::_load_reader_from_db` 从 `lazyllm_algorithm` 第一条记录的 `info_pickle` 恢复 reader。
2. `register_algorithm` 调用 `_validate_reader`。`update` 策略允许重新提交定义，但不允许读取器指纹改变。
3. 通过读取器检查后，`_upsert_node_groups` 计算节点组指纹；`_upsert_single_node_group` 对同名、不同指纹的非内置节点组拒绝覆盖，除非显式使用 `force`。
4. 注册异常沿远程调用返回，导致算法进程启动失败。

`DirectoryReader.signature()` 在 `tools/rag/data_loaders.py` 中将 local/global reader 注册映射转换为描述，排序后计算摘要。类/函数通常使用模块和限定名；部分 callable 使用源码摘要，取不到源码时可能退回 `repr`。因此“读取器指纹”比“代码文件 SHA”更准确。

**已确认的根因边界：持久化注册定义与新客户端提交定义不兼容，当前策略拒绝覆盖。** 尚未对两代 payload 做完整语义比较，不能确定具体变化来自读取器集合、代码/配置变化、序列化行为还是非稳定 callable 描述。启动日志中出现 AppTranslocation 路径，但这本身不能证明路径变化就是指纹变化原因。

## 5. 本次实际执行的恢复过程

### 5.1 先尝试最小修复，再明确扩大授权

1. 只读检查表结构，确认 `lazyllm_algorithm` 唯一记录为 `general_algo`，未发现该表相关触发器或声明的引用级联删除。
2. 用 SQLite `.backup` 创建 `reader-registration-20261022-1106/lazyllm-before.db`，完整性检查为 `ok`。
3. 停止服务，删除这唯一的旧注册记录并重启。
4. 发现 `block` 指纹冲突后，恢复原记录；没有将 `force` 设置为全局默认。
5. 用户进一步明确授权清空现有知识库，才执行下述知识库重置。

### 5.2 停止全部进程并备份

- 使用当前应用自带 `local-runtime-manager down --profile desktop`，并使用当前实例的 resources-root、repo-root 和 owner-token。实例信息从当前启动日志核对，不照抄其他实例的值。
- 本地开发 profile 不能用于关闭仍由 desktop profile 持有的运行实例；实例冲突保护不应绕过。
- 等待 supervisor 和业务服务停止。若正常关闭超时，先查看 shutdown 输出及回退清理结果。
- 本次桌面主进程在服务停止后仍未正常退出，先发送 TERM；确认仍残留后才结束已核实 PID。不要用宽泛的名称匹配批量杀进程。
- 再次检查进程列表，并用 `lsof` 确认两个数据库没有打开者。清理前的一次操作因客户端残留被安全检查拦截，没有执行；消除并发风险后才重试。
- 完整复制已停机的 `data/` 到 `backups/kb-reset-20261022-1112/data/`，约 600 MiB；Core、LazyLLM 两个备份库 `PRAGMA integrity_check` 均为 `ok`。

备份会包含私有聊天和配置数据，应保持本机受限访问，不上传到公共 issue、CI artifact 或代码仓库。备份目录末尾时间是目录标识，不作为精确审计时间。

### 5.3 清理范围

参考 `local/local-runtime-manager/reset.go` 的知识库表范围，但**没有直接执行 `reset --scope kb`**。原因是该入口会删除整个共享 `UploadRoot`，本机其中还有 `ai_generated`、`workflow-artifacts`、`workflow-workspaces` 等非知识库内容，超出本次保留聊天附件的目标。

| 对象 | 本次处理 |
| --- | --- |
| Core `tasks`、`upload_sessions`、`uploaded_files`、`documents`、`acl_kbs`、`default_datasets`、`datasets` | 事务内删除知识库相关记录；这里的 `tasks` 是文档任务表，不是对话历史表。 |
| `conversations.search_config` | 清为 `{}`，解除对旧知识库的搜索配置引用；保留对话行。 |
| LazyLLM 知识库、文档、解析状态、队列、算法与节点组表 | 按内置 reset 的表名单移除，启动时重建。保留其他表，不直接删除整个数据库。 |
| 切片 SQLite 目录 | 移到备份下 `retired-segment-index/`，新启动创建新存储。 |
| Milvus `lazymind-v3` 目录 | 移到备份下 `retired-milvus-index/`，新启动创建新索引。 |
| 聊天记录、账号、模型配置、共享上传文件 | 保留。 |
| RAG ZIP 安装结果、清单回执、`.ready` | 保留，不重新下载。 |

LazyLLM 本次重置表名单：

```text
lazyllm_doc_node_group_status  lazyllm_doc_parse_state
lazyllm_kb_algorithm          lazyllm_kb_documents
lazyllm_knowledge_bases       lazyllm_doc_path_locks
lazyllm_documents            lazyllm_doc_service_tasks
lazyllm_callback_records     lazyllm_idempotency_records
lazyllm_node_group           lazyllm_algorithm
lazyllm_waiting_task_queue    lazyllm_finished_task_queue
```

操作前必须检查当前版本 schema，不应把上述名单当作跨版本通用删除脚本。共享上传目录被保留意味着旧文档原始文件可能仍占磁盘；本次是逻辑知识库及索引清空，不是隐私擦除或所有原始文件的物理删除。扫描缓存也未扩大清理；仍需后续观察数据源同步是否重新导入内容。

### 5.4 重启与验收结果

重新启动同一测试客户端后，日志时间均为 UTC+8：

```text
11:12:07.830718  general_algo 注册记录重新生成
11:12:10        milvus-lite=ready, lazyllm-parse-server=ready,
                lazyllm-parse-worker=ready, lazyllm-algo=ready,
                lazyllm-doc-server=ready, algorithm-registration=ready
11:12:11.817    host algorithm services ready
11:12:11.819    local runtime ready
```

数据库复核：知识库从 3 个变为 0，文档从 15 条变为 0，对话仍为 82 条。重启阶段未再次出现上述 reader / block 指纹冲突。

登录后直接显示“已启用”符合预期：清理的是知识库数据，不是 RAG 组件。该状态也有服务启动日志支持，不仅是界面文本。若要测试“首次下载 RAG”，必须另用隔离的数据目录或专门卸载组件，不能将本次数据清空当作首次安装测试。

## 6. 可复用的处理决策

### 用户需要保留知识库

不要删除注册表、节点组或索引，也不要简单把默认策略改为 `force`。应先备份，比较旧/new reader 和节点组的规范化定义：

1. 若是无语义差异的指纹不稳定，修正确定性计算，并设计旧指纹到新指纹的兼容迁移。
2. 若确实改变解析或切片语义，给算法/节点组引入明确版本，迁移绑定并重建受影响文档索引；验证完成前保留旧索引和回滚路径。
3. `force` 虽能绕过注册冲突，但代码只覆盖定义，不代表已有切片和向量已重新计算。不能以“进程起来了”证明旧数据正确。

### 用户明确不要旧知识库（本次）

停机 → 完整备份与校验 → 按当前 schema 清空知识库状态 → 移走旧切片与向量索引 → 重启重建注册 → 检查全服务 ready → 上传小文档做端到端测试。

如果只想验证安装包，优先使用独立用户运行目录，避免开发版和桌面版混用状态。具体隔离配置须按当前 runtime 支持参数核实，不建议临时改目录后忘记恢复。

## 7. 回滚注意事项

本次备份保存在用户 runtime 的 `backups/kb-reset-20261022-1112/` 下。可恢复不等于运行中直接覆盖：

1. 停止桌面客户端、所有服务和数据库打开者。
2. 先备份重置之后产生的新数据；恢复旧快照可能覆盖新聊天、新配置等变更。
3. 一致恢复相互关联的 Core 元数据、LazyLLM 状态、切片与向量索引，不只恢复一张表后继续写入。
4. 若恢复全部 `data/`，必须向用户说明它会回到备份时点；若只恢复知识库，则需要有针对性的表/文件迁移，不能直接覆盖整个 Core 库。
5. 旧状态恢复后原指纹冲突也可能回来，仍需兼容旧定义的客户端或正式迁移方案。

## 8. 后续代码修复建议（尚未实施）

- **可诊断性**：注册冲突使用独立错误码，记录旧/新指纹、算法/节点组标识及安全的定义差异；不要输出完整 pickle、密钥或用户文档。
- **状态区分**：明确展示“组件已安装”和“知识库服务可用”，安装后重启失败时直接显示注册冲突，避免一直“正在重启”。
- **升级兼容**：定义解析器/节点组版本和迁移契约；检测旧库时给出备份迁移或用户确认重建选项，不自动清空。
- **指纹稳定性**：覆盖源码缺失、callable 实例、不同安装路径、进程重启及序列化往返，避免把内存地址等非语义信息带入持久化指纹。
- **安全重置**：把知识库原始文件与共享聊天/工作流上传目录分开；在 reset 前预览范围并备份，禁止运行中重置。
- **退出流程**：排查此次业务服务已停止但 Electron 主进程仍残留的问题；该现象与注册冲突不同，不能用强制结束进程代替产品修复。

建议回归矩阵：空数据首次装 RAG；已安装组件重启；旧库升级保留数据；真实解析规则变化需要重建；无语义变化跨路径重启；注册失败 UI；知识库 reset 保留聊天附件；Developer ID 同 Team 配套包；ad-hoc 配套包；断网重启复用组件。

## 9. 开发定位入口

- `backend/core/systemdeps/python_components.go`：组件验证、`.ready` 写入与已安装判断。
- `algorithm/lazyllm/lazyllm/tools/rag/data_loaders.py`：读取器指纹计算。
- `algorithm/lazyllm/lazyllm/tools/rag/parsing_service/server.py`：旧 reader 恢复、注册策略、节点组冲突检查。
- `algorithm/lazyllm/lazyllm/tools/rag/doc_impl.py`、`transform/factory.py`：节点组与转换定义指纹。
- `local/local-runtime-manager/reset.go`：现有 KB reset 范围，包括共享 UploadRoot 风险。
- 本机日志：`~/Library/Logs/lazymind-desktop/desktop-startup.log`、`~/Library/Logs/LazyMind/lazyllm-algo.log`、`lazyllm-parse-server.log`。

相关发布链路说明见 [macOS RAG 发布文档](macos-published-rag.md)。本文没有修改发布校验、签名策略、LazyLLM 子模块或产品运行逻辑。

## 10. 后续修复：同名分支 `cst/sig_test_fix`

### 10.1 新证据与结论修正

同事报告验证版本 LazyMind `50c4e83f`、LazyLLM `6b6ac9bb`，并定位三项独立问题：DocServer 冷导入循环、旧节点组签名冲突、配置覆盖导致 force 未生效。本文作者的修复基于本地获取到的 LazyMind upstream `fed34309` 和 LazyLLM upstream `655cb3ba`，不声称复测了同事的两个具体提交。

已在独立 Python 进程复现：`from lazyllm.tools.rag.doc_service import DocServer` 报 partially initialized module。链路为 `doc_service.base → parsing_service.__init__ → server → worker → doc_service.base`。导入 `parsing_service.base.TaskType` 之前会初始化包，旧初始化器立即加载 worker，后者读取尚未定义的 `DOC_NODE_GROUP_STATUS_TABLE_INFO`。

代码也确认 `GeneralParser.sig_fields()` 现有实现包括 `embed_max_input_tokens` 与 `budget_version`；同事报告其新旧计算值对应此次 `block` 冲突。没有删除这些字段来伪装兼容：embedding 输入预算确实可能影响新文档切分，应保留这一配置事实。开发场景按用户指定策略接受新旧处理结果共存，显式覆盖注册，而不是自动清库。

### 10.2 已实现的修复

1. **LazyLLM 包导入**：`parsing_service/__init__.py` 改为与 RAG 根包一致的按需导出，保留 `_Processor`、`DocumentProcessor`、`DocumentProcessorWorker` API。只导入 TaskType 不加载 server/worker。
2. **正式 wheel 路径**：`desktop/scripts/patch-reader-imports.py` 同时修补 pinned wheel 的 RAG 根包和 parsing_service 初始化器。否则仅更新子模块不能覆盖不打包源码的正式发行构建。
3. **显式覆盖开关**：新增 `LAZYMIND_FORCE_ALGO_REGISTER`，这是布尔值；开启后最终注册策略为字符串 `force`。它优先于旧的 `LAZYLLM_ALGO_REGISTER_POLICY=none/update`，不是把 policy 设为字符串 `false`。
4. **桌面运行时**：desktop profile 默认启用覆盖，避免同一显示版本的测试包重装仍受旧指纹影响；显式设置新开关为 `false` 可恢复原有策略选择。该开关为 true 时不受旧 policy 环境值影响。
5. **本地开发**：`local/config.env.example` 中此开关默认 false，仅在需要覆盖注册时显式开启；只有 Desktop 默认 true。已有的 gitignored 配置不自动修改。Make 配置加载顺序与 upstream 一致：local targets 先加载 `local/config.env`，`.env` 的同名配置随后覆盖它，命令行变量仍最高优先级。镜像文件按此顺序确定的 `MIRROR_PROFILE` 加载，不再在加载镜像文件后重新覆盖 profile。
6. **Compose**：将新开关传给 `lazyllm-algo`，默认 false，不强制改变所有服务端部署。算法构建入口在创建 Document 前更新 LazyLLM 配置和子进程环境，并记录 effective policy。即使容器原始旧 policy 仍为 none，新开关为 true 时最终业务注册使用 force。
7. **CI Bash**：保留此前 `00a34711` 的非空验证参数数组修复，覆盖 ad-hoc 与正式签名两条路径。

两仓库使用同名分支。LazyMind 的 gitlink 固定到本次 LazyLLM 修复提交；`.gitmodules` 保持官方 URL 和 main。合并本 PR 前，需先将 SDK 修复合入官方仓库，并将 gitlink 更新到官方可获取且包含修复的提交，确保 CI 能正常初始化子模块。

### 10.3 配置方式与生效检查

本地开发默认关闭此开关。需要显式启用时，在 `local/config.env` 中配置：

```dotenv
LAZYMIND_FORCE_ALGO_REGISTER=true
LAZYLLM_ALGO_REGISTER_POLICY=update
LAZYMIND_RESET_ALGO_ON_STARTUP=false
LAZYMIND_RESET_ALL_ON_STARTUP=false
```

然后重启本地运行时。开关会令下游 `LAZYLLM_ALGO_REGISTER_POLICY` 实际变为 force。不要启用 reset 开关来替代 force；reset 会删除数据。

Compose 不会因为文件叫 `local/config.env` 就自动加载它。部署者可以在实际使用的 `.env` 中设置新开关，或在已核对的 Compose 启动命令前传入：

```bash
LAZYMIND_FORCE_ALGO_REGISTER=true docker compose up -d --force-recreate lazyllm-algo
```

仍需使用自己环境的 Compose 文件、项目名和 env-file 参数。先检查 `docker compose config` 中 `lazyllm-algo.environment` 的两个变量，再检查新容器的环境和日志：

```text
[build_document] effective algorithm registration policy=force
```

不要只查看宿主 shell 的 `echo`，不要把整个 Compose 配置或容器环境贴到公开日志，以免暴露密钥。本次只执行 Compose 配置解析，并未重建用户 Docker 容器。

桌面从 Finder 启动不继承任意终端 export；默认覆盖由运行时生成子进程环境，因此无需用户手工 export。高级用户若关闭新开关，原有版本升级逻辑仍保留：显式旧 policy 优先；没有旧 policy 时安装版本变化可触发 force、同版本使用 update。

### 10.4 数据语义与测试边界

force 更新旧算法注册 payload 及节点组指纹/配置，保留同名节点组 ID 和既有 lazy_mode；没有调用清表、删除向量或自动重新 embedding。它不解决 Team ID、SHA、云端 404 或模型更换造成的向量空间不兼容。

新增 SQLite 保留数据测试：建立旧 `block` 指纹，先断言 update 拒绝，再 force 更新，检查节点组 ID、lazy_mode 及独立持久化文本/向量字节哨兵保持不变。该测试验证注册操作不清理持久化内容，**不是完整 Milvus 老知识库检索验收**。未将这次测试称为所有历史数据无损兼容保证。

本轮测试：LazyLLM 冷导入、注册保留数据、DocServer 和 parsing chunks 共 42 项通过；LazyMind 配置优先级、真实环境子进程、独立 DocServer `--help` 冷入口、wheel 修补、算法构建和 GeneralParser 共 34 项通过；桌面 Node 测试 53 项通过。`local/local-runtime-manager` 下 `go test ./...` 全部通过。独立冷入口验证覆盖 import 阶段，不冒充完整独立 HTTP 服务启动测试。

剩余验收：新 CI 安装包在保留旧知识库环境中启动，并实际检索旧文档、添加新文档及更新/删除文档。已有本机知识库已经按用户要求清空，因此不能用本机空库状态替代这一验收。历史记录中关于 UI 更精确错误展示、安全 reset 范围和退出残留的建议仍属后续任务，本次未扩大实现。
