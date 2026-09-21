# 算法跃迁 B1–B5 实现与验证

本次接续前端与 Core 的 A1–A7，修改 Evo、ArtifactRuntime 和必要的 Core/前端契约。没有修改 LazyLLM 子模块、已有默认模型或用户数据。

## 行为

| 项目 | 实现 | 验证 |
| --- | --- | --- |
| B1 真实状态 | 保留原 `status`，增量返回 `runtime_status` 和 `cleanup_pending`；详情、列表与事件投影一致。Core 透传，刷新后可展示终止中。 | 公开状态投影、Core 转换和前端重新挂载测试 |
| B2 模型固定 | 专用配置修改、通用 patch/replace/rollback 和最终提交统一校验。所有模型角色、凭证、端点和适配配置必须与 `run.config` 的创建版本一致。 | 25 个入口/字段组合与普通参数修改测试 |
| B3 能力验证 | 离线工具使用真实规划器、生成器、评测器、OpenCode 及完整五阶段执行链；Core 对精确配置保存验收报告，独立展示未验证/通过/未通过，不作为模型使用门槛。 | 协议失败、错误脱敏、缺失完整流程、授权、配置变化和验收状态更新测试；真实外部模型与隔离全流程验收仍待执行 |
| B4 取消 | 完成后取消保持完成；重复取消幂等；各阶段取消丢弃迟到产物。协作操作清理有超时；子进程无法确认停止时保留活动记录，不释放用户任务占用。 | 五阶段取消、清理超时、未确认子进程、已有产物保留及真实本机子进程测试 |
| B5 离页恢复 | Evo 已受理的创建/消息由服务持有任务并屏蔽请求取消，消息以原 message_id 重放。Core 在预留创建名额后完成上游创建和本地保存，不跟随浏览器取消。 | 创建断开、重新打开存储、消息断开及幂等重放、Core 请求断开后的本地任务/活动入口测试 |

运行中的任务不能通过普通消息刷新凭证或更换模型；需要改变模型配置时创建新任务。模型配置更新后旧验收结果不代表新配置，新配置展示“尚未验证”，仍按原有权限、连接验证、凭证与执行器适配规则判断能否使用。

## 真实模型验收工具

入口：`backend/core/cmd/evolution-validate` 与 `python -m evo.validation`。这是受信任运维工具，没有允许浏览器提交“验证通过”记录的接口。

先在具备项目 Python/LazyLLM/OpenCode 依赖、数据库访问和模型解密配置的隔离环境构建、运行：

```bash
cd backend/core
go build -o /tmp/evolution-validate ./cmd/evolution-validate
cd ../..
/tmp/evolution-validate \
  --user-id "$VALIDATION_USER_ID" \
  --model-id "$VALIDATION_MODEL_ID" \
  --isolated-inputs /private/path/evolution-inputs.json \
  --report /private/path/new-evolution-report.json \
  -- python -m evo.validation
```

数据库配置沿用 `ACL_DB_DRIVER`、`ACL_DB_DSN`，模型解密配置沿用 Core 的受控 Secret 来源。不要在命令行中填写密钥。省略 `--model-id` 时仅验证该用户配置的默认模型，不挑选任意候选。

`--isolated-inputs` 使用现有 Evo `ThreadInputs` 格式，例如：

```json
{
  "kb_id": ["isolated-test-kb"],
  "router_chat_url": "http://isolated-router:8000",
  "router_admin_url": "http://isolated-router-admin:8001",
  "algorithm_id": "isolated-baseline",
  "num_case": 1,
  "case_deadline_seconds": 120
}
```

这些地址与 ID 必须替换成真实隔离环境。**五阶段流程会正常执行候选发布/清理，禁止把生产或共享正式路由作为验证路由。** 工具使用独立的临时运行目录，但这一点不等于远端路由自动隔离。

不提供 `--isolated-inputs` 时仅执行协议探测并输出未通过报告。规划、样本生成、评测、OpenCode 工具编辑、完整五阶段流程必须全部通过，才能把报告标记为通过。DAG 完成但评测产物包含基础设施或契约失败时也不能通过。报告结果不控制模型列表和任务创建准入。

验证报告仅包含模型引用、随机关联标识、版本、各项结果、安全失败码及流程产物摘要，不包含凭证或上游异常原文。凭证通过进程 stdin 传递；运行目录与报告仅当前系统用户可访问。CLI 将报告摘要作为 evidence_id，不设置 24 小时过期；已有 expires_at 字段仅保留存储兼容性。保存前再次检查权限和精确配置，验证失败会将报告状态更新为未通过，而不会禁用模型。

模型验证包含真实调用，会产生供应商用量。必须分别完成当前默认模型和至少一个非 Qwen 模型的真实代表性流程，不能用单元测试、连接验证或手写数据库记录代替。

## 回归命令与当前边界

```bash
python -m pytest tests/evo -q
cd backend/core
go test ./agent ./modelconfig ./cmd/evolution-validate
TEST_DB_DRIVER=postgres TEST_DB_DSN="$TEST_POSTGRES_DSN" go test ./agent ./modelconfig
cd ../../frontend
pnpm exec vitest run src/modules/selfEvolution
pnpm run gen:openapi:check
pnpm build
```

算法实现阶段 Python 回归 74 项通过，包含实际子进程执行检查。模型准入修复后，前端模块 35 项通过，Core 的 agent、modelconfig 测试在 SQLite 和独立临时 PostgreSQL 16 下均通过；OpenAPI 一致性检查、前端构建、变更文件 ESLint 及变更格式检查通过。临时 PostgreSQL 容器和测试卷已清理。

目前未执行使用已保存凭证的外部模型验证：此前自动审批拒绝了读取模型凭证并跨容器调用供应商的命令。尚无默认/非 Qwen 模型的真实全流程证据，页面如实显示“尚未验证”。用户随后确认取消完整流程记录和 24 小时过期的强制准入条件；服务可部署用于测试，完整算法验收结果仍需单独取得。

请求取消屏蔽解决的是浏览器离页/断开边界。服务进程崩溃、数据库故障或超出服务超时仍遵循既有持久化和故障恢复规则，不宣称跨服务分布式创建具备 exactly-once 保证。
