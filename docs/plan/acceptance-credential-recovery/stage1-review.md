# 阶段一：恢复测试待 Review

## 当前结果

2026-09-17：25 个叶子测试场景，其中密钥不一致的复现测试通过，24 个恢复场景预期失败。失败均来自尚未实现的函数/命令入口，不是编译失败、Fixture 失败或环境阻塞。

`backend/core/cmd/acceptance-model-credentials/recovery.go` 仅包含类型、无业务行为的占位函数及入口；函数返回不支持，命令退出码为 2。没有读取或写入实际配置的实现，没有执行真实凭据恢复，不应使用此骨架操作验收环境。

## 需求与测试映射

| 场景 | 测试 |
| --- | --- |
| 复现 Local / Desktop 密钥不匹配 | `TestRecoveryReproducesLocalDesktopMismatch` |
| 默认检查不写入 | `TestRecoveryDryRunDoesNotWrite`、`TestRecoveryCLIDefaultIsReadOnlyAndOutputIsSafe` |
| 恢复、元数据保留、私有备份、配置统一 | `TestRecoveryApplyPreservesDataAndBacksUp`、`TestRecoveryReplacesExistingSourceConfigKey` |
| 重复与中断恢复 | `TestRecoveryRepeatIsIdempotent`、`TestRecoveryResumesWhenConfigAlreadyUsesDesktopKey`、`TestRecoveryRepairsConfigAfterCredentialsAlreadyConverted` |
| 密钥/身份/运行状态/路径不安全 | `TestRecoveryRejectsUnsafeInputWithoutWrites` 的 11 个场景 |
| 备份/配置文件写入失败 | `TestRecoveryFilesystemFailureDoesNotChangeCredentials` 的 2 个场景 |
| 数据库写入失败与错误脱敏 | `TestRecoveryWriteFailureRollsBackAllRowsAndConfig` |
| WAL 备份一致性 | `TestRecoveryBackupIncludesCommittedWALData` |
| 显式写入、错误退出与命令输出 | `TestRecoveryCLIApplyAndErrorOutput` 的 2 个场景 |

## 命令与结果

从 `backend/core` 运行：

```sh
GOTOOLCHAIN=auto go test ./cmd/acceptance-model-credentials -count=1 -json
GOTOOLCHAIN=auto go test ./common/secretcrypto -count=1
GOTOOLCHAIN=auto go test ./modelprovider -run '^TestAPIKeyForGroupMigratesLegacyPlaintext$' -count=1
```

- 第一条：退出码 1，1 个复现测试通过、24 个待实现场景预期失败。
- 第二条：既有加解密测试通过。
- 第三条：已随加密相关测试组合执行通过。
- `gofmt` 已执行，`git diff --check` 通过。
- 日志：`/tmp/lazymind-credential-recovery-stage1.jsonl`、`/tmp/lazymind-credential-recovery-crypto.log`、`/tmp/lazymind-credential-recovery-baseline.log`。

## 待批准后的工作

实现离线维护工具，完成所有测试并验证备份/恢复路径；再停止对应验收服务并备份、恢复当前一条凭据和 Local 加密配置，分别验证 Desktop/Local 的任务执行，最后保持 Desktop 运行。密钥不经日志或命令行传递，不对提供商 API Key 做更换，不改变产品默认安全配置。

本轮真实数据库、设备身份、Local 配置均未修改；任务故障尚未恢复。当前所有数据测试使用临时文件 SQLite，无 PostgreSQL/Compose 验收，原因见 `spec.md` 的部署边界。

## 后续状态

用户已回复“确认”批准本批测试。阶段二已完成；本文件保留阶段一失败结果作为历史记录，最终验证见 `delivery.md`。
