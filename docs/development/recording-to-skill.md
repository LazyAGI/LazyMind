# 录屏转 Skill

入口为对话输入框 `+ → 录制技能`。首次使用显示安装卡片及敏感信息提示。客户端选择电脑屏幕后，自动采集画面、鼠标和键盘操作；无需浏览器扩展或选择浏览器页面，不再采集 DOM。取消不提交生成任务，并销毁采集缓冲；停止后提交时间有序的证据。未开始的空对话在首次提交有效录制时创建。

## 采集及生成

画面每秒采集一张 JPEG，长边不超过 1280 像素，最多 120 张/约 2 分钟，总体积约 20 MiB；不采集音频，不保留原始视频。Electron 使用主窗口限定、有用户手势的屏幕选择菜单，不会自动选择屏幕。浏览器使用 `getDisplayMedia`，需要 HTTPS 或 localhost。

客户端通过 `uiohook-napi` 在独立 Electron utility process 中监听系统鼠标和键盘。仅在明确选择屏幕后开启；停止、取消、窗口销毁、页面导航或崩溃、退出客户端都会停止监听，原生进程自身有 2 分钟超时及父进程退出保护。macOS 首次录制需允许录屏和辅助功能；权限不足显示引导。部分系统还需在输入监控中授权或重启客户端。

鼠标包括按下、释放、位置变化（最多每秒 4 次）及滚轮，可结合按下/移动/释放识别拖动。已知所选屏幕边界时仅收集该屏幕鼠标事件，并附上相对于画面的归一化坐标。键盘操作为系统级，不读取 DOM、输入框或剪贴板。按键和原始键码直接上传；macOS 同时保留 CGEvent 提供的 Unicode 文字，浏览器保留实际 key 和 input value，不做内容脱敏。Windows uiohook 仅提供键码和修饰键；桌面键盘事件不保证包含输入法最终上屏文字，模型需结合画面，缺失时请求补充。鼠标键盘记录和画面使用同一时间轴，最多 1500 个事件、约 1.5 MB；截断会标注 limitations。

目前纯浏览器端只能录制画面，会明确提示使用客户端采集系统操作。若要让网页获得相同能力，需要额外连接后台运行的客户端或本机辅助程序；这一跨端连接尚未实现。普通浏览器扩展不能监听其他原生软件的系统鼠标键盘。

已有旧版浏览器证据和重试请求保持兼容，但新录制界面不再调用 DOM 采集入口。

Core 加载账户的模型配置，调用算法服务的 VLM 联合解析画面与操作证据。优先使用 `vlm` 角色。未单独配置时，与 attachment reader 共用视觉能力选择器：仅使用声明了 `vision: true` 的 LLM（或类型为 `vlm` 的模型），不按模型名称猜测，也不发起探测调用。内置 LLM 在 `model_catalog.yaml` 中配置 `vision: true`；用户添加 LLM 时勾选“是否支持多模态”。未声明时显示配置提示。提示词要求把画面和用户说明作为数据，识别用途、输入、步骤、输出；证据不足时返回问题，不创建技能。快速操作可能发生在采样间隔内，因此用户可以补充步骤或重新录制。该功能不承诺复原每次点击或键盘事件。

生成工作在 Core 后台执行，卡片持久化至 `skill_recordings`；每个账户同时最多一个生成任务。单次调用限制 10 分钟，超过 12 分钟未完成的记录在查询时转为可重试失败。重试递增 attempt，旧任务不能覆盖新结果。原始画面仅在生成/失败/待补充期间留存供重试，成功生成后清除画面、操作证据及补充说明。

## 状态与确认

`generating → pending | needs_input | failed`

`needs_input / failed → generating`（补充或重试）

`pending → kept | discarded`

生成 Skill、绑定卡片和切换 pending 在同一事务内完成。生成的技能携带 `recording:pending` 标记、关闭自动进化且默认禁用；普通 metadata PATCH 不能移除标记或启用技能。我的技能列表显示待确认，并禁用启用开关。详情页复用技能包编辑器，用户应先保存修改，再保留或不保留。

保留操作原子更新卡片、移除标记，并保持技能禁用；不保留操作原子删除技能及其版本关系，并将卡片改为 discarded。重复提交相同决定是幂等的，其他账户无法操作。对话卡片每 4 秒查询，并在窗口重新聚焦时刷新，名称和简介读取当前技能信息；从回收站恢复的技能会重新反映其确认状态。

## Core API

所有接口沿用 Core 身份验证、`qa.read`/`qa.write` 权限及 `{code, message, data}` 响应。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/api/core/skill-recordings/setup` | 查询指引技能是否安装 |
| POST | `/api/core/skill-recordings/setup` | 幂等安装指引技能，默认关闭 |
| GET | `/api/core/skill-recordings?conversation_id=…` | 查询当前账户的对话卡片 |
| GET | `/api/core/skill-recordings?skill_id=…` | 查询技能确认状态 |
| POST | `/api/core/skill-recordings` | 创建或重试生成任务 |
| POST | `/api/core/skill-recordings/browser` | 账户限定的 targets/start/read/stop/cancel 操作采集指令 |
| POST | `/api/core/skill-recordings/decision` | 保留或删除生成的技能 |

首次生成请求：`{conversation_id?, frames: [{image: "data:image/jpeg;base64,…", seconds: 0}, …], evidence?: {events: [{seconds, kind, …}], limitations: string[]}, notes?: string}`。重试请求：`{id, notes?: string}`，画面由服务端已有记录提供。确认请求：`{id, keep: boolean}`。

卡片包含 `id, conversation_id, skill_id, status, name, description, error, created_at, updated_at`；永不向查询接口返回源画面、用户说明或模型密钥。

## 验证

- Go：`go test ./skillv2/handler ./skillv2/service ./algo ./migrate`（在 backend/core）。设置 `MIGRATION_TEST_POSTGRES_DSN` 可同时验证 PostgreSQL。
- 前端：Vitest 运行 `SkillRecording` 及 `ChatInput/index.test.tsx`。
- 桌面：`node --test desktop/electron/tests/input-recording.test.js desktop/electron/tests/screen-capture.test.js desktop/scripts/preload-bridge.test.mjs`。
- 算法：`PYTHONPATH=algorithm python -m pytest tests/algorithm/review/test_recording_skill.py`。
- 人工联调：在有 VLM 配置的运行环境中验证操作画面识别；分别在浏览器和打包桌面端验证操作系统授权、拒绝授权、切换窗口、停止共享及跨页返回后的卡片状态。

### macOS 独立录制组件

macOS 14+ 使用 `LazyMind Recorder.app`（`ai.lazymind.recorder`）持有录屏和辅助功能权限。Swift/ScreenCaptureKit 负责按秒采样所选屏幕或窗口，CGEvent passive tap 负责操作记录。浏览器仍使用屏幕共享；Windows 仍使用 Electron + uiohook。

组件通过用户私有 Unix socket 和一次性握手与桌面壳通信，不开放网络端口；不落盘录屏，采集到的输入内容原样传给模型。取消、主窗口销毁、IPC 断开、父进程退出均停止采集，120 秒自动结束。开始前重启组件以刷新 TCC 状态，LazyMind 主程序不必退出；原有“退出后后台运行”语义保留。

构建：`node desktop/scripts/build-recording-helper.cjs`。electron-builder 的 `beforePack` 自动构建并携带组件。安装在 `~/Library/Application Support/LazyMind/recording-helper/LazyMind Recorder.app`；相同 build-id 不覆盖、不重签。可设置 `LAZYMIND_RECORDING_SIGN_IDENTITY`，正式桌面构建自动使用相同 Developer ID。无证书的本地构建为 ad-hoc，主程序更新不会改变组件；**组件自身代码或签名更新仍可能需要重新授权**，无法承诺临时签名跨版本保留系统授权。

权限应授予 **LazyMind Recorder**。授权后再次点击开始，仅重启组件。勿使用全局 `tccutil reset` 清理其他应用权限。

窗口选项直接绑定 `SCWindow` / `SCDisplay`，不依赖菜单索引，避免重名窗口被合并后录到其他窗口。支持列出其他桌面空间的窗口；截图转为最长边 1280 像素的 sRGB JPEG，并限制单帧与总大小。

若系统没有自动列出组件，可在“录屏与系统录音”上方列表点击“+”，前往上述固定安装目录，选中 `LazyMind Recorder.app` 添加；不要添加到“仅系统录音”。系统提示“退出并重新打开”时只会重启组件。

本地端到端验证（2026-09-17）：打包客户端通过独立组件采集本地待办演示窗口，33 帧经已声明 `vision=true` 的 LLM 生成“待办清单添加与清理”，对话卡片进入 pending，点击卡片能打开真实技能详情，包含输入参数、4 个步骤和结果验证。确认保留后记录变为 kept，技能仍禁用，源画面及操作记录已清除。测试中主程序 PID 保持不变，更新主应用后独立组件文件哈希保持不变。

自动化辅助功能操作可能不经过系统键鼠事件流：上述生成实测主要验证视觉链路，不能据此声称真实键盘和鼠标点击采集已完整验证。
