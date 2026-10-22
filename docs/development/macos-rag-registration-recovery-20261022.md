# macOS RAG 安装后启动失败：旧注册指纹冲突与本机恢复

日期：2026-10-22。本文记录一次 Mac ARM64 桌面客户端实测，供开发、测试和支持排障使用。

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
