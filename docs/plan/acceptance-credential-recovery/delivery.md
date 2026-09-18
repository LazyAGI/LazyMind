# 凭据恢复交付记录

## 结果与范围

2026-09-17，在用户批准阶段一测试后完成离线维护工具和当前验收环境恢复。真实根因是同一 SQLite 数据库在 Local/Desktop 间切换时，模型凭据加密密钥不一致；独立执行也会失败，未确认存在并发执行缺陷。

新增 `backend/core/cmd/acceptance-model-credentials/recovery.go`，复用现有 AES-GCM 编解码、SQLite 驱动和桌面设备密钥派生约定。默认 dry-run，显式 `--apply` 才更新。未更换提供商 API Key，未修改业务接口、数据库结构、默认加密配置或通知实现，也未提交、推送代码。

## 验证

在仓库的 `backend/core` 目录执行：

```bash
GOTOOLCHAIN=auto go test -race ./cmd/acceptance-model-credentials ./common/secretcrypto ./modelprovider -count=1
GOTOOLCHAIN=auto go build -o /tmp/lazymind-acceptance-model-credentials ./cmd/acceptance-model-credentials
gofmt -l cmd/acceptance-model-credentials
```

恢复工具 25 个场景通过，三个相关包 race 测试全部通过；构建、格式检查和仓库 `git diff --check` 通过。已批准的测试未因实现而调整。测试覆盖文件 SQLite、WAL 一致性备份、只读预检、重复执行、配置先/数据库先完成的中断恢复、写入回滚、私有权限及不安全输入拒绝。PostgreSQL/Compose 不在该离线工具适用范围；未将 SQLite 测试视为 Compose 验收。

## 真实环境执行

验收根目录：`/Users/zouyu/Library/Application Support/LazyMind-Notification-Acceptance`。

确认零活动任务并停止服务后，使用进程环境在内存中传入源密钥。首次预检发现遗留启动锁，核实锁对应进程不存在、服务已停止后才移除该遗留锁；工具不自动忽略锁。dry-run 与 apply 均报告需重新加密 1 条、已匹配 0 条。

备份目录：`/Users/zouyu/Library/Application Support/LazyMind-Notification-Acceptance/credential-recovery-backups/recovery-1482138577`。

目录权限 0700，备份数据库、原 Local 配置及原设备身份文件均为 0600。Local 配置已采用现有桌面身份派生的模型密钥，保持 Git 忽略和 0600 权限；没有输出真实密钥。现有设备身份逐字节保持一致。当前数据库和备份数据库 quick_check 均为 ok。

维护工具仅更新模型密文字段和指定 Local 配置。重启后的对比中，任务、计划、模型选择及连接元数据、通知偏好保持不变；部分种子/注册表因服务正常启动更新，未声称整个数据库逐字节不变。

同一个既有计划 `sched_4bb8940912ee4d5eb299c689e9bb39` 的真实执行结果如下：

| 模式 | 任务 ID | 结果 |
| --- | --- | --- |
| Desktop | `tc_c04d7d67f1bb4b9381b16f13235e24e3` | succeeded，output_status=ready |
| Local | `tc_2fc14e0eaca14131a7a2215083f88d73` | succeeded，output_status=ready |
| 再切回 Desktop | `tc_6a3483af61ae4056b055daa8e21787a0` | succeeded，output_status=ready |

三次模型输出均为“浏览器系统通知验收成功。”。桌面界面已确认最新任务“已完成”；修复前的三条失败记录保留。当前 Desktop 保持运行，Local 已停止；离线恢复无需重建桌面包。

## 通知待办与验收边界

三次成功任务的桌面通知仍为 pending，未发现桌面投递回执。桌面任务详情显示“等待交接”，通知记录区同时出现“加载失败，请重试”及“请求无效”。尚未确认原因，不能据此宣称 macOS 横幅正常。本次批准的凭据恢复完成标准要求单独记录通知结果，此处保持为未完成的通知问题，没有顺带修改通知或认证策略。

## 恢复与回退说明

本次保留备份，未执行回退。若维护被打断，应保持服务停止，使用相同身份与源密钥重跑工具预检，再显式 apply；工具识别源/目标混合密文并保留已匹配记录。源密钥通过受保护的进程环境提供，不应写入命令参数或文档。

如必须恢复旧备份，应先停止 Desktop/Local，另行备份恢复后的当前数据库及配置，再将同一备份批次的数据库与 Local 配置成对恢复。处理 SQLite WAL/SHM 前需确认无存活数据库连接；不要把旧主库文件直接覆盖到运行中的 WAL 数据库。该回退会回到原来的 Local 密钥状态，不应立即用 Desktop 打开。当前设备身份未改写，无需恢复它。恢复前仍需明确选择要保留的恢复后任务记录，避免覆盖后续业务数据。

## 通知问题后续更新

随后用户批准通知响应契约修复，已完成修复、重建，并验证新任务收到真实系统 delivered 回执。上面的 pending 描述保留为凭据恢复时的历史状态；见 `../desktop-notification-response-fix/delivery.md`。
