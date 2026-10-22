# 供应商优先的模型设置

本阶段按 2026-10-22 最新确认实施：在主工作区 `feat/chat` 合入 `origin/main`；参考 [Cherry Studio 模型服务设置](https://www.cherryai.com/docs/en/pre-basic/settings/providers/)重做紧凑双栏布局。此前计划中“按协议合并连接、不同地址迁移为新供应商”的方案已由以下边界取代。

## 最终边界

- 只显示现有供应商，不新增供应商入口。供应商名称是左栏的顶层身份，不按协议或 SenseNova 的端点再拆分。
- 一个供应商允许多个连接实例；各自保留名称、多个 API Key 及模型列表。供应商内跨连接最多启用一个 Key，所有模型共用该 Key；切换先验证，停用或删除当前项后不自动选择其他 Key。
- API 地址由供应商统一管理。升级时按 `created_at ASC, id ASC` 选取第一个有效连接的地址，覆盖该供应商其他连接的地址；不合并或删除连接。
- 保留连接 ID、模型 ID、加密 Key 池、默认模型引用和凭据备份绑定。旧 `groups` 表及 API 名称保留作为兼容实现，页面称为“连接实例”。
- OCR、搜索等非模型服务继续使用原有独立配置。Phase 1 的 models.dev / OpenRouter 构建期目录、彩色模态标记及硬编码模型禁用策略保留。

## 实现任务

- [x] 主工作区创建 `feat/chat`，拉取并合入 `origin/main`，解决知识库及 LazyLLM 子模块冲突。
- [x] 供应商共享端点：`UserModelProvider.BaseURL` 为配置源，组地址为运行时兼容镜像；供应商端点更新在事务中同步连接并撤销检查状态。
- [x] 新增 `PUT /model_providers/{model_provider_id}/endpoint`，更新 OpenAPI 和生成客户端；连接创建、更新及备份恢复服从供应商地址。
- [x] 双数据库可逆迁移；保留旧地址和检查状态用于回滚，更新已有 v0_3 aggregate。
- [x] 供应商左栏、共享地址、连接切换、独立 Key 列表、模型表格，默认模型和云端备份使用抽屉。
- [x] 端点、凭据、迁移、前端交互和合并回归测试；浏览器检查桌面、窄屏及实际容器服务。
- [x] 重建必要容器、检查数据引用与服务健康，记录最终验证。

## 验证原则

迁移在独立 PostgreSQL 和 SQLite 中执行 up/down 和 aggregate 对比；业务库部署前备份。端点修改不得丢 Key、泄露明文、改变模型 ID 或绕过用户归属。旧备份恢复不能重新引入连接级端点。未保存地址切换供应商或离开设置必须提示处理草稿。

## 已验证结果

- 最新模型配置模块与设置入口：42 个前端测试文件、262 项测试全部通过；此前设置与知识库导航：21 个文件、215 项测试通过。
- Go：`modelprovider`、`modelconfig`、`chat`、`externalcapability`、`credentialvault`、`academic` 通过，文档存储和解析暂停相关回归通过；OpenAPI 契约测试通过，生成客户端检查为 fresh。
- 按最新要求取消 Key 轮询，改为供应商全局单 Key。运行时只接收启用项，旧换行格式兼容读取首个 Key；存储及备份保留所有 Key。Python 81 项相关回归通过；供应商启停、并发切换、跨连接聊天/共享模型/自进化/管理员嵌入及直接代理调用测试通过。
- 修复合入 LazyLLM 后 DocServer 序列化子进程的循环导入：解析服务包改为惰性导出。两个新解释器回归、实际 relay 子进程启动通过；实际容器 `/v1/health` 和 `/v1/ready` 均返回 200。
- PostgreSQL/SQLite 执行端点及单 Key 迁移 up/down；PostgreSQL 完整 dev/aggregate schema/data 一致；SQLite 验证新增结构与数据一致。覆盖端点迁移后改 Key 再回滚时不恢复旧验证状态，以及选择版本防止慢请求覆盖较新停用操作。
- 实际业务库升级至 `320261022065811`，dirty=false。8 个有效连接、12 个有效模型、2 个默认模型引用均保留，默认模型 ID 逐行一致；供应商端点与所有连接镜像一致。现有 Key 首次初始化为最早连接中的第一个，验证状态跟随该凭据。
- 核心和前端已重建并启动，Kong、Core `/health`、nginx 配置检查通过。单 Key 部署前追加数据库备份 `data/backups/provider-workspace-20261022/core-before-single-active-key.dump`。
- 浏览器核对 1440px 桌面、390px 供应商选择/详情及默认模型抽屉；实际配置正常显示，输入/输出彩标继续取上游目录。截图位于当前聊天的 model-settings 视觉产物目录。
- 全仓 `typecheck:all` 仍存在既有的聊天、工作流和测试类型问题；本次模型页及设置入口没有剩余类型错误。生产构建通过，不将全仓类型检查描述为全部通过。

- 多 Key 备份恢复验证支持合计超过 512 字节的保存池：每个 Key 上限 512 字节，完整序列化凭据记录上限 16KiB；页面仍只启用其中一个。
- 最终容器：Core、Frontend、Chat、Evo、DocServer、Processor Server/Worker、算法服务均已重建启动，已有但停止的 Milvus 依赖已启动。三类 Python 镜像 pip/import/health 冒烟检查均通过；实际依赖健康检查通过。
- 部署收尾曾观察到 PostgreSQL 子进程 exit 2 引发短暂恢复，随后数分钟未复现。独立检查未见损坏、OOM、磁盘或共享内存耗尽；未据此修改数据库配置。恢复后迁移版本、8/12/2 数据计数和默认选择 ID 再次核对一致。
