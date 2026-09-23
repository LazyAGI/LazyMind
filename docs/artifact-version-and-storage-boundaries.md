# Artifact 的版本与存储边界

## 版本语义

启用 `LAZYMIND_ARTIFACT_V2_ENABLED=true` 时，Artifact V2 是已迁移产物的版本事实来源：

- 文件面板使用 conversation binding 枚举 logical artifact，每个 artifact 展示 published head 指向的不可变 revision。restore 同时调整 current/published head，不重写历史交付记录。
- 历史消息使用 history binding 固定到交付时的 revision。后续更新或 restore 不改变旧消息附件；后续消息引用旧附件时，使用该消息之前最近一次交付，不允许解析到未来版本。
- legacy 行保留为兼容交付记录，不再负责决定 V2 面板版本。尚无 V2 binding 的旧数据、用户上传保留兼容读取。关闭开关时继续走 legacy 语义；本改动不做全量数据迁移。
- legacy 交付与 V2 revision/binding 在同一数据库事务中提交。V2 提交失败会回滚交付，不静默退回 legacy 成功；文件系统中的不可变快照不参与数据库回滚，未引用 blob 可留待回收。
- fork 按所选历史范围复制固定 revision，并创建独立的子会话 artifact/binding；后续 restore 不影响源会话。历史中的重复 legacy ID 按各消息可见的交付版本重写。
- purge 后 V2 映射会抑制 legacy 回退，不使删除的 artifact 在面板中重新出现。已签名链接仍受原签名有效期约束。

列表接口的 `projection=v2` 返回 published 面板列表，同时返回独立 `deliveries` 和 `history_order` 供消息引用解析。V2 面板查询批量读取 binding、head/revision/count，不逐条加载完整 revision chain；历史交付单独批量查询。不可变 file_list 快照以 ZIP 文件交付，不依赖当前 workspace 中的成员文件。

## 模块边界

Artifact 管理 owner、revision、head、binding、删除和 blob 可达性。Doc 仅负责通用静态文件签名/传输，通过 `staticstorage` 注册的 namespace/root/授权回调调用领域策略，不直接查询 Artifact 表。Chat/SubAgent/Fork 负责调用和 DTO 适配，不定义另一套当前版本。

## 持久化存储

`LAZYMIND_ARTIFACT_STORAGE_ROOT` 独立指定持久化 blob root。执行 workspace 可单独改变；新写入不再必须存放在执行目录。未配置时暂时回退至原 workspace 下的 `artifact-blobs`，以兼容现有安装。

Docker Compose 将宿主机 `${LAZYMIND_ARTIFACT_STORAGE_ROOT:-./data/subagent/artifact-blobs}` 挂载到 core 的 `/data/artifacts`，并显式传入容器内 root。默认复用原物理数据，不需要搬迁；旧 `subagent/artifact-blobs/` URL namespace 继续支持，新 URL 使用 `artifacts/`。本地 runtime 也显式传递独立配置。

已有 blob 的 storage key 包含原绝对路径。将已有数据迁移到不同机器或新 root 时，需要保留旧路径挂载，或另行迁移 storage key；仅修改配置不会搬迁历史文件。修改执行 workspace 前应为存量旧路径保留兼容挂载。不要将独立持久化卷随临时执行容器清理。

Compose 的 V2 开关默认仍为 false，遵循已有灰度策略；重建容器不自动开启 V2 或迁移旧数据。
