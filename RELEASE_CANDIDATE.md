# Release Candidate — Superpowers Dispatch Reliability Workflow Integration

验收日期：2026-10-20。**CODE_REVIEW_READY: PASS。EXTERNAL_TRIAL_READY: PASS，限下述支持范围。**
建议提交独立 Example PR 并邀请有 LazyMind/Codex 环境的开发者试用；没有 push、PR、合并或 Issue 评论。
这不是完整 SDD 转换器，也不是生产级任意 Agent 拦截器。

## 版本与可复现边界

| 项目 | 本轮实测版本 |
| --- | --- |
| 最新 fetch 的 origin/main | `3486910b9f761ae84f3f98b823bcb2e14d2500d4` |
| 原开发基线 | `17b5e716cffe754f95eac28f2626971a0fc095f4` |
| 分支 | `feat/superpowers-workflow-eval` |
| 最终执行器候选 commit | `732fab2eb28a486511bd522f34eb4a9d5fee6924`；后续提交只补交付文档、证据及离线证据检查器 |
| 首轮干净 checkout / 恢复修正版 | `633ed8fe7b1d65ae410023af6efacef8817472e5` / `a3a227afb3096deb4ea4084b431bdc968a4d3754` |
| Superpowers | v7.0.0，`bb92a77741419a4ab5f06e711a283343f1ada0c3`，未修改 |
| 客户端环境 | macOS arm64；Python 3.9.6、PyYAML 6.0.3、Git 2.50.1、Codex CLI 0.155.0-alpha.9 |
| 模型 | 外部 Codex 请求 gpt-6-astra / medium；实际后端身份 N/A。LazyMind 对话模型设置与该子进程独立 |
| 服务 | 既有本地 LazyMind，公开 workflow.control.v1 API；本轮未重建服务或认证运行二进制的 commit，实际服务 SHA=N/A |

原工作已保存为 `4291be83`，在最新 main 上无冲突整理；主 checkout 未修改。新 venv、干净 checkout、
新上游 clone、私有配置、新 Workflow namespace 和八个测试仓库/工作区均独立建立。服务和 Codex 登录
是明确的前置条件；不把“新的客户端环境”称作全新 OS/服务器安装。
[每个 Case 的源码版本及文件哈希](examples/superpowers-workflow-reliability/evidence/rc/provenance.json)。

## Gap List 与实际改动

| 级别 / 原缺口 | 处理结果与文件 |
| --- | --- |
| P0：只支持固定三个 Fixture ID | **已解决**。`task.py` 绑定一项已提交 Plan、完整 Base、linked worktree、允许修改文件、受保护验收文件和动态 Coverage→Checks；`project.py` 执行六步骤；`demo.py` 提供 CSV 演示 |
| P0：同名空白发布 HTTP 500 | **正常使用路径已解决**。`lifecycle.py` 查询包含禁用项的目录，精确比较文件/执行契约，再复用 ref/revision。旧 Core 空白重复发布缺陷未修复、不吞错、不盲目更新 |
| P0：隐含本机配置 | **已解决**。`cli.py` 提供 configure/install/preflight/run/status；URL、模型、CLI 路径、上游路径由用户指定；凭据 0600，不进入子任务目录/公开日志；`requirements.txt` 固定依赖 |
| P1：恢复路径不完整 | **已解决支持的校验失败恢复**。真实 API Retry 返回 403 后修正为 restore-brief → 登录页面 Retry → run --resume；保留失败 Attempt，拒绝任何已发生或不确定派发的自动重放 |
| P1：文件名/证据/进程退出 | **已解决**。Git NUL 分隔支持中文路径；`gates.py` 派发前复核 Brief 快照；实际子进程 PID/终态/usage；处理超时/信号时终止自有进程组；status 独立回读 Artifact |
| P1：说明与回归 | **已解决**。中英文 README、comparison、两 Issue 案例、release-drafts、索引；`test_rc.py` 新回归与离线 `verify_rc_evidence.py` |

原有 `gates.py`/`runtime.py` 仅做局部复用扩展；Workflow 仍为三个标准文件、六节点/六 required output。
**LazyMind Core、其他模块、子模块、Superpowers 均未修改。** 新证据只保留本轮选定 Trace/结果/输入，
不复制凭据、完整目录或重复 readback；原 89 文件和上一轮 38 文件历史证据的哈希完整保留。

## 真实验收结果

[完整命令、起止时间和退出码](examples/superpowers-workflow-reliability/evidence/rc/commands.json)；
[独立在线状态/Artifact/行为核验](examples/superpowers-workflow-reliability/evidence/rc/verification-live.json)。
负例中的任务 FAIL 是预期结果，验收 PASS 不等于该任务成功。

| Case | 判定 | 直接证据和核验 |
| --- | --- | --- |
| A 首次使用 | **PASS** | [csv-a](examples/superpowers-workflow-reliability/evidence/rc/csv-a/result.json)：新 venv/config/namespace；实际编译 diagnostics valid 后首次 publish；真实 Codex 修改并提交 CSV 解析；四项测试、六 Artifact、Core completed |
| B 重复使用 | **PASS** | [csv-b](examples/superpowers-workflow-reliability/evidence/rc/csv-b/result.json)：复用同 ref/revision，新 Session 完成；再次 install 没有 POST draft/publish。八个 Session 都固定 revision `b6a4f724-0804-479a-986c-6c5043cb096d` |
| C 自定义任务 | **PASS** | [Task 7 标签规范化](examples/superpowers-workflow-reliability/evidence/rc/custom-labels/result.json) 使用不同 Plan、三个自定义 Coverage ID、四个测试；最终代码另跑 [中文文件任务](examples/superpowers-workflow-reliability/evidence/rc/custom-unicode/result.json)，实际文件、提交和额外边界断言通过 |
| D 缺失 Brief | **PASS：预期阻断** | [最终代码负例](examples/superpowers-workflow-reliability/evidence/rc/missing-current/result.json)：真实删除 Brief，BRIEF_MISSING，0 个 Popen receipt、无 child trace、Dispatch execution=none |
| E 恢复 | **PASS** | [恢复前](examples/superpowers-workflow-reliability/evidence/rc/missing-before-recovery.json) / [恢复后](examples/superpowers-workflow-reliability/evidence/rc/missing-brief/result.json)：同 Session，校验 attempt 1 failed/stale、attempt 2 succeeded，之后只启动一个真实子进程；[UI 原始回执摘录](examples/superpowers-workflow-reliability/evidence/rc/ui-retry-receipts.json) |
| F 错误 Base | **PASS：预期阻断** | [wrong-base](examples/superpowers-workflow-reliability/evidence/rc/wrong-base/result.json)：在一次性仓库实际新增空 commit，Base mismatch，Prepare 失败、0 启动 |
| F Coverage 缺项 | **PASS：预期阻断** | [missing-coverage](examples/superpowers-workflow-reliability/evidence/rc/missing-coverage/result.json)：真实 Codex 成功后明确删除一行，Validate completion 失败，Record 未执行、无最终 result Artifact |
| 不兼容已有 Workflow | **PASS：预期拒绝** | commands 中 incompatible-install 返回 WORKFLOW_INCOMPATIBLE；对应 lifecycle trace 只有 GET，没有更新或重复创建 |
| 自动化回归 | **PASS** | [53 项测试](examples/superpowers-workflow-reliability/evidence/rc/units-current.log)。API double/timeout stub 是单元测试，不冒充 E2E |
| 格式/许可证/安全/历史证据 | **PASS** | [静态核验](examples/superpowers-workflow-reliability/evidence/rc/static-checks.json)：源码解析、YAML、文档链接、diff whitespace、白名单脱敏扫描、MIT 原文一致及历史 manifest 哈希 |

本轮总计六个真实 Codex 子进程、五个成功 Session、三个最终阻断 Session。最初 CLI 尝试通过外部凭据
Retry 的 **403 是实测失败**，保留在 missing-brief/trace.jsonl 和 commands.json，未改写成成功。
修正后的实际恢复依赖已登录 UI；这不是全自动恢复承诺。阶段时间、HTTP 状态、CLI usage 均保留原值。
零启动是本执行器 launch 路径的回执与未执行的 Runtime 步骤交叉验证，不是全系统进程审计。
没有自然复现旧原生 Bug，没有性能、成本或可靠性提升百分比；费用和模型认证均为 N/A。

## 两 Issue 覆盖与 Hard Gate 边界

| 能力 | #2282 / #2245 覆盖 | 实际控制点 |
| --- | --- | --- |
| Brief 非空、已生成、内容未被替换 | #2282 完整覆盖所声明路径 | Python Guard 在校验及 Popen 前读取同一快照；Runtime 控制步骤先后 |
| 可观察 in_progress、失败不成功 | #2282 完整覆盖执行器状态；UI Todo 未覆盖 | 私有事件/状态文件和 Artifact；成功记录只在实际完成检查后写入 |
| 正确 Base | #2245 完整覆盖当前工作区初始 HEAD | 完整 SHA 相等、Git identity；完成时保留 Base ancestry。不能替调用者判断其给定 Base 是否正确 |
| 可枚举任务范围 | #2245 部分覆盖 | Manifest/Plan token 关联、精确 ID 集、可信测试、保护文件及改动范围；不证明任意自然语言需求的完整语义 |
| 模型身份 | #2245 未覆盖 | 仅请求配置与 CLI 回执，实际后端 N/A，无 Hard Gate |
| 并行隔离 | #2245 部分覆盖，历史有界实验 | 独立 worktree/index、协作锁；RC 入口单任务。未验证任意进程隔离或共享 Index 写入 |
| Artifact / Attempt | 两者复用一套六步骤 | Core 的准入、当前 Attempt required slots 和租约是真约束；acceptance_criteria 文本本身不执行业务检查 |

`project.py` 仅在 accepted hosted attempt 内调用 Guard→Popen，派发失败无法走成功路径。
持有执行凭证的其他程序仍可提交自身 Artifact；此 Example 不是禁止其他执行器绕过业务校验的全局安全边界。
Codex 的 workspace-write 也不应等同于独立容器。准确定位为 **Dispatch Reliability Workflow Integration**。

## 剩余问题及发布建议

P0：**无，限已声明的单任务、本地可信输入与支持 API/CLI 范围**。必要 P1：已解决。
P2：Windows、全新 OS/服务安装、远程共享部署、本轮源码最新 main 的服务重建未独立 E2E；
模型证明、任意进程隔离、完整 SDD 多任务/Reviewer、长任务租约续期及崩溃后确定性恢复均未实现。
子进程限 600 秒，每条验收命令限 120 秒；须让整步处于服务租约内，不能据此运行无限/长批处理。
真实验收仅使用上述 Codex 版本和请求模型，其他版本由 preflight 和真实派发结果决定，不保证兼容所有 Host。

**CODE_REVIEW_READY = PASS**：适合独立 Example PR。生产 Runtime 零改动；业务 Gate、失败证据、回归和双语文档齐全。
**EXTERNAL_TRIAL_READY = PASS（有界试用）**：具备上述先决环境的开发者可从候选 checkout 安装、执行自己的有界任务、
重复使用、查看产物并按 UI+CLI 路径恢复。先用一次性 demo；公开分发仍由维护者决定，目前未上传任何分支。

[Quick Start](examples/superpowers-workflow-reliability/README.CN.md) ·
[证据说明与重跑入口](examples/superpowers-workflow-reliability/evidence/rc/README.md) ·
[PR / 两个 Issue 技术说明草稿](examples/superpowers-workflow-reliability/release-drafts.md)。
可把本地交付 bundle 发给评审人，使用 README 的 bundle checkout 命令；bundle 仅包含 Git 内容，不携带服务、凭据或私有运行目录。
