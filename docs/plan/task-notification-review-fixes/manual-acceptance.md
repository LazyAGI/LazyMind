# macOS Apple Silicon 人工验收：通知与独立后台续期

适用当前机器 arm64 和当前工作区源码。旧 DMG 不含本次修改，必须先构建。此文只说明操作，不表示已替你执行构建、启动或真机验收。

主线约 20–30 分钟，不含构建和首次模型配置。每一步通过后再往下；出现失败保留步骤编号和脱敏输出。所有命令在同一个终端执行。不要把 credentials.json、原始 Token 或完整启动日志发到聊天。

## 1. 确认环境，停止旧实例

```sh
cd /Users/zouyu/Downloads/LazyMind-main
uname -m
go version
node --version
pnpm --version
uv --version
pgrep -x LazyMind
```

预期架构 arm64；工具均能输出版本。任何工具不存在时先停在这里，不用旧安装包代替。

若最后一条输出 PID：先确认没有需要保留的运行中任务，在“活动监视器”中找到旧的 LazyMind 主进程并退出；仍常驻时选择“强制退出”。不要批量结束所有 Python/Go 进程。再次执行 `pgrep -x LazyMind` 应无输出。此项目的关闭窗口、Cmd+Q 都可能只是后台常驻，不能据此判断旧进程结束。普通 Local 实例也应先按其原启动方式停止。

## 2. 构建本次源码

```sh
make desktop-darwin-arm64
```

构建可能下载依赖并耗时较长；只在返回成功后继续。该命令重建生成目录，不清理业务数据库。

```sh
test -x desktop/dist/mac-arm64/LazyMind.app/Contents/MacOS/LazyMind && echo APP_OK
test -x desktop/dist/mac-arm64/LazyMind.app/Contents/Resources/runtime/bin/lazymind && echo CLI_OK
codesign --verify --deep --strict desktop/dist/mac-arm64/LazyMind.app
```

预期 APP_OK、CLI_OK，签名检查退出码 0。签名成功不等于功能已通过。

## 3. 建立独立验收目录，缩短令牌寿命

```sh
export LAZYMIND_ACCEPTANCE_ROOT="$HOME/Downloads/LazyMind-notification-acceptance-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$LAZYMIND_ACCEPTANCE_ROOT"
chmod 700 "$LAZYMIND_ACCEPTANCE_ROOT"
export LAZYMIND_HOME="$LAZYMIND_ACCEPTANCE_ROOT/credentials"
export LAZYMIND_DESKTOP_RUNTIME_ROOT="$LAZYMIND_ACCEPTANCE_ROOT/runtime"
export LAZYMIND_JWT_TTL_MINUTES=2
export LAZYMIND_ACCEPTANCE_APP="$PWD/desktop/dist/mac-arm64/LazyMind.app"
export LAZYMIND_ACCEPTANCE_BIN="$LAZYMIND_ACCEPTANCE_APP/Contents/Resources/runtime/bin/lazymind"
export LAZYMIND_DESKTOP_AGENT_CONNECTOR="$PWD/docs/plan/task-notification-review-fixes/acceptance-cli.sh"
export LAZYMIND_ACCEPTANCE_HELPER="$PWD/docs/plan/task-notification-review-fixes/acceptance.py"
chmod +x "$LAZYMIND_DESKTOP_AGENT_CONNECTOR"
printf '%s' '{' | "$LAZYMIND_ACCEPTANCE_BIN" internal session renew
```

最后一条故意传入不完整 JSON。预期 `{"ok":false,"code":"DESKTOP_SESSION_INVALID"}`（字段顺序可不同），证明新续期命令已打包；不会发起认证请求。

运行数据、凭证、页面配置分别放入本次验收目录的 runtime、credentials、profile。macOS 的应用/服务日志仍可能写入默认 `~/Library/Logs/LazyMind`，不能认为日志目录也全部隔离。

`--user-data-dir` 的隔离参数已按 [Electron 31.7.7 源码](https://github.com/electron/electron/blob/v31.7.7/shell/app/electron_main_delegate.cc#L299-L308) 核对；本步骤面向 macOS，不能照搬到会主动覆盖 profile 路径的 Windows 版本。

## 4. 启动验收应用并完成首次配置

```sh
"$LAZYMIND_ACCEPTANCE_APP/Contents/MacOS/LazyMind" \
  --user-data-dir="$LAZYMIND_ACCEPTANCE_ROOT/profile" \
  > "$LAZYMIND_ACCEPTANCE_ROOT/launch.log" 2>&1 &
LAZYMIND_ACCEPTANCE_PID=$!
printf '%s\n' "$LAZYMIND_ACCEPTANCE_PID" > "$LAZYMIND_ACCEPTANCE_ROOT/app.pid"
```

等待主界面可用。首次启动新 runtime 需要初始化；先在应用内完成登录或本地自动登录，并配置一个可用模型。在普通聊天中发送“只回复：模型可用”，确认有真实结果。不要导入业务库。

```sh
python3 "$LAZYMIND_ACCEPTANCE_HELPER" status
python3 "$LAZYMIND_ACCEPTANCE_HELPER" me
```

预期：

- `ttl_seconds` 为 120；`remaining_seconds` 为正数。若不是 120，环境未进入本次认证进程，先停下排查。
- `me` 返回当前测试用户、`status: active`。这里是真正的服务端身份校验；status 中的 JWT 内容只是观察值。
- 记录当前 `user_id`，后面的续期不能换用户。

打开 macOS“系统设置 → 通知 → LazyMind”，允许通知，选择横幅或提醒；暂时关闭“专注模式/勿扰”。若列表暂未出现，首次发送后再检查。首次权限提示选择允许。

```sh
ls "$LAZYMIND_ACCEPTANCE_ROOT/profile/credential-device.json"
```

应存在，确认没有误用原应用页面配置目录。

## 5. 创建仅用于验收的任务，检查前台通知

```sh
python3 "$LAZYMIND_ACCEPTANCE_HELPER" setup
python3 "$LAZYMIND_ACCEPTANCE_HELPER" run foreground
```

setup 只创建/配置本验收任务：名称“后台续期人工验收”，只开桌面成功/失败通知；创建后立即禁用自动排程，后面每次 run 只手动执行一次。它不会修改其他任务或接入外部渠道。全局通知或定时任务功能若在设置中关闭，先在验收应用内开启。

预期 setup 输出 `automatic_schedule_disabled: true`，`availability.desktop.state: available`。run 输出 task_id 和 conversation_id。

等待模型完成，再执行：

```sh
python3 "$LAZYMIND_ACCEPTANCE_HELPER" task foreground
```

预期任务 `succeeded`；只有一条 desktop/succeeded 通知，正文包含“后台通知验收成功”，系统实际出现一条对应通知；收到系统 show 回调后记录 `status: sent`。任务仍 running 时等几秒再查。若任务 failed，先解决模型/任务运行问题，不能把失败通知当作成功场景验收通过。

点击系统通知，预期打开上述 conversation_id 的会话；再查同一任务，不能因查询或点击生成第二条通知。`sent` 仅表示客户端收到系统展示回调，不代表你已肉眼看到横幅，两者分别记录。

## 6. 核心：无窗口跨过期，证明由独立续期入口恢复

保持前台已登录，先记录基线：

```sh
python3 "$LAZYMIND_ACCEPTANCE_HELPER" baseline background
```

立即点击窗口左上角红色关闭按钮。不要最小化，不要停主进程，不要重新开窗口，也不要用浏览器访问本实例。

```sh
ps -p "$LAZYMIND_ACCEPTANCE_PID" -o pid=,comm=
pgrep -P "$LAZYMIND_ACCEPTANCE_PID" -fl 'Helper.*Renderer'
python3 "$LAZYMIND_ACCEPTANCE_HELPER" watch background --seconds 240
```

预期主进程仍在，Renderer 查询无输出；GPU 等其他辅助进程可以存在。watch 每 5 秒只读取凭证文件，不联网、不刷新。约 2 分钟内应输出 PASS，并满足：

```text
same_user       true
same_origin     true
access_changed  true
refresh_changed true
expiry_advanced true
remaining_seconds > 0
```

```sh
grep 'session renew' "$LAZYMIND_ACCEPTANCE_ROOT/session-actions.log"
python3 "$LAZYMIND_ACCEPTANCE_HELPER" me
```

必须同时看到关闭窗口后的 `session renew` 调用记录，且 me 仍为步骤 4 的 active 用户。只有令牌变化而没有 renew 记录，不能证明本次新增入口被实际走到；记录为未覆盖，不强行判通过。

## 7. 保持窗口关闭，触发真实任务并检查通知点击

```sh
python3 "$LAZYMIND_ACCEPTANCE_HELPER" run background
```

继续不打开窗口，等待任务完成并出现系统通知，再执行：

```sh
python3 "$LAZYMIND_ACCEPTANCE_HELPER" task background
```

预期 succeeded、唯一 desktop/succeeded、sent，肉眼看到正文。现在点击这条系统通知，预期应用重建窗口并打开该任务会话；没有弹登录页，也没有换成别的用户。

```sh
python3 "$LAZYMIND_ACCEPTANCE_HELPER" me
python3 "$LAZYMIND_ACCEPTANCE_HELPER" task background
```

记录打开会话是否正确、是否重复弹出。若系统没有横幅，先查通知中心，再分别记录 OS 展示与后端状态。

## 8. 再次关窗续期，验证多轮交接

```sh
python3 "$LAZYMIND_ACCEPTANCE_HELPER" baseline second
```

再次点红色关闭按钮，然后：

```sh
python3 "$LAZYMIND_ACCEPTANCE_HELPER" watch second --seconds 240
python3 "$LAZYMIND_ACCEPTANCE_HELPER" run second
```

任务完成后点击第二轮的系统通知，执行：

```sh
python3 "$LAZYMIND_ACCEPTANCE_HELPER" task second
python3 "$LAZYMIND_ACCEPTANCE_HELPER" me
```

预期和步骤 7 一样。此步专门查“第一次正常，第二次重开却拿回旧令牌”的缺陷。

## 9. 冷启动：后台轮换后真正结束并重开进程

```sh
python3 "$LAZYMIND_ACCEPTANCE_HELPER" baseline cold_before
```

关窗，继续：

```sh
python3 "$LAZYMIND_ACCEPTANCE_HELPER" watch cold_before --seconds 240
python3 "$LAZYMIND_ACCEPTANCE_HELPER" baseline cold_saved
```

此时不要重新打开窗口。只结束这次记录的验收主进程；不要在任务运行中执行：

```sh
ps -p "$LAZYMIND_ACCEPTANCE_PID" -o pid=,comm=
kill -TERM "$LAZYMIND_ACCEPTANCE_PID"
sleep 15
ps -p "$LAZYMIND_ACCEPTANCE_PID" -o pid=,comm=
```

最后一条应无输出。如果仍在，停止此步排查，不以 Cmd+Q 代替真正退出。

用同一终端、同一配置重新启动：

```sh
"$LAZYMIND_ACCEPTANCE_APP/Contents/MacOS/LazyMind" \
  --user-data-dir="$LAZYMIND_ACCEPTANCE_ROOT/profile" \
  >> "$LAZYMIND_ACCEPTANCE_ROOT/launch.log" 2>&1 &
LAZYMIND_ACCEPTANCE_PID=$!
printf '%s\n' "$LAZYMIND_ACCEPTANCE_PID" > "$LAZYMIND_ACCEPTANCE_ROOT/app.pid"
```

待界面就绪：

```sh
python3 "$LAZYMIND_ACCEPTANCE_HELPER" compare cold_saved
python3 "$LAZYMIND_ACCEPTANCE_HELPER" me
python3 "$LAZYMIND_ACCEPTANCE_HELPER" run cold
```

預期原用户仍 active、无登录错误，任务通知仍正常。若重启及时且没有新的正常刷新，access_changed/refresh_changed 应为 false，表明没有用页面旧凭证覆盖后台结果。如果端口改变或启动期间又跨过期，单靠 compare 不能证明摘要交接成功，应将此细项记录为不确定，并保留时序。此步不验证“重启前系统历史通知点击冷启动”，那是另一个验收项。

## 10. 可选：可控临时故障与自动恢复

这是验收包装器模拟 connector 暂时不可用，不是断开真实网络，也不修改生产代码。关闭 Wi-Fi 不会切断本机 127.0.0.1 认证服务，不适合作为此项证明。

先确保前面 cold 任务已完成、用户仍登录：

```sh
python3 "$LAZYMIND_ACCEPTANCE_HELPER" baseline outage
touch "$LAZYMIND_ACCEPTANCE_ROOT/simulate-renewal-outage"
```

关窗，然后：

```sh
sleep 150
python3 "$LAZYMIND_ACCEPTANCE_HELPER" compare outage
grep 'session renew' "$LAZYMIND_ACCEPTANCE_ROOT/session-actions.log" | tail -10
```

预期相同用户/来源，旧令牌未换且已过期，出现间隔逐步拉长的 renew 尝试；不出现循环弹窗。若另一个组件提前轮换了令牌，本故障细项不能判通过。

恢复：

```sh
rm "$LAZYMIND_ACCEPTANCE_ROOT/simulate-renewal-outage"
python3 "$LAZYMIND_ACCEPTANCE_HELPER" watch outage --seconds 180
python3 "$LAZYMIND_ACCEPTANCE_HELPER" run recovered
```

预期等待当前退避结束后原用户成功续期，任务又能通知；不要点击旧任务“重试执行”来恢复通知。

## 11. 刷新凭证失效时停止，不自动换成管理员

在所有成功场景完成后做此项。确认上个任务已结束，窗口保持关闭，等待一个新轮换，保证接下来 access token 尚未过期：

```sh
python3 "$LAZYMIND_ACCEPTANCE_HELPER" baseline revoked
python3 "$LAZYMIND_ACCEPTANCE_HELPER" watch revoked --seconds 240
python3 "$LAZYMIND_ACCEPTANCE_HELPER" baseline revoked_latest
python3 "$LAZYMIND_ACCEPTANCE_HELPER" revoke
sleep 150
python3 "$LAZYMIND_ACCEPTANCE_HELPER" compare revoked_latest
grep 'session renew' "$LAZYMIND_ACCEPTANCE_ROOT/session-actions.log" | tail -10
```

revoke 通过真实 logout API 只撤销本验收 refresh token，故意保留本地旧凭证以观察后台行为。预期 refresh/access 不再轮换、原令牌过期；一次明确续期拒绝后停止，不出现新管理员会话。再等 65 秒观察 renew 日志末尾不再增加。重新登录才能恢复。页面本身既有的本地自动登录可能在你重开窗口后生效，因此“不自动管理员回退”的结论只取重开窗口前的后台时段。

## 12. 可选：明确登出与用户切换

完成步骤 11 后重新打开测试应用并登录。先确认 me，然后在账号菜单使用“退出登录”（不是关窗）。

```sh
test ! -f "$LAZYMIND_HOME/credentials.json" && echo SESSION_CLEARED
tail -10 "$LAZYMIND_ACCEPTANCE_ROOT/session-actions.log"
```

预期 SESSION_CLEARED、出现 session clear；关窗等待超过 2 分钟后不应有旧用户 renew。如果页面的既有自动登录立即产生了新的 session set，应记录该事实，不能将它误判为后台旧会话复活，也不能声称本细项已覆盖。

有第二个测试账号 B 时：登录 B，执行 me，确认 user_id 与 A 不同；查询 A 的任务应 HTTP 404，而非显示 A 的正文：

```sh
python3 "$LAZYMIND_ACCEPTANCE_HELPER" me
python3 "$LAZYMIND_ACCEPTANCE_HELPER" task foreground
```

尝试点击 A 留在通知中心的旧通知，不能进入 A 的任务内容。没有第二账号则记录未测；不要修改现有业务账号来凑条件。

## 13. 收尾与恢复正常环境

验收任务在 setup 时就禁用了自动排程；如用原账号仍能调用接口，可再确认一次：

```sh
python3 "$LAZYMIND_ACCEPTANCE_HELPER" cancel
```

如果已撤销或退出登录导致 401，不为清理重新触发管理员会话，保留隔离目录即可。确认无运行任务后结束本验收 PID：

```sh
kill -TERM "$LAZYMIND_ACCEPTANCE_PID"
unset LAZYMIND_JWT_TTL_MINUTES LAZYMIND_HOME LAZYMIND_DESKTOP_RUNTIME_ROOT
unset LAZYMIND_DESKTOP_AGENT_CONNECTOR LAZYMIND_ACCEPTANCE_BIN
```

正常从原位置启动原应用，就恢复默认 60 分钟配置。本次未修改源码默认 TTL；隔离目录保留供排查，不执行清库或 reset。目录中 credentials/profile 含敏感数据，不能整体上传。

## 结果记录

| 项目 | 结果 | 证据 |
| --- | --- | --- |
| 当前源码构建及新 CLI 命令 | 待验收 | 构建成功/CLI_INVALID |
| 两分钟 TTL、原用户 active | 待验收 | status + me |
| 前台成功通知与正确点击 | 待验收 | task foreground + 视觉观察 |
| 无 renderer 后独立续期 | 待验收 | watch PASS + session renew + me |
| 续期后后台真实通知 | 待验收 | task background + 视觉观察 |
| 多轮关闭/重开 | 待验收 | second |
| 冷启动交接 | 待验收 | cold_saved 对比 + me |
| 临时故障恢复（模拟边界） | 待验收 | outage/recovered |
| 真实 refresh 撤销后停止 | 待验收 | revoked_latest + renew 时序 |
| 显式登出/跨用户隔离 | 待验收 | clear/B 返回 404 |

反馈只需：步骤编号、上表输出、是否看到通知、点击去了哪里。helper 输出是摘要而非令牌；session-actions.log 只含时间和动作名称，可用于反馈。launch.log 等原始运行日志请先脱敏。

本清单验证 Desktop 主线；数据库通知失败回滚、终态竞态、外部渠道重试链的证据仍来自前一批自动测试，不把这些真机步骤冒充为三渠道及 PostgreSQL/Compose 的完整系统验收。
