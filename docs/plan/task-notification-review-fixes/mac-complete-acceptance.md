# macOS 完整人工验收手册：定时任务、多渠道与原生通知

适用：当前工作区的审查修复代码，macOS Apple Silicon（arm64）。2026-09-16 的旧 DMG 不含后续审查修复，不能作为本次验收版本。当前修复尚有未提交文件，直接使用当前工作区构建，不要先执行 pull、checkout 或 reset。

2026-09-17 启动修复已在独立验收包验证：首次启动预热及关闭检查通过，正常模式 15 个服务全部 running，窗口已进入模型配置页面。修复包为 `/Users/zouyu/Downloads/LazyMind-acceptance-repaired-20260917/LazyMind.app`，对应验收目录为 `/Users/zouyu/Downloads/LazyMind-manual-05uNt5`。该包基于原构建替换 Caddy、恢复本地 LazyLLM 并应用已批准的 Python 启动钩子修复；仓库原 desktop/dist 包尚未重建，不能混用。下面的完整构建命令已纠正，但修复后的整条 make 流程尚未重新执行。

这是供验收人执行的流程，不表示本手册中的真实投递、构建或系统操作已经执行。预计桌面主线 30–45 分钟，三渠道另需 30–60 分钟，不含构建、模型配置及平台账号准备。测试失败时停在该步骤，不用重新运行任务掩盖通知问题。

命令面向同一个 zsh 终端。`m` 复用已有 acceptance.py；`nreq` 使用同目录 manual-api.py 发送受保护的本地 API 请求，只读取已有登录凭证，不自行登录、刷新、重试或确认通知。每条请求打印 HTTP 状态和结果文件名；JSON 结果保存在独立验收目录，Core 的 data 包装已经解开。不要整体上传该目录，里面可能有登录凭证、二维码、机器人资料和任务正文。

## 1. 检查工具与旧实例

```sh
cd /Users/zouyu/Downloads/LazyMind-main
uname -m
git status --short
git log -1 --oneline
go version
node --version
pnpm --version
uv --version
python3 --version
jq --version
pgrep -x LazyMind
```

预期 arm64；工具均可用。缺少工具先安装对应开发环境，不能跳过构建。`pgrep` 无输出且退出码 1 表示没有旧实例，不是验收失败。若有旧实例，确认其任务已结束，通过菜单/活动监视器退出；不要批量 kill Python/Go，也不要强制中断业务任务。

```sh
open -a "Activity Monitor"
pgrep -x LazyMind
```

后续验收需要专用模型配置、测试用户 A，以及用于隔离测试的用户 B。三平台使用专用测试账号/机器人和明确允许接收测试消息的对象，不使用生产机器人轮换 Secret。没有某平台条件时，将该项记为“未测”，不能记为通过。

## 2. 建立证据目录并重建当前代码

```sh
umask 077
export LAZYMIND_ACCEPTANCE_REPO="$PWD"
export LAZYMIND_ACCEPTANCE_ROOT="$(mktemp -d "$HOME/Downloads/LazyMind-manual-XXXXXX")"
chmod 700 "$LAZYMIND_ACCEPTANCE_ROOT"
git rev-parse HEAD > "$LAZYMIND_ACCEPTANCE_ROOT/base-commit.txt"
git status --short > "$LAZYMIND_ACCEPTANCE_ROOT/worktree-status.txt"
set -o pipefail
# 单独生成与锁文件匹配的 Skill 缓存，不把整个构建固定到 Go 1.25。
(
  cd backend/core
  GOTOOLCHAIN=go1.25.0 go run ./cmd/builtin-skill-bundle \
    --sources ../../skills/builtin-sources.yaml \
    --lock ../../skills/builtin-skills.lock.json \
    --cache ../../desktop/cache/builtin-skills \
    --output "$LAZYMIND_ACCEPTANCE_ROOT/skill-preflight/builtin-skills" \
    --featured-sources ../../skills/featured \
    --featured-output "$LAZYMIND_ACCEPTANCE_ROOT/skill-preflight/featured-skills" \
    --frozen-lockfile
)
# 上一步成功后再执行完整构建。验收包装脚本只选择工具链和冻结 Skill 锁文件。
export LAZYMIND_ACCEPTANCE_REAL_GO="$(command -v go)"
export GO="$LAZYMIND_ACCEPTANCE_REPO/docs/plan/task-notification-review-fixes/acceptance-build-go.sh"
chmod +x "$GO"
LAZYMIND_RELEASE_BUILD=false LAZYMIND_DESKTOP_SIGNING_MODE=adhoc make desktop-darwin-arm64 \
  2>&1 | tee "$LAZYMIND_ACCEPTANCE_ROOT/build.log"
```

构建会下载依赖并重建 desktop/build、desktop/dist 生成目录，不清业务库。必须返回 0；如需保留旧构建文件，执行前自行移到单独目录。不要用 `make clean/reset/clear` 排查。

验收包装脚本将 Skill 打包和 Caddy v2.10.2 固定到 Go 1.25.0，其余 Go 操作使用 auto，允许 process-compose v1.116.0 使用所需的 Go 1.26 或更新版本。不要把整个构建固定到 Go 1.25。当前机器已实际复现：Go 1.27 构建的 Caddy 加载配置时 panic，Go 1.25.0 构建的相同版本可通过同一配置校验。

必须保留 `LAZYMIND_RELEASE_BUILD=false`，让当前代码所需的本地 LazyLLM 一起打包。之前建议的 true 会移除这份源码，导致聊天服务缺少 `WriterExecutionTools`，不能作为当前验收构建命令。包装脚本通过现有 GO 覆盖入口独立启用 Skill 冻结模式，不修改生产构建脚本、依赖版本或锁文件。这是本机验收环境的构建兼容措施；正式发布构建仍需单独修复和验证。

若此前在 diagram-design 报 `GitHub ref lookup failed with HTTP status 403`：本机已确认 GitHub 未认证 API 限额耗尽。非冻结模式会重新查询 ref；锁文件模式直接使用已固定的归档地址。不要把 URL 改成 Markdown 链接、删除 Skill 或关闭哈希检查。当前机器默认 Go 1.27 还会使该 Skill 重新生成的 ZIP 与锁文件不一致；Go 1.25.0 已完成缓存预热，随后默认 Go 1.27 的锁文件模式也已实际完成 29 个内置 Skill 和 26 个精选能力打包。不要清除 desktop/cache/builtin-skills；清除后需要重新预热。完整应用构建仍须以上述命令成功退出为准。

```sh
export LAZYMIND_ACCEPTANCE_APP="$LAZYMIND_ACCEPTANCE_REPO/desktop/dist/mac-arm64/LazyMind.app"
export LAZYMIND_ACCEPTANCE_BIN="$LAZYMIND_ACCEPTANCE_APP/Contents/Resources/runtime/bin/lazymind"
test -x "$LAZYMIND_ACCEPTANCE_APP/Contents/MacOS/LazyMind" && echo APP_OK
test -x "$LAZYMIND_ACCEPTANCE_BIN" && echo CLI_OK
codesign --verify --deep --strict "$LAZYMIND_ACCEPTANCE_APP"
printf '%s' '{' | "$LAZYMIND_ACCEPTANCE_BIN" internal session renew
```

预期 APP_OK、CLI_OK、签名退出码 0；最后一条故意传非法 JSON，预期 `DESKTOP_SESSION_INVALID`，用于确认新续期 CLI 已打包，不会发起真实刷新。

## 3. 启动隔离实例、登录并配置模型

```sh
cd /Users/zouyu/Downloads/LazyMind-main
export LAZYMIND_ACCEPTANCE_REPO="$PWD"
if [ -z "${LAZYMIND_ACCEPTANCE_ROOT:-}" ]; then
  export LAZYMIND_ACCEPTANCE_ROOT="$(mktemp -d "$HOME/Downloads/LazyMind-manual-XXXXXX")"
fi
chmod 700 "$LAZYMIND_ACCEPTANCE_ROOT"
export LAZYMIND_ACCEPTANCE_APP="$LAZYMIND_ACCEPTANCE_REPO/desktop/dist/mac-arm64/LazyMind.app"
export LAZYMIND_ACCEPTANCE_BIN="$LAZYMIND_ACCEPTANCE_APP/Contents/Resources/runtime/bin/lazymind"
export LAZYMIND_HOME="$LAZYMIND_ACCEPTANCE_ROOT/credentials"
export LAZYMIND_DESKTOP_RUNTIME_ROOT="$LAZYMIND_ACCEPTANCE_ROOT/runtime"
export LAZYMIND_JWT_TTL_MINUTES=2
export LAZYMIND_ACCEPTANCE_HELPER="$LAZYMIND_ACCEPTANCE_REPO/docs/plan/task-notification-review-fixes/acceptance.py"
export LAZYMIND_DESKTOP_AGENT_CONNECTOR="$LAZYMIND_ACCEPTANCE_REPO/docs/plan/task-notification-review-fixes/acceptance-cli.sh"
chmod +x "$LAZYMIND_DESKTOP_AGENT_CONNECTOR"
m() { python3 "$LAZYMIND_ACCEPTANCE_HELPER" "$@"; }
nreq() { python3 "$LAZYMIND_ACCEPTANCE_REPO/docs/plan/task-notification-review-fixes/manual-api.py" "$@"; }
"$LAZYMIND_ACCEPTANCE_APP/Contents/MacOS/LazyMind" \
  --user-data-dir="$LAZYMIND_ACCEPTANCE_ROOT/profile" \
  > "$LAZYMIND_ACCEPTANCE_ROOT/launch.log" 2>&1 &
LAZYMIND_ACCEPTANCE_PID=$!
printf '%s\n' "$LAZYMIND_ACCEPTANCE_PID" > "$LAZYMIND_ACCEPTANCE_ROOT/app.pid"
```

若启动立即 exit 127，先读取本次 `launch.log`。日志若为 `no such file or directory: /Contents/MacOS/LazyMind`，说明 APP 环境变量为空，并非构建产物损坏；完整重执行本步骤的变量初始化，不必重新构建。应用尚未启动/登录前，先不要执行后面的 me、通知配置或 jq 检查。

等首次初始化完成，在应用中登录测试用户 A，配置可用模型，在普通聊天发送“只回复：模型可用”。必须收到真实结果。应用运行数据、凭证和页面配置分别隔离；默认 macOS 日志目录未全部隔离，不能把原始日志直接外发。

```sh
m status
m me
test -f "$LAZYMIND_ACCEPTANCE_ROOT/profile/credential-device.json" && echo PROFILE_OK
nreq GET /api/core/user/notification-preferences 200 prefs-original.json
jq '{enabled,revision,defaults}' "$LAZYMIND_ACCEPTANCE_ROOT/prefs-original.json"
```

预期 active 用户、`ttl_seconds=120`、剩余时间为正。新用户通知总开关默认 true、成功/失败开启、桌面开启，人工等待关闭。若 API 为 401，本工具不会帮你刷新：保持应用前台并确认已登录，或等待后台续期后重新执行读取。不得手工粘贴内部服务 Token 绕过认证。

```sh
open -a "System Settings"
```

人工进入“通知 → LazyMind”：允许通知并选择横幅/提醒；先关闭专注模式。首次通知权限弹窗点允许。此项无法用安全通用的终端命令代替点击，不使用 `osascript display notification` 冒充 LazyMind 通知。

## 4. 创建任务、验证前台通知及点击

```sh
m setup
export SID="$(jq -r '.schedule_id' "$LAZYMIND_ACCEPTANCE_ROOT/schedule.json")"
nreq GET "/api/core/schedules/$SID/notifications" 200 rule-original.json
jq '{configured,revision,config,availability}' "$LAZYMIND_ACCEPTANCE_ROOT/rule-original.json"
m run foreground
```

setup 创建“后台续期人工验收”任务并关闭自动排程，后续 run 每个新 label 只执行一次。若系统的定时任务功能总开关关闭，先在测试实例的任务设置中开启。预期桌面 available；等待模型完成后：

```sh
m task foreground
export FOREGROUND_TASK="$(jq -r '.task_id' "$LAZYMIND_ACCEPTANCE_ROOT/run-foreground.json")"
nreq GET "/api/core/task-center/tasks/$FOREGROUND_TASK/notifications" 200 foreground-notices.json
jq '{snapshot,items}' "$LAZYMIND_ACCEPTANCE_ROOT/foreground-notices.json"
screencapture -i "$LAZYMIND_ACCEPTANCE_ROOT/foreground.png"
```

预期任务 succeeded，恰好一条 desktop/succeeded，正文含“后台通知验收成功”，系统显示 LazyMind 标识、任务标题及摘要。Core 的 sent 表示收到系统 show 回调，不等于人已看见横幅，分别记录。点击系统通知应打开该 task 的 conversation_id，不是首页、空页面或其他用户会话。

```sh
m task foreground
```

重复查询/点击不能生成第二条事件或重跑任务。长正文应最多 200 个字符；不要把空摘要或只有“任务已完成”算通过。

## 5. 配置保存、版本冲突、无效输入与默认值

先关闭该任务的桌面渠道，保留原配置。使用同一个 revision 再提交一次，必须冲突：

```sh
nreq GET "/api/core/schedules/$SID/notifications" 200 rule-current.json
jq '{revision,config:(.config | .channels.desktop.enabled=false)}' \
  "$LAZYMIND_ACCEPTANCE_ROOT/rule-current.json" > "$LAZYMIND_ACCEPTANCE_ROOT/rule-off-request.json"
nreq PUT "/api/core/schedules/$SID/notifications" 200 rule-off.json rule-off-request.json
nreq PUT "/api/core/schedules/$SID/notifications" 409 rule-stale.json rule-off-request.json
jq . "$LAZYMIND_ACCEPTANCE_ROOT/rule-stale.json"
m run muted
```

等 muted 完成：

```sh
m task muted
```

预期任务仍 succeeded，未产生桌面通知；单任务关闭全部渠道合法。此后恢复最初配置：

```sh
nreq GET "/api/core/schedules/$SID/notifications" 200 rule-current.json
jq --slurpfile original "$LAZYMIND_ACCEPTANCE_ROOT/rule-original.json" \
  '{revision,config:$original[0].config}' "$LAZYMIND_ACCEPTANCE_ROOT/rule-current.json" \
  > "$LAZYMIND_ACCEPTANCE_ROOT/rule-restore-request.json"
nreq PUT "/api/core/schedules/$SID/notifications" 200 rule-restored.json rule-restore-request.json
nreq GET /api/core/user/notification-preferences 200 prefs-current.json
jq '{revision,defaults:(.defaults | .events |= with_entries(.value.enabled=false))}' \
  "$LAZYMIND_ACCEPTANCE_ROOT/prefs-current.json" > "$LAZYMIND_ACCEPTANCE_ROOT/no-events.json"
nreq PATCH /api/core/user/notification-preferences 422 no-events-error.json no-events.json
jq . "$LAZYMIND_ACCEPTANCE_ROOT/no-events-error.json"
```

预期 `NOTIFICATION_EVENT_REQUIRED`；失败请求没有部分保存。再把用户默认桌面设为关闭，检查旧任务不被覆盖：

```sh
jq '{revision,defaults:(.defaults | .channels.desktop.enabled=false)}' \
  "$LAZYMIND_ACCEPTANCE_ROOT/prefs-current.json" > "$LAZYMIND_ACCEPTANCE_ROOT/default-off.json"
nreq PATCH /api/core/user/notification-preferences 200 prefs-off.json default-off.json
nreq GET "/api/core/schedules/$SID/notifications" 200 rule-before-reset.json
jq '.config.channels.desktop' "$LAZYMIND_ACCEPTANCE_ROOT/rule-before-reset.json"
```

旧任务应仍为 true。再创建一条新任务，确认复制已变更的默认值，而不是硬编码桌面开启：

```sh
jq -n '{name:"默认值继承验收",cron_expr:"0 0 1 1 *",timezone:"Asia/Shanghai",prompt_template:"只回复：默认值继承验收。",kb_ids:[],file_ids:[]}' \
  > "$LAZYMIND_ACCEPTANCE_ROOT/inherit-request.json"
nreq POST /api/core/schedules 200 inherit-created.json inherit-request.json
export INHERIT_SID="$(jq -r '.id' "$LAZYMIND_ACCEPTANCE_ROOT/inherit-created.json")"
nreq POST "/api/core/schedules/$INHERIT_SID:cancel" 200 inherit-disabled.json
nreq GET "/api/core/schedules/$INHERIT_SID/notifications" 200 inherit-rule.json
jq '{configured,config}' "$LAZYMIND_ACCEPTANCE_ROOT/inherit-rule.json"
```

预期新任务 configured=true、desktop.enabled=false。最后显式 reset 原任务：

```sh
jq '{revision}' "$LAZYMIND_ACCEPTANCE_ROOT/rule-before-reset.json" > "$LAZYMIND_ACCEPTANCE_ROOT/reset-request.json"
nreq POST "/api/core/schedules/$SID/notifications:reset" 200 rule-reset.json reset-request.json
jq '.config.channels.desktop' "$LAZYMIND_ACCEPTANCE_ROOT/rule-reset.json"
```

预期 reset 前仍 true，显式 reset 后 false。恢复默认值，再 reset 该任务：

```sh
nreq GET /api/core/user/notification-preferences 200 prefs-current.json
jq --slurpfile original "$LAZYMIND_ACCEPTANCE_ROOT/prefs-original.json" \
  '{revision,defaults:$original[0].defaults}' "$LAZYMIND_ACCEPTANCE_ROOT/prefs-current.json" \
  > "$LAZYMIND_ACCEPTANCE_ROOT/default-restore.json"
nreq PATCH /api/core/user/notification-preferences 200 prefs-restored.json default-restore.json
nreq GET "/api/core/schedules/$SID/notifications" 200 rule-current.json
jq '{revision}' "$LAZYMIND_ACCEPTANCE_ROOT/rule-current.json" > "$LAZYMIND_ACCEPTANCE_ROOT/reset-request.json"
nreq POST "/api/core/schedules/$SID/notifications:reset" 200 rule-restored.json reset-request.json
```

## 6. 运行快照不被后续配置覆盖

```sh
m run snapshot
export SNAPSHOT_TASK="$(jq -r '.task_id' "$LAZYMIND_ACCEPTANCE_ROOT/run-snapshot.json")"
nreq GET "/api/core/task-center/tasks/$SNAPSHOT_TASK/notifications" 200 snapshot-before.json
nreq GET "/api/core/schedules/$SID/notifications" 200 rule-current.json
jq '{revision,config:(.config | .channels.desktop.enabled=false)}' \
  "$LAZYMIND_ACCEPTANCE_ROOT/rule-current.json" > "$LAZYMIND_ACCEPTANCE_ROOT/rule-off-request.json"
nreq PUT "/api/core/schedules/$SID/notifications" 200 rule-off.json rule-off-request.json
nreq GET "/api/core/task-center/tasks/$SNAPSHOT_TASK/notifications" 200 snapshot-after.json
diff -u <(jq -S '.snapshot' "$LAZYMIND_ACCEPTANCE_ROOT/snapshot-before.json") \
  <(jq -S '.snapshot' "$LAZYMIND_ACCEPTANCE_ROOT/snapshot-after.json")
m task snapshot
m run snapshot_next
```

预期 diff 无输出，第一次仍按创建时快照通知，第二次无桌面通知。第一次若执行过快，仍证明快照不可变，但运行中修改竞态记为未覆盖。两任务完成后：

```sh
m task snapshot_next
nreq GET "/api/core/schedules/$SID/notifications" 200 rule-current.json
jq '{revision}' "$LAZYMIND_ACCEPTANCE_ROOT/rule-current.json" > "$LAZYMIND_ACCEPTANCE_ROOT/reset-request.json"
nreq POST "/api/core/schedules/$SID/notifications:reset" 200 rule-restored.json reset-request.json
```

## 7. 通知总开关、运行中确认与不补发

确认前面所有任务结束。先验收无活动运行时关闭：

```sh
nreq GET /api/core/user/notification-preferences 200 prefs-current.json
jq '{revision,enabled:false}' "$LAZYMIND_ACCEPTANCE_ROOT/prefs-current.json" > "$LAZYMIND_ACCEPTANCE_ROOT/global-off.json"
nreq PATCH /api/core/user/notification-preferences 200 prefs-disabled.json global-off.json
m run global_off
```

等任务结束后查询，再开总开关：

```sh
m task global_off
nreq GET /api/core/user/notification-preferences 200 prefs-current.json
jq '{revision,enabled:true}' "$LAZYMIND_ACCEPTANCE_ROOT/prefs-current.json" > "$LAZYMIND_ACCEPTANCE_ROOT/global-on.json"
nreq PATCH /api/core/user/notification-preferences 200 prefs-enabled.json global-on.json
sleep 10
m task global_off
m run global_on
```

预期 global_off 正常完成但记录 skipped，不因重新开启补发；global_on 正常通知。完成后测运行中关闭：

```sh
m run switch_running
m task switch_running
nreq GET /api/core/user/notification-preferences 200 prefs-current.json
jq '{revision,enabled:false}' "$LAZYMIND_ACCEPTANCE_ROOT/prefs-current.json" > "$LAZYMIND_ACCEPTANCE_ROOT/global-off.json"
nreq PATCH /api/core/user/notification-preferences 200,409 switch-confirmation.json global-off.json
jq . "$LAZYMIND_ACCEPTANCE_ROOT/switch-confirmation.json"
```

只有请求时确有活动运行，才应返回 409 `NOTIFICATION_CONFIRMATION_REQUIRED`。若已完成而返回 200，不算确认场景通过；开回总开关，使用新 label 重做。收到 409 后，人工核对 `.detail.running_task_ids` 全是本次允许抑制通知的测试任务，再执行：

```sh
jq -s '.[0] + {confirm_running_task_ids:.[1].detail.running_task_ids}' \
  "$LAZYMIND_ACCEPTANCE_ROOT/global-off.json" "$LAZYMIND_ACCEPTANCE_ROOT/switch-confirmation.json" \
  > "$LAZYMIND_ACCEPTANCE_ROOT/global-confirmed.json"
nreq PATCH /api/core/user/notification-preferences 200,409 switch-confirmed-result.json global-confirmed.json
m task switch_running
```

集合变化再次 409 是预期，必须重新读取确认，不能盲重试。总开关不会取消业务任务；已经取得发送许可的请求可能无法撤回。结束后按本节 global-on 命令读取新 revision 并恢复 true。

## 8. 真正的定时触发，而非只测 run-now

```sh
jq -n '{name:"分钟触发验收",cron_expr:"* * * * *",timezone:"Asia/Shanghai",prompt_template:"不要调用工具。只回复：分钟触发验收成功。",kb_ids:[],file_ids:[]}' \
  > "$LAZYMIND_ACCEPTANCE_ROOT/cron-request.json"
nreq POST /api/core/schedules 200 cron-created.json cron-request.json
export CRON_SID="$(jq -r '.id' "$LAZYMIND_ACCEPTANCE_ROOT/cron-created.json")"
sleep 75
nreq POST "/api/core/schedules/$CRON_SID:cancel" 200 cron-disabled.json
nreq GET "/api/core/task-center/schedules/$CRON_SID/tasks" 200 cron-tasks.json
jq . "$LAZYMIND_ACCEPTANCE_ROOT/cron-tasks.json"
```

预期至少出现一次真实定时执行并有实际结果/通知；因分钟边界可能触发两次，应按两个不同 task_id 分别核对，不能误判为重复通知。cancel 只关闭后续调度，不取消已经运行的任务。用返回的实际任务 ID 查看事件：

```sh
read -r 'CRON_TASK?输入上一步实际定时执行的 task_id: '
nreq GET "/api/core/task-center/tasks/$CRON_TASK/notifications" 200 cron-notices.json
jq . "$LAZYMIND_ACCEPTANCE_ROOT/cron-notices.json"
```

## 9. 关窗、续期、点击重开与冷启动

下面步骤先保持只有桌面通知，模型任务全部完成。记录基线后人工点击窗口红色关闭按钮（不是最小化）：

```sh
m baseline background
# 此处人工关窗，然后继续。
ps -p "$LAZYMIND_ACCEPTANCE_PID" -o pid=,comm=
pgrep -P "$LAZYMIND_ACCEPTANCE_PID" -fl 'Helper.*Renderer'
m watch background --seconds 240
grep 'session renew' "$LAZYMIND_ACCEPTANCE_ROOT/session-actions.log"
m me
m run background
```

Renderer 查询应无输出；主进程存在。watch 应同用户、同 origin、access/refresh 均变化、过期时间推进且仍有效；还必须有关窗后的 renew 调用，才能证明独立续期。待收到通知：

```sh
m task background
```

人工点击新通知，预期重开原用户会话、进入正确任务，不跳登录或重复弹出。再关窗重复一轮：

```sh
m baseline second
# 人工关窗。
m watch second --seconds 240
m run second
# 任务完成后点击通知重开窗口。
m task second
m me
```

冷启动交接：

```sh
m baseline cold_before
# 人工关窗；确认没有运行中任务。
m watch cold_before --seconds 240
m baseline cold_saved
ps -p "$LAZYMIND_ACCEPTANCE_PID" -o pid=,comm=
kill -TERM "$LAZYMIND_ACCEPTANCE_PID"
sleep 15
ps -p "$LAZYMIND_ACCEPTANCE_PID" -o pid=,comm=
```

确认旧 PID 已结束，再使用本手册第 3 步的启动命令重新启动、重新记录 PID，保持所有环境变量和 profile 不变：

```sh
m compare cold_saved
m me
m run cold
# 等完成后：
m task cold
```

预期仍为原用户，通知正常。启动跨过令牌过期或 origin 改变时，单凭 token 差异不能证明冷启动交接，记录时序并重测。本项不等于验证系统历史通知在进程退出后的冷启动激活。

## 10. macOS 权限、勿扰、通知中心与同用户刷新

```sh
open -a "System Settings"
```

人工打开专注模式，保持通知权限允许：

```sh
m run focus
# 等完成后：
m task focus
screencapture -i "$LAZYMIND_ACCEPTANCE_ROOT/focus.png"
```

预期遵守系统设置，不以 critical 优先级绕过。无横幅不代表任务失败；检查通知中心和记录状态。再关闭专注模式、关闭 LazyMind 通知权限：

```sh
m run permission_off
# 等完成后：
m task permission_off
```

预期不强制展示、不循环弹权限提示。没有可靠系统回调时不应凭猜测写 delivered/permission_denied；macOS 可能仍给 show 回调，应同时记录系统实际行为，不能只由 sent 判断权限。

人工恢复通知权限；以新任务验证恢复，不要求先前未知结果自动补弹：

```sh
m run permission_on
# 等完成后：
m task permission_on
m baseline foreground_refresh
# 保持窗口打开，并暂不点击最近一条系统通知。
m watch foreground_refresh --seconds 240
```

同用户刷新前后，通知不应被程序主动清除或重新弹一次；刷新后点击仍进入原任务。前台没有在此观察窗口内刷新则记为未覆盖，不能冒充独立续期成功。

## 11. 微信与飞书接入、上下文及接收对象

外部步骤会产生真实消息，由验收人在确认账号/对象后执行。先在应用既有“应用接入”页面分别连接微信、飞书，用手机完成扫码/确认；使用专用测试会话发一条“通知验收，请回复收到”，等待原聊天通路正常回复。

```sh
export GW=/api/channel-gateway/v1
nreq GET "$GW/channel-accounts?provider=wechat" 200 wechat-accounts.json
nreq GET "$GW/channel-accounts?provider=feishu" 200 feishu-accounts.json
jq '.items[] | {id,provider,label,status,runtime_status}' "$LAZYMIND_ACCEPTANCE_ROOT/wechat-accounts.json"
jq '.items[] | {id,provider,label,status,runtime_status}' "$LAZYMIND_ACCEPTANCE_ROOT/feishu-accounts.json"
read -r 'WECHAT_ACCOUNT?输入本次微信测试账号 id: '
read -r 'FEISHU_ACCOUNT?输入本次飞书测试账号 id: '
nreq GET "$GW/channel-accounts/$WECHAT_ACCOUNT/notification-targets" 200 wechat-targets.json
nreq GET "$GW/channel-accounts/$FEISHU_ACCOUNT/notification-targets" 200 feishu-targets.json
jq . "$LAZYMIND_ACCEPTANCE_ROOT/wechat-targets.json"
jq . "$LAZYMIND_ACCEPTANCE_ROOT/feishu-targets.json"
read -r 'WECHAT_TARGET?输入明确允许接收消息的微信 recipient_id: '
read -r 'FEISHU_TARGET?输入明确允许接收消息的飞书 recipient_id: '
```

必须是正确账号的已知对象，available=true；微信只扫码不建立有效上下文不能算接入完成。不要默认选第一项。多对象时账号详情 primary_recipient 应为 null：

```sh
nreq GET "$GW/channel-accounts/$WECHAT_ACCOUNT" 200 wechat-detail.json
jq '{id,primary_recipient,capabilities,avatar_url}' "$LAZYMIND_ACCEPTANCE_ROOT/wechat-detail.json"
```

扫码取消/刷新回归：在页面创建新扫码会话，取消后扫旧码不能新增账号；刷新后旧二维码不能覆盖新连接。操作前后重新执行上述账号列表命令比对。不要为了此测试断开业务账号。若当前页面无法渲染二维码，此分支记为待客户端验收，不用终端二维码替身冒充产品 UI。

## 12. 企业微信智能机器人接入

在企业微信管理界面准备测试智能机器人 BotID 和 Secret，并确保没有其他程序占用同一个长连接。以下输入使用 getpass，不进入 shell 历史，也不打印凭据：

```sh
PYTHONPATH="$LAZYMIND_ACCEPTANCE_REPO/docs/plan/task-notification-review-fixes" python3 - <<'PY'
import acceptance as a
from getpass import getpass
bot_id = getpass('测试 BotID（隐藏输入）: ').strip()
secret = getpass('测试 Secret（隐藏输入）: ').strip()
assert bot_id and secret
result = a.api('/api/channel-gateway/v1/connection-sessions', 'POST',
               {'provider':'wecom','credentials':{'bot_id':bot_id,'secret':secret}})
a.save('wecom-session.json', result)
print({'status':result.get('status'), 'account_id':(result.get('account') or {}).get('id'),
       'error_code':(result.get('error') or {}).get('code')})
PY
nreq GET "$GW/channel-accounts?provider=wecom" 200 wecom-accounts.json
jq '.items[] | {id,provider,status,runtime_status}' "$LAZYMIND_ACCEPTANCE_ROOT/wecom-accounts.json"
read -r 'WECOM_ACCOUNT?输入本次企微测试账号 id: '
```

预期 connected、runtime 最终 running；不是二维码模式。手工向机器人发文本并等待回复，之后：

```sh
nreq GET "$GW/channel-accounts/$WECOM_ACCOUNT/notification-targets" 200 wecom-targets.json
jq . "$LAZYMIND_ACCEPTANCE_ROOT/wecom-targets.json"
read -r 'WECOM_TARGET?输入明确允许接收消息的企微 recipient_id: '
```

验证错误 Secret：用上述隐藏输入方式输入专用测试 BotID 和故意错误的 Secret，保存到另一个结果文件；预期 WECOM_AUTH_FAILED，不泄漏 SDK 异常/Secret。HTTP 201 只表示连接会话创建，不能把失败会话当作已连接成功。

## 13. 四渠道真实投递、正文与历史

确保已选择三个明确允许测试的对象，然后为本验收任务绑定：

```sh
nreq GET "/api/core/schedules/$SID/notifications" 200 rule-current.json
jq --arg wa "$WECHAT_ACCOUNT" --arg wt "$WECHAT_TARGET" \
   --arg fa "$FEISHU_ACCOUNT" --arg ft "$FEISHU_TARGET" \
   --arg ca "$WECOM_ACCOUNT" --arg ct "$WECOM_TARGET" \
   '{revision,config:(.config |
     .channels.desktop={enabled:true} |
     .channels.wechat={enabled:true,account_id:$wa,recipient_id:$wt} |
     .channels.feishu={enabled:true,account_id:$fa,recipient_id:$ft} |
     .channels.wecom={enabled:true,account_id:$ca,recipient_id:$ct})}' \
   "$LAZYMIND_ACCEPTANCE_ROOT/rule-current.json" > "$LAZYMIND_ACCEPTANCE_ROOT/all-channels.json"
nreq PUT "/api/core/schedules/$SID/notifications" 200 all-channels-saved.json all-channels.json
jq .availability "$LAZYMIND_ACCEPTANCE_ROOT/all-channels-saved.json"
m run channels
```

任务完成后：

```sh
m task channels
export CHANNEL_TASK="$(jq -r '.task_id' "$LAZYMIND_ACCEPTANCE_ROOT/run-channels.json")"
nreq GET "$GW/task-notifications?task_id=$CHANNEL_TASK&limit=100" 200 channel-history.json
jq '.items[] | {notification_id,status,reason,attempt_count,retry_of,payload}' "$LAZYMIND_ACCEPTANCE_ROOT/channel-history.json"
```

逐个平台肉眼核对只发到指定对象、内容含实际结果、任务标题/状态/时间正确；Gateway 最终 sent。Core queued 只表示交接，不能算平台投递成功。不得泄漏推理或工具内部输出。

全文分段：使用测试实例任务编辑界面，把该验收任务提示改为生成约 3000 字的无敏感测试报告（不调用工具），并保留禁用自动排程。然后：

```sh
nreq GET "/api/core/schedules/$SID/notifications" 200 rule-current.json
jq '{revision,config:(.config | .events.succeeded.content="full")}' \
  "$LAZYMIND_ACCEPTANCE_ROOT/rule-current.json" > "$LAZYMIND_ACCEPTANCE_ROOT/full-request.json"
nreq PUT "/api/core/schedules/$SID/notifications" 200 full-saved.json full-request.json
m run full
# 等真实任务完成后：
m task full
export FULL_TASK="$(jq -r '.task_id' "$LAZYMIND_ACCEPTANCE_ROOT/run-full.json")"
nreq GET "$GW/task-notifications?task_id=$FULL_TASK&limit=100" 200 full-history.json
jq '.items[] | {status,reason,payload}' "$LAZYMIND_ACCEPTANCE_ROOT/full-history.json"
```

验收各平台分段连续、不重复、不把未发全文说成完整；桌面仍限 200 字符。模型若没有生成足够长正文，分段项未覆盖。若任务有真实附件，企微应提示在任务查看，不声称附件已发送。完成后在页面恢复原短提示，并把规则 summary 恢复；后续桌面/故障测试先 reset 回桌面默认，避免意外外发。

## 14. 账号引用、断开、同身份重连、多账号隔离

以下仅对专用企微测试账号操作，先确认全部发送完成：

```sh
nreq GET "$GW/channel-accounts/$WECOM_ACCOUNT/notification-references" 200 refs-before.json
jq . "$LAZYMIND_ACCEPTANCE_ROOT/refs-before.json"
nreq DELETE "$GW/channel-accounts/$WECOM_ACCOUNT" 204 disconnected.json
nreq GET "/api/core/schedules/$SID/notifications" 200 rule-disconnected.json
nreq GET "$GW/task-notifications?task_id=$CHANNEL_TASK&limit=100" 200 history-after-disconnect.json
jq '{config,availability}' "$LAZYMIND_ACCEPTANCE_ROOT/rule-disconnected.json"
```

预期账号引用/规则和历史保留，企微 unavailable；不能把不可达当可用。不要只查 HTTP 200，读取 availability。要测断开后的运行，可在仍保存原绑定时手动 run 一个新 label；其余已接渠道应正常，断开渠道不能成功投递。

同身份重连，使用原 BotID/Secret，并传原 account_id：

```sh
export WECOM_ACCOUNT
PYTHONPATH="$LAZYMIND_ACCEPTANCE_REPO/docs/plan/task-notification-review-fixes" python3 - <<'PY'
import os
import acceptance as a
from getpass import getpass
result = a.api('/api/channel-gateway/v1/connection-sessions', 'POST',
    {'provider':'wecom','account_id':os.environ['WECOM_ACCOUNT'],
     'credentials':{'bot_id':getpass('原 BotID: ').strip(),'secret':getpass('原 Secret: ').strip()}})
a.save('wecom-reconnected.json',result)
print({'status':result.get('status'),'account_id':(result.get('account') or {}).get('id')})
PY
nreq GET "$GW/channel-accounts/$WECOM_ACCOUNT/notification-targets" 200 reconnected-targets.json
```

应保留原 account_id；再次从测试会话发送文本建立上下文后检查可用性。用另一专用 BotID 重连此 ID 应 HTTP 409，不能替换身份；没有第二机器人则记未测。多个账号时分别绑定不同任务、不同收件对象并执行，不得串号。微信断开重连还需重新建立会话上下文，不能沿用已清除的旧上下文。

## 15. 失败、unknown 与人工重试链

只有出现真实 failed/unknown 才执行本节。可以在专用平台管理台暂时撤销测试机器人访问或制造真实平台不可用，之后恢复；必须记录实际状态，不能把超时一律当 failed，也不要改数据库伪造平台发送成功。自动重试可能持续数分钟。

```sh
read -r 'RETRY_TASK?输入存在真实投递失败的测试 task_id: '
nreq GET "$GW/task-notifications?task_id=$RETRY_TASK&limit=100" 200 retry-history.json
jq '.items[] | {notification_id,status,reason,retry_of,attempt_count}' "$LAZYMIND_ACCEPTANCE_ROOT/retry-history.json"
read -r 'NOTICE_ID?输入要重试的 Gateway notification_id（不是 Core 事件 ID）: '
export RETRY_KEY="$(uuidgen)"
jq -n --arg key "$RETRY_KEY" '{idempotency_key:$key}' > "$LAZYMIND_ACCEPTANCE_ROOT/retry-request.json"
```

真实 failed 且整条链没有活动/成功/unknown 分支时：

```sh
nreq POST "$GW/task-notifications/$NOTICE_ID:retry" 201 retry-created.json retry-request.json
nreq POST "$GW/task-notifications/$NOTICE_ID:retry" 201 retry-replayed.json retry-request.json
diff -u <(jq -r '.notification_id' "$LAZYMIND_ACCEPTANCE_ROOT/retry-created.json") \
  <(jq -r '.notification_id' "$LAZYMIND_ACCEPTANCE_ROOT/retry-replayed.json")
```

预期同 key 返回同 ID；重试不创建新 task_id、不重跑模型。若是 unknown，应先用同一未确认请求验收 409：

```sh
nreq POST "$GW/task-notifications/$NOTICE_ID:retry" 409 retry-needs-confirmation.json retry-request.json
jq . "$LAZYMIND_ACCEPTANCE_ROOT/retry-needs-confirmation.json"
# 人工确认可能产生重复消息后，才执行下面两条。
jq '.confirm_duplicate_risk=true' "$LAZYMIND_ACCEPTANCE_ROOT/retry-request.json" > "$LAZYMIND_ACCEPTANCE_ROOT/retry-confirmed.json"
nreq POST "$GW/task-notifications/$NOTICE_ID:retry" 201 retry-created.json retry-confirmed.json
```

上面 failed 与 unknown 是两条分支，不要对同一已成功重试机械连续执行。祖先绕过回归：当链中已有 queued/sending/sent 子尝试，对失败祖先使用新的 key，应 409 NOTIFICATION_STATE_CHANGED：

```sh
jq -n --arg key "$(uuidgen)" '{idempotency_key:$key}' > "$LAZYMIND_ACCEPTANCE_ROOT/retry-new-key.json"
nreq POST "$GW/task-notifications/$NOTICE_ID:retry" 409 retry-chain-blocked.json retry-new-key.json
nreq GET "$GW/task-notifications?task_id=$RETRY_TASK&limit=100" 200 retry-history-after.json
jq '.items[] | {notification_id,status,retry_of}' "$LAZYMIND_ACCEPTANCE_ROOT/retry-history-after.json"
```

若子尝试又已失败，409 前提不成立，应重新安排场景。不能稳定制造 unknown、并发或分段响应丢失时，人工项记未覆盖，并执行第 20 步精确故障回归，不宣称真平台故障已验证。

## 16. 查询边界、未认证和跨用户隔离

```sh
nreq GET '/api/core/task-center/desktop-notifications?device_id=other' 403 wrong-device.json
nreq GET '/api/core/task-center/desktop-notifications?limit=101' 422 invalid-page.json
nreq GET '/api/core/user/notification-preferences' 401,403 anonymous.json --anonymous
printf '%s' '{"revision":1,"revision":2,"enabled":false}' > "$LAZYMIND_ACCEPTANCE_ROOT/duplicate-fields.json"
nreq PATCH /api/core/user/notification-preferences 422 duplicate-error.json duplicate-fields.json
```

预期请求被拒绝；不得返回 SQL、堆栈、凭据或依赖原始错误。随后人工退出用户 A，登录专用用户 B：

```sh
m me
nreq GET "/api/core/schedules/$SID/notifications" 404 other-user-rule.json
nreq GET "/api/core/task-center/tasks/$FOREGROUND_TASK/notifications" 404 other-user-task.json
nreq GET "$GW/channel-accounts/$WECOM_ACCOUNT" 404 other-user-account.json
```

没有执行外部渠道则跳过最后一条。B 不能读取 A 正文或接管 A 账号；点击 A 留存的系统通知不能进入 A 的结果。不要伪造 X-User-Id 在内部端口调用来代替真实用户隔离测试。完成后登录回 A。

## 17. 业务失败、人工等待、原聊天和工作区回归

先 reset 验收任务回桌面默认，确认不再外发：

```sh
nreq GET "/api/core/schedules/$SID/notifications" 200 rule-current.json
jq '{revision}' "$LAZYMIND_ACCEPTANCE_ROOT/rule-current.json" > "$LAZYMIND_ACCEPTANCE_ROOT/reset-request.json"
nreq POST "/api/core/schedules/$SID/notifications:reset" 200 rule-restored.json reset-request.json
```

业务失败：在隔离实例的模型配置中临时选一个确定不可用的测试模型端点，关闭自动 fallback（如有），执行：

```sh
m run business_failure
# 等执行进入真实 failed 后：
m task business_failure
```

预期任务 failed、有 failed 通知、安全失败原因；如果模型 fallback 成功，不能计作失败场景覆盖。立即恢复有效模型，然后 `m run after_model_restore`，待完成 `m task after_model_restore`，确认成功路径未受影响。

人工等待：

```sh
nreq GET "/api/core/schedules/$SID/notifications" 200 rule-current.json
jq '{revision,config:(.config | .events.waiting.enabled=true)}' \
  "$LAZYMIND_ACCEPTANCE_ROOT/rule-current.json" > "$LAZYMIND_ACCEPTANCE_ROOT/waiting-request.json"
nreq PUT "/api/core/schedules/$SID/notifications" 200 waiting-saved.json waiting-request.json
mkdir -p "$LAZYMIND_ACCEPTANCE_ROOT/workspace"
printf '人工验收原始内容\n' > "$LAZYMIND_ACCEPTANCE_ROOT/workspace/approval.txt"
open "$LAZYMIND_ACCEPTANCE_ROOT/workspace"
```

在应用中只授权该测试目录，用现有“覆盖文件需要审批”的工作区任务流程触发人工审批；不要选择业务目录。若当前模型/能力无法稳定进入审批，记未测。记录任务实际 ID：

```sh
read -r 'WAIT_TASK?输入实际进入人工审批的定时 task_id: '
nreq GET "/api/core/task-center/tasks/$WAIT_TASK/notifications" 200 waiting-notices.json
jq . "$LAZYMIND_ACCEPTANCE_ROOT/waiting-notices.json"
```

人工等待应通知，单纯等待依赖不应产生人工等待通知；审批后继续执行，不能因通知回调重复执行文件写入。重复审批与数据库故障用第 20 步补充验证。

工作区重新授权：在应用工作区界面对本测试目录执行重新授权，选同一目录应成功；选另一个目录应拒绝；取消不得新增授权。创建可控目录及符号链接：

```sh
mkdir -p "$LAZYMIND_ACCEPTANCE_ROOT/workspace-other"
ln -s "$LAZYMIND_ACCEPTANCE_ROOT/workspace" "$LAZYMIND_ACCEPTANCE_ROOT/workspace-link"
open "$LAZYMIND_ACCEPTANCE_ROOT"
```

同目录链接解析后应可授权，其他目录不得替换原绑定。最后在普通聊天发送消息、上传一个无敏感测试文件，并分别从原飞书/微信会话触发一个后台聊天任务；通知全局开关关闭时原聊天提醒应保持原行为。关闭/恢复总开关复用第 7 步命令，不要与仍运行的定时任务混在一起判断。

## 18. 续期临时故障、撤销及退出（最后执行）

恢复短任务提示、桌面默认规则，确认无任务运行。先记录基线、模拟 connector 临时不可用，然后人工关窗：

```sh
m baseline outage
touch "$LAZYMIND_ACCEPTANCE_ROOT/simulate-renewal-outage"
# 人工关闭窗口。
sleep 150
m compare outage
grep 'session renew' "$LAZYMIND_ACCEPTANCE_ROOT/session-actions.log" | tail -10
rm "$LAZYMIND_ACCEPTANCE_ROOT/simulate-renewal-outage"
m watch outage --seconds 180
m run recovered
```

预期临时失败逐渐退避，恢复后原用户续期并通知。此处是包装器边界模拟，不是实际认证服务断网；关闭 Wi-Fi 不能模拟本地回环服务失联。

真实 refresh 撤销：等 recovered 完成、关窗后：

```sh
m baseline revoked
m watch revoked --seconds 240
m baseline revoked_latest
m revoke
sleep 150
m compare revoked_latest
grep 'session renew' "$LAZYMIND_ACCEPTANCE_ROOT/session-actions.log" | tail -10
sleep 65
grep 'session renew' "$LAZYMIND_ACCEPTANCE_ROOT/session-actions.log" | tail -10
```

预期明确拒绝后停止，不能自动切换管理员；此结论只观察重新打开窗口前，因为页面有原有的本地自动登录行为。再打开应用、登录回 A，并通过账号菜单明确退出登录：

```sh
test ! -f "$LAZYMIND_HOME/credentials.json" && echo SESSION_CLEARED
tail -10 "$LAZYMIND_ACCEPTANCE_ROOT/session-actions.log"
```

预期 clear；如果页面立即自动 set 新会话，需要记录，不能说旧后台会话自行复活，也不能声称退出细项通过。

## 19. 老任务兼容与验收收尾

旧未配置任务只能使用真实升级前测试数据验收。新建任务或把数据库手动改成 NULL 不能证明升级正确。若隔离测试数据中存在旧任务：

```sh
read -r 'LEGACY_SID?输入真实升级前未配置通知的测试 schedule_id: '
nreq GET "/api/core/schedules/$LEGACY_SID/notifications" 200 legacy-rule.json
jq '{configured,config,revision}' "$LAZYMIND_ACCEPTANCE_ROOT/legacy-rule.json"
```

预期 false/null/0，业务执行仍正常但不自动补通知；没有旧测试数据记未测。应在第 18 步撤销/退出前执行此项，或正常重新登录后执行，不为验收绕过认证。

收尾不删库、不 reset、不批量断开账号。若仍为用户 A 且能调用 API：

```sh
m cancel
if [ -n "${CRON_SID:-}" ]; then
  nreq POST "/api/core/schedules/$CRON_SID:cancel" 200 cron-final-disabled.json
fi
```

若已退出导致 401，任务早已在创建时或第 8 步关闭自动排程，保留隔离目录即可；不要为了清理自动获取管理员凭证。确认所有任务完成后，仅结束本次已核对的 PID：

```sh
ps -p "$LAZYMIND_ACCEPTANCE_PID" -o pid=,comm=
kill -TERM "$LAZYMIND_ACCEPTANCE_PID"
sleep 15
ps -p "$LAZYMIND_ACCEPTANCE_PID" -o pid=,comm=
unset LAZYMIND_JWT_TTL_MINUTES LAZYMIND_HOME LAZYMIND_DESKTOP_RUNTIME_ROOT
unset LAZYMIND_DESKTOP_AGENT_CONNECTOR LAZYMIND_ACCEPTANCE_BIN
```

最后 ps 应无输出；仍有进程/端口则保留现场排查。正常启动原应用恢复原环境。本次 2 分钟 TTL 没有修改源码默认值。

## 20. 不能靠手工时序稳定覆盖的审查回归

本节是人工执行的自动回归补充，不冒充真实平台人工验收。无需启动业务应用；测试使用独立临时数据库。禁止把 TEST_DB_DSN 指向业务库。先检查当前修复的桌面与 CLI：

```sh
cd "$LAZYMIND_ACCEPTANCE_REPO"
node --test desktop/scripts/*.test.mjs 2>&1 | tee "$LAZYMIND_ACCEPTANCE_ROOT/desktop-tests.log"
(cd local/lazymind-cli && go test -race ./...) 2>&1 | tee "$LAZYMIND_ACCEPTANCE_ROOT/cli-tests.log"
(cd backend/core && TEST_DB_DRIVER=sqlite go test -race ./chat ./taskcenter \
  -run '^TestNotificationReview' -count=1 -timeout=180s) \
  2>&1 | tee "$LAZYMIND_ACCEPTANCE_ROOT/core-review-sqlite.log"
uv venv --python 3.11 "$LAZYMIND_ACCEPTANCE_ROOT/test-venv"
uv pip install --python "$LAZYMIND_ACCEPTANCE_ROOT/test-venv/bin/python" \
  -r tests/backend/channel-gateway/requirements-test.txt
CHANNEL_GATEWAY_TEST_DRIVER=sqlite "$LAZYMIND_ACCEPTANCE_ROOT/test-venv/bin/python" -m pytest \
  tests/backend/channel-gateway/test_notification_retry_chain.py -v --tb=short \
  2>&1 | tee "$LAZYMIND_ACCEPTANCE_ROOT/retry-chain-sqlite.log"
```

重点对应通知写入失败不污染业务结果、旧未配置任务异步收尾、终态竞态、恢复扫描公平性和祖先重试绕过。按审查修复记录，桌面基线是 200 项；以实际输出为准，不沿用旧 174 项数字。有失败/跳过必须逐项记录。

本机 Desktop 使用 SQLite，上述不能代表 PostgreSQL/Compose 验收。若需发布两部署模式，还应按 implementation-report.md 的专用 PostgreSQL 环境要求分别运行；本手册不连接或修改未知 PostgreSQL 实例。

## 验收记录

```sh
cat > "$LAZYMIND_ACCEPTANCE_ROOT/result.md" <<'EOF'
| 项目 | 通过/失败/未测 | task_id 或脱敏证据 |
|---|---|---|
| 当前代码构建、签名、新续期 CLI | | |
| 默认配置、保存、版本冲突、reset | | |
| 前台通知、摘要、实际点击 | | |
| 快照、单任务关闭、总开关与确认、不补发 | | |
| 真实分钟调度 | | |
| 无窗口续期、多轮交接、冷启动 | | |
| 勿扰、权限、通知中心、同用户刷新 | | |
| 微信扫码、正确上下文及真实投递 | | |
| 飞书原接入/聊天提醒及真实投递 | | |
| 企业微信凭据、文本会话及主动投递 | | |
| 多账号、多对象、断开/同身份重连 | | |
| 摘要/全文分段、真实附件降级 | | |
| failed/unknown 重试、幂等及祖先阻断 | | |
| 未认证、跨用户、设备/输入边界 | | |
| 业务失败、人工等待、工作区及普通聊天回归 | | |
| 续期临时故障、撤销、明确退出 | | |
| 升级前旧任务兼容 | | |
| 审查故障回归与退出清理 | | |
EOF
open -e "$LAZYMIND_ACCEPTANCE_ROOT/result.md"
```

反馈时只提供此表、步骤编号和脱敏的相关输出。保留“未测”项，不将模拟平台、单元测试或系统 show 回调当成真实用户收到消息的证明。

手册自身检查：命令块已通过 zsh 语法检查；manual-api.py 已用独立本地 HTTP Fixture 验证成功、预期错误、204、匿名请求、意外状态和禁止重定向，不打印令牌、不自动刷新/重试，输出文件权限 0600。这不代表以上真实应用、平台或人工步骤已经执行。
