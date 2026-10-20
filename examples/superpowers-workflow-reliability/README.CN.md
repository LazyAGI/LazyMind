# Superpowers 派发可靠性 Workflow 集成

[English](README.md) · [RC 验收](../../RELEASE_CANDIDATE.md) · [对照](comparison.md)

本例在隔离 Git worktree 中执行**一项明确声明的开发任务**。六步骤 Workflow 与外部
Python 执行器在启动 Codex 前检查非空 Brief、可观察的 in_progress 状态和真实 Base SHA；
完成后检查 Coverage、受保护测试、文件修改范围、真实测试结果及 Runtime Artifact。

这是手工编写的派发可靠性集成，不是自动 Skill 转换器，也不等同于完整 Superpowers SDD。
硬检查只约束本执行器启动的子进程，单独加载 YAML 不会拦截其他 Agent。

## 快速开始

需要 macOS/Linux、Python 3.9+、支持 `--path-format=absolute` 的 Git、Bash、已登录的
Codex CLI 和运行中的 LazyMind。[安装/启动服务](../../docs/quick_start.md)。本轮实测
Python 3.9.6、Git 2.50.1、Codex CLI 0.155.0-alpha.9；CLI 必须支持
`exec --ignore-user-config --ephemeral --json`。CLI 不在 PATH 时，配置命令可加
`--codex /path/to/codex`。MODEL 必须替换为自己账号实际可用的模型。

在包含本例的 checkout 中，选择一个不存在的新目录：

```bash
cd examples/superpowers-workflow-reliability
python3 -m venv .venv
.venv/bin/pip install -r requirements.txt
export TRIAL=/tmp/lazymind-reliability-trial
export LAZYMIND_URL=http://127.0.0.1:5024
export MODEL=gpt-6-astra
mkdir -m 700 "$TRIAL"
git clone https://github.com/obra/superpowers.git "$TRIAL/superpowers"
git -C "$TRIAL/superpowers" checkout bb92a77741419a4ab5f06e711a283343f1ada0c3
.venv/bin/python cli.py --config "$TRIAL/private/config.json" configure \
  --url "$LAZYMIND_URL" --local-login --superpowers "$TRIAL/superpowers" --model "$MODEL"
.venv/bin/python cli.py --config "$TRIAL/private/config.json" install
.venv/bin/python demo.py "$TRIAL/csv-a"
.venv/bin/python cli.py --config "$TRIAL/private/config.json" run \
  --task "$TRIAL/csv-a/task.json" --repo "$TRIAL/csv-a/worktree" --state "$TRIAL/csv-a/run"
.venv/bin/python cli.py --config "$TRIAL/private/config.json" status --state "$TRIAL/csv-a/run"
```

5024 是源码本地实例的默认**网关**端口，应使用所选实例的实际地址。`--local-login`
只访问显式指定的 loopback 本地登录接口，凭证保存在权限 0600 的文件，不输出 Token。
远程部署要求 HTTPS，用 `--token-file /private/session.json` 替代本地登录；文件格式为
`{"token":"已授权的令牌"}`，权限 0600。凭证过期后可替换为同一用户的新令牌。
不要提交 config、凭证、运行目录或用户项目 Trace。本例不配置对话/向量模型。

首次 install 查询所有可见 Workflow（包含禁用的），发布后以 manual 模式启用。
再次 install 会复用兼容 Ref，每次 run 也会检查文件语义及版本。不兼容时拒绝，
不覆盖旧草稿。需要并存新版本时，用新的 config 并指定
`configure --workflow-name YOUR_UNIQUE_NAME`，或通过正式 authoring 流程人工升级。
旧的同名空白发布 Core 缺陷未修改，但正常重复使用已不走该错误路径。

## 第二次运行与产物

无需重新发布，执行 `demo.py "$TRIAL/csv-b"`，然后使用同样的 run/status 命令，
将 csv-a 换成 csv-b，即创建新 Session。完整命令见[英文 README](README.md)。

实际代码留在 worktree；`git log -1`、`git show --stat` 可查看提交。
`run/result.json` 包含状态与 Artifact，`trace.jsonl` 是阶段/API 轨迹，
`child-trace.jsonl` 是真实 Codex 进程流。status 重新读取 Core 并保存
`readback-control.json`、`readback-artifacts.json`。完成产物包含测试命令、输出、
文件哈希、HEAD 和 diff。也可打开 `<前端>/workflow-runs/<session_id>`。
这些原始文件可能包含私有任务数据，不会自动对外发布。

## 适配自己的仓库

支持范围是一份已提交 Plan 中的一个编号任务、一个 Implementer、明确文件范围和事先
编写的确定性验收测试；不负责复杂项目环境安装、多任务调度或完整 Reviewer 循环。
Plan 和验收命令必须可信，命令使用本地用户权限执行。

先提交 Plan、测试及忽略 `.eval/` 的规则，记录完整 SHA，再执行
`git worktree add -b my-reviewed-task /path/to/new-worktree "$BASE"`。
必须是新的干净 linked worktree；主工作区会被拒绝。Manifest、config 和 state 放在其外。
以 demo 生成的 task.json 为模板，所有字段必须提供：

| 字段 | 要求 |
| --- | --- |
| schema_version | 1 |
| task_id | 小写短标识，出现在选中的 Plan 标题中 |
| plan / plan_task | 已跟踪的相对路径；整数 N 对应 `### Task N: task-id` |
| expected_base | 包含 Plan/测试的完整 40 位 Commit SHA |
| allowed_files | 允许修改的精确相对路径，不支持 glob、越界或 symlink |
| protected_files | 已提交且禁止修改的验收文件 |
| coverage | 唯一 `{id, description, checks:[check-id]}`；ID 出现在该 Plan 任务中 |
| checks | 唯一 `{id, argv:[程序,参数,...]}`，全部与 Coverage 关联；不做 shell 展开 |

每个要求应在测试中有真实断言。仅把 ID 关联到命令不证明命令有效；调用者必须提供
可信验收。执行器运行这些测试，并拒绝缺项、多项、重复、SKIPPED 或无文本证据的
Checklist。用自己的 task/repo 参数运行即可；变更 Manifest 不可复用旧失败会话。

## 故障与恢复

仅在一次性 demo 上给 run 添加 `--fault missing-brief`：真实脚本生成 Brief 后，
故障注入将其删除，Validate 失败且启动数为 0。负例预期 exit 1。
对这个失败 run（不是已成功的快速开始 run）执行：

```bash
.venv/bin/python cli.py --config "$TRIAL/private/config.json" restore-brief \
  --task "$TRIAL/csv-a/task.json" --repo "$TRIAL/csv-a/worktree" --state "$TRIAL/csv-a/run"
# 在已登录的 LazyMind 页面打开失败 Session，点击“重试”。
.venv/bin/python cli.py --config "$TRIAL/private/config.json" run --resume \
  --task "$TRIAL/csv-a/task.json" --repo "$TRIAL/csv-a/worktree" --state "$TRIAL/csv-a/run"
```

restore 从同一个已提交任务重新生成 Brief。随后在已登录的 Workflow 页面点击“重试”，
再用 --resume 认领 UI 创建的新 Attempt。Core 保留同 Session 的失败 Attempt。
外部凭证调用用户 control Retry 实际返回 403，CLI 会明确提示 UI_RETRY_REQUIRED，
不会冒充浏览器权限。
仅支持 Prepare 已成功且从未派发的校验失败。旧结果保存在 previous-results.jsonl。

错误 Base、Plan/测试被修改或 Manifest 无效时，检查错误并重新准备经过审阅的输入、
新 worktree 和 state，不要 reset 掉已有工作。派发失败、中断或结果不确定时先 status、
检查 Git 和 Trace，在 LazyMind 中处理原 Session，再有意识地创建新的任务；不自动重放。
已处理的超时/SIGINT/SIGTERM 会终止本执行器拥有的进程组，无法保证 SIGKILL、断电或
脱离进程组的孙进程。不要删除 state 隐藏未解决派发。子任务限时 600 秒，验收命令 120 秒。

## 验证与边界

```bash
.venv/bin/python -m unittest discover -s . -p 'test_*.py' -v
.venv/bin/python cli.py --config "$TRIAL/private/config.json" preflight
```

预检依赖、CLI 参数与登录、固定上游版本、Runtime 授权和 Workflow 兼容性，不隐藏调用模型。
模型可用性由实际被 Runtime 接受的派发验证；实际后端身份仍为 N/A。
Core 控制步骤准入、租约和必需产物，Python 执行器控制 Brief/状态/Base 与任务验收。
协作锁不等于 OS 隔离；Codex Todo UI 同步、模型认证、任意进程隔离和完整 SDD 对等仍未实现。

Before：原生 Skill 依赖模型遵循派发前约定。After：本执行器把选定前置条件变成可阻断
自己的派发检查。历史两个正常 Native 样本均成功，没有自然复现缺 Brief；不声称故障率、
速度或成本提升。详见 [RC 结果](../../RELEASE_CANDIDATE.md)、[#2282](cases/issue-2282.md)、
[#2245](cases/issue-2245.md)、[对照](comparison.md)。

cli.py 是当前用户任务入口；run.py/native.py/parallel.py 和旧证据保留固定 fixture 对照。
旧发布审计是有日期的历史快照，不能代替本轮代码验收。上游内容附有 [MIT 许可](UPSTREAM-LICENSE)，
新增代码使用仓库 [Apache-2.0](../../LICENSE)。
