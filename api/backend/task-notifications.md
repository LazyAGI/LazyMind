# 定时任务通知后端契约

本契约只覆盖定时任务。聊天产生的后台任务提醒保持原有行为。Core 对外路径以 `/api/core` 为前缀；Gateway 路径以 `/api/channel-gateway/v1` 为前缀。浏览器经既有 Kong/本地代理认证，身份由可信代理注入；不得把 Core/Gateway 内部端口开放为无需认证的公共接口。

## 配置与运行

| Core 方法与路径 | 行为 |
| --- | --- |
| GET/PATCH `/user/notification-preferences` | 总开关、默认规则、独立 revision |
| GET/PUT `/schedules/{schedule_id}/notifications` | 规则、configured、revision、availability |
| POST `/schedules/{schedule_id}/notifications:reset` | 按请求 revision 将当前用户默认值复制到指定任务 |
| GET `/task-center/tasks/{task_id}/notifications` | 本次执行不可变 snapshot 与各渠道通知记录 |
| GET `/notification-account-references/{account_id}` | 当前用户默认规则、任务定义、活动运行中的账号引用 |
| GET `/task-center/desktop-notifications` | 本实例桌面待通知记录 |
| POST `/task-center/desktop-notifications/{notification_id}:ack` | 原生客户端幂等回执 |
| POST `/task-center/notification-events/{notification_id}:claim` | 内部服务在每一分段发送前领取许可；同时要求可信用户身份和现有内部服务 Token |

GET 使用 `qa.read`，写入使用 `qa.write`。Core 沿用统一 `{code,message,data}` 包装。错误沿用现有数字错误目录，并在 `data.detail` 返回稳定 `reason`、`request_id`；关闭确认额外返回 `running_task_ids`。不向客户端返回依赖异常原文。

规则示例（PUT 请求另含本次读取的 `revision`，规则置于 `config`）：

```json
{
  "events": {
    "succeeded": {"enabled": true, "content": "summary"},
    "failed": {"enabled": true, "content": "summary"},
    "waiting": {"enabled": false, "content": "summary"}
  },
  "channels": {
    "desktop": {"enabled": true},
    "wechat": {"enabled": true, "account_id": "账号稳定ID", "recipient_id": "已知接收对象"},
    "feishu": {"enabled": false},
    "wecom": {"enabled": false}
  }
}
```

新用户总开关默认开启，成功/失败为摘要，人工等待默认关闭，桌面默认开启。其他渠道须显式选择目标。升级前未配置的任务保持 `configured=false, config=null, revision=0`；新任务复制当时默认配置。修改默认值不覆盖旧任务。运行创建时固定规则快照，修改任务只影响下一次运行。等待依赖不属于人工待处理通知。

修改配置采用 revision 比较并递增，冲突需重新读取。关闭总开关时，若存在受影响运行，首次返回 409 及精确 ID 集合；用户确认后携带原 revision、`enabled=false`、`confirm_running_task_ids` 重试。集合或版本变化需要重新确认。总开关不停止任务；已经获得分段许可的外部请求无法撤回，其结果仍按实际回执记录。尚未获得许可的发送被阻止；关闭期间产生及被抑制的记录永久跳过，再开启不补发。

默认配置必须至少开启一个事件；单任务可关闭所有渠道。关闭事件/渠道保留其内容与目标。`availability` 单独表示 disabled/available/unavailable 及安全原因，不改变保存的配置。平台当前连通性会变化，配置通过不保证未来投递成功。

## 渠道账号和投递

| Gateway 方法与路径 | 行为 |
| --- | --- |
| POST `/connection-sessions` | 微信/飞书沿用扫码；企业微信传 provider=wecom、credentials.bot_id/secret |
| GET `/channel-accounts?provider=…` | 复用现有按平台账号列表 |
| GET `/channel-accounts/{account_id}` | 安全账号详情、能力、头像或 null、唯一已知接收对象或 null、引用数量 |
| DELETE `/channel-accounts/{account_id}` | 软断开并清除凭据/微信上下文，保留身份和历史 |
| GET `/channel-accounts/{account_id}/notification-targets` | 已知接收对象和 available；支持 recipient_id 精确筛选 |
| GET `/channel-accounts/{account_id}/notification-references` | 断开前展示影响；转发 Core 所有权范围内的引用 |
| POST `/task-notifications` | 校验 Core 已提交事件的全部内容和目标，确定性幂等入队 |
| GET `/task-notifications?task_id=…` | 按任务读取本用户全部渠道及重试历史，支持 cursor/limit |
| GET `/task-notifications/{notification_id}` | 状态、原因、尝试次数、时间、原始事件和 retry_of |
| POST `/task-notifications/{notification_id}:retry` | 只重试通知，不重跑任务；保留原失败历史 |

企业微信使用官方 `wecom-aibot-python-sdk==1.0.1`，以长连接接收文本并主动发送 Markdown，复用公共 Inbox、消息路由、Outbox、分段和租约。此模式不提供二维码。重连时传原 `account_id`，必须与原平台身份一致；不同身份返回 409，不能替换绑定。微信必须先有来自正确账号/收件人的有效会话上下文；只登录无法保证主动通知可用。上下文加密存储、发送前重新读取，断开后清除。

账号有多个已知接收对象时，详情 `primary_recipient=null`，必须显式选择，不能默认取列表首项。头像不可取得时返回 null。详情的引用数量包含默认规则、任务定义和活动运行，具体类别由引用列表区分；Core 不可用时返回明确 503，不用零代替未知数量。

投递状态：`queued`、`sending`、`sent`、`failed`、`skipped`、`unknown`；Core 交接前为 `pending`，桌面拒绝权限为 `unavailable`。通知展示任务名称、状态、事件发生时间和实际结果。摘要最多 1000 字符，桌面最多 200 字符；全文按已有发送器分段，极长结果明确提示到任务中查看完整结果。企业微信附件明确降级为任务查看提示，不宣称附件已发送。

Core 提交事件和输出/状态在同一事务；网关复用现有 Outbox 唯一键和工作线程，不另起投递服务。入队响应丢失可安全重放。每段发送前再次验证 Core 事件、总开关、账号/目标并领取许可。可确定发送前失败最多尝试 5 次，指数退避上限 300 秒。无成功/失败确认、发送后崩溃或租约丢失记为 unknown，不自动盲重发。断开时已在发送的记录也保留 unknown；未开始的排队记录跳过。

最终聊天历史已经提交后，通知持久化故障会回滚本次收尾，保留原非终态并由既有恢复扫描重试；不能将成功执行改成业务失败或要求重新运行任务。恢复后只提交一份最终输出和对应事件，依赖任务再消费该结果。旧未配置任务也参与结果恢复，但保持配置为空且不回填通知。会话同步的实际更新条件保护已有成功、失败、取消和跳过终态。

人工重试请求：`{"idempotency_key":"一次用户操作的稳定键","confirm_duplicate_risk":true}`。failed 可直接重试；unknown 必须确认可能重复，缺确认返回 409。新记录保存 retry_of，并保留已确认分段进度。重试仍验证当前总开关和目标。共享 Outbox 使用 120 秒租约、30 秒续租；多个 worker 不能同时推进同一记录。

重试资格在事务内按同一用户、事件、渠道、账号和接收对象的完整尝试链判断，不能只检查被点击的历史记录。链中存在排队、发送中或已成功的尝试时，新操作返回 409 `NOTIFICATION_STATE_CHANGED`；最新有效尝试为 unknown，或历史其他分支仍有未知风险时，未确认返回 409 `NOTIFICATION_CONFIRMATION_REQUIRED`。选择失败祖先不能绕过上述限制。新的尝试保留点击来源 `retry_of`，从最新有效尝试继承已确认分段进度；历史不变。同一来源记录与相同操作键始终返回已创建的回执，后续状态或总开关变化不导致再次入队；不同操作键并发最多创建一个活动尝试。

## 原生桌面通知接入

仅 Desktop/Local 的本实例提供桌面流，设备 ID 固定 `local`（可省略）；其他设备拒绝，Compose 返回 503 不可用。本期没有云端多设备注册。查询支持 `cursor`、`limit`，默认 20、最大 100；使用稳定通知 ID 去重。通知含：

```json
{
  "notification_id": "稳定通知ID",
  "user_id": "当前已认证用户ID",
  "app_name": "LazyMind",
  "title": "每日简报",
  "body": "今日简报的实际结果预览……",
  "execution_id": "本次执行ID",
  "task_id": "本次执行ID",
  "schedule_id": "定时任务ID",
  "navigation": {"type": "task", "task_id": "本次执行ID", "schedule_id": "定时任务ID"}
}
```

Electron 主进程现已使用 LazyMind 应用名称及图标接入原生 Notification，标题与正文直接使用上述数据。预加载脚本监听已有登录存储和 `lazymind:user-change` 事件，复用原会话 IPC；认证服务确认当前用户后才读取本实例通知。退出/换号取消旧请求、关闭旧通知，后台关闭窗口保留通知接收。没有修改前端页面或新增路由。

同用户正常刷新令牌会使旧网络请求失效，并在认证服务确认同一用户、同一实例后继续使用原通知对象。验证期间收到的系统 show 回调保存为待回执进度，验证通过后使用新凭证补交，不重复弹出；已展示通知的点击能力保留。重复同步相同有效会话不会清理通知。换号、实例变化、退出或明确无效身份仍撤销旧通知；身份未验证时不提交旧通知回执或导航。

点击通知时重新查询所属任务；有会话则打开现有 `/agent/chat/home/{conversation_id}`，无会话打开 `/task-center?tab=tasks`。不使用通知自带的任意 URL。前后台均可提交启用的任务通知；声音、权限、横幅和勿扰遵从系统设置，不使用 critical 优先级绕过。macOS 已验证实际 show 回调及后端回执，横幅外观/实际点击、Windows 真机仍待验收。

收到系统 show 事件后回执 `{"device_id":"local","status":"delivered"}`；只有可靠的权限拒绝信号才可使用 `permission_denied`。Electron 31 的 isSupported 不代表用户授权，因此桥接不会根据 failed/无回调猜测权限或假报成功；delivered 也不证明用户已阅读。重复回执保留首次结果。桥接以用户、实例和通知 ID 持久去重；系统提交与回执不能原子完成，提交中崩溃保留未知，不自动重复弹出；已收到 show 但回执失败则重启补交。

桥接每 5 秒串行查询、单次请求最长 10 秒、查询故障退避上限 60 秒，分页最多 100 条、每轮最多 20 页；认证失效时暂停，现有登录/令牌刷新同步后恢复。状态文件只保存哈希作用域、通知 ID、投递阶段，不保存正文和凭据；最多 5000 条（优先淘汰已确认记录），读取大小上限 2 MiB，Unix 文件权限 0600。损坏/不可写时安全停止投递并记录固定诊断码。当前桥接不单独刷新或自动切换管理员身份，退出后也不自行恢复登录。

## 错误与限制

| HTTP / 稳定原因 | 调用方处理 |
| --- | --- |
| 401 UNAUTHORIZED | 缺失可信用户/服务身份 |
| 404 NOTIFICATION_NOT_FOUND / ACCOUNT_NOT_FOUND | 不存在或不属于当前用户 |
| 409 NOTIFICATION_CONFIG_CONFLICT | 重新读取版本 |
| 409 NOTIFICATION_CONFIRMATION_REQUIRED | 确认受影响运行或未知结果重复风险 |
| 409 ACCOUNT_IDENTITY_MISMATCH | 用原平台身份重连或新增独立账号 |
| 409 NOTIFICATIONS_DISABLED | 已关闭/已永久跳过，不能补发 |
| 409 NOTIFICATION_STATE_CHANGED | 当前记录不能重试 |
| 422 NOTIFICATION_EVENT_REQUIRED | 至少保留一个适用事件 |
| 422 NOTIFICATION_TARGET_REQUIRED | 显式选择账号和收件人 |
| 422 NOTIFICATION_TARGET_UNAVAILABLE | 重连或从正确会话重新建立上下文 |
| 422 NOTIFICATION_EVENT_INVALID | 来源或正文/目标与 Core 事件不符 |
| 413/422 INVALID_REQUEST | 请求大小、类型、重复/未知字段或允许值错误 |
| 403/503 NOTIFICATION_DEVICE_UNAVAILABLE | 非法接收类别，或部署不支持 local 原生接收类别 |
| 503 NOTIFICATION_CORE_UNAVAILABLE / NOTIFICATION_UNAVAILABLE | 依赖不可用；不放行发送 |
| 500 NOTIFICATION_INTERNAL_ERROR | 安全服务错误，使用 request_id 排查 |

记录原因另含 `NOTIFICATION_DELIVERY_FAILED`、`NOTIFICATION_DELIVERY_UNKNOWN`、`DESKTOP_NOTIFICATION_PERMISSION_DENIED`；企微连接会话使用 `WECOM_AUTH_FAILED`，不会返回原始 SDK 错误。Gateway 沿用 `{error:{code,message,retryable,request_id}}`。现有 Core 数字错误码目录不新增号码；此表定义本功能安全 detail 原因。

配置/凭据请求最多 16 KiB，网关通知请求最多 2 MiB、正文最多 262144 字符；Core 全文上限更低以容纳降级提示。一般标识最多 256 字符，重试键最多 128；通知目标及引用列表默认 20、最大 100。既有账号列表沿用旧接口，不能把新增分页限制误套到旧接口。

## 运行与兼容

Compose 复用现有内网服务名和内部服务 Token；Desktop/Local 运行管理器注入回环 Core/Gateway 地址及相同的 `LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN`。Core 可用 `LAZYMIND_CHANNEL_GATEWAY_BASE_URL` 指定网关，Gateway 沿用 `LAZYMIND_CHANNEL_GATEWAY_CORE_BASE_URL`。Secret 由部署环境配置，客户端不得持有内部服务 Token。

Core 新增 dev 迁移 `20260914145559_add_task_notifications`，含 up/down；同步原有 v0_3 聚合迁移。Gateway 复用现有状态库初始化升级，新增接收对象表及重连身份字段，不删除账号/历史。PostgreSQL 与文件 SQLite 使用同一公开契约，分别验证迁移、事务与投递。

依赖仍由 Gateway requirements 安装；Compose Dockerfile 与 Desktop/Local 现有打包路径自动包含新增适配器及批准的 SDK，无新服务。真实平台消息可见性需要指定测试账号/收件人后的联调；两系统原生弹窗外观和实际点击仍需系统验收，macOS 已通过实际 show/回执验证。不能用协议 Fake 代替这些验收。

### Desktop 无窗口会话续期

Electron 常驻且窗口已关闭时，已验证会话遇到 401 会由主进程调用既有认证 refresh 接口，并通过 auth/me 校验同一 active 用户后恢复通知。复用 CLI 凭证文件锁和原子写入，不回退为本地管理员，不跟随重定向。临时失败指数退避至 60 秒；刷新凭证明确失效、身份不符或退出时停止并要求重新登录。前台继续使用原有刷新流程。

重建窗口等待正在进行的续期结束，preload 在页面启动前交接新凭证。应用重启通过既有凭证文件中的原会话摘要匹配交接，不保存额外旧明文令牌；受信主 frame、同 origin、完整原令牌对匹配缺一不可。通知状态文件仍不保存凭证。内部 CLI 失败仅返回稳定状态 `DESKTOP_SESSION_AUTHENTICATION_REQUIRED`、`DESKTOP_SESSION_RENEWAL_UNAVAILABLE` 或输入非法的 `DESKTOP_SESSION_INVALID`；不改变公开后端 API 错误码。

已收到新令牌但身份查询暂时不可用时，主进程只在内存保留候选，重试身份校验；验证通过前不写成有效凭证或向页面交接。候选仅通过受控内部 CLI 数据字段传递，不进入日志或异常原文。

认证服务的 refresh token 为一次性轮换：如果服务端已消耗旧令牌但新的响应丢失，或续期后的身份核验始终无法完成，不能保证无需重新登录。本实现不创建新的服务端刷新重放协议，也不在完全退出 Electron 后运行。


## 浏览器系统通知（2026-09-17）

用户确认网页打开（含切换标签页、最小化）时接收，同源同用户多个标签页只弹一次。新增固定 `device_id=browser` 接收类别供 Local/Compose 网页使用；查询/回执继续复用上述 desktop-notifications 路径、集中鉴权、用户归属和全局开关。`local` 仍只用于 Local/Desktop 原生实例，不接受任意设备地址。配置字段保持 `desktop`，表示系统通知，既有配置无需迁移。渠道 availability 表示服务端可供消费；接收端的安全上下文、权限和运行能力由客户端判断。

浏览器使用原生 Notification（标题/200 字符摘要）及 Web Locks 协调同源标签页；前端复用原认证/刷新和任务结果查询，无新生产依赖、后台推送服务或数据库变更。授权只能由用户点击触发。每 5 秒查询，单请求 10 秒，故障退避至 60 秒；后台休眠可能延迟。关闭全部页面停止查询，重新打开可领取仍 pending 的记录。

只在 show 事件后回执 `device_id=browser,status=delivered`；无授权不回执成功或覆盖其他客户端状态。持久去重日志最多 5000 项，不含正文或凭据；结果未知不自动重弹，已展示只补交回执。关闭标签页可由其他标签页接管。清除站点存储（包括现有退出登录的 localStorage 清理）会移除客户端日志，服务端已确认通知仍不重复返回。

本次不是多设备广播：沿用单条通知首次成功回执后退出 pending 的语义。不承诺浏览器和 Electron 同时在线时跨进程去重。展示遵循系统权限与勿扰；浏览器标识/站点名不能伪装成 Electron 应用身份。OS 横幅和通知中心实际可见性需真机验收。

系统通知 feed 显式返回当前已认证的 `user_id`，供浏览器/Electron 校验会话归属；该字段由服务端身份生成，不从请求参数读取，不改变 ORM 其他接口对所有权字段的隐藏规则。


## 任务草稿与原子保存

`POST /schedules` 和 `PUT /schedules/{schedule_id}` 可携带可选 `notification` 对象：`{ "revision": 3, "config": { ... } }`，或 `{ "revision": 3, "clear": true }`。创建时 revision 由服务端以初始规则版本确定；更新时比较当前通知版本。config 与 clear:true 互斥，config 的校验、目标归属和可用性检查复用独立通知接口。任务内容、依赖与通知更新位于同一事务，任一步失败全部回滚；冲突为 HTTP 409 / NOTIFICATION_CONFIG_CONFLICT。

省略 notification 保持旧客户端行为：创建复制默认规则，编辑不改变通知。显式草稿覆盖仅针对新任务，不改变其他任务及全局默认。初始默认复制后应用显式草稿，因此显式创建的通知 revision 为 2；调用者后续以读取接口返回的 revision 为准。

独立 `PUT /schedules/{schedule_id}/notifications` 同样支持 `{ "revision": 3, "clear": true }`。清除将后续规则置为未配置并增加 revision，保留已生成的执行快照和通知历史。仅发送 `config:null` 或漏 config/clear 仍返回 422 / INVALID_REQUEST，避免误清除。


## 飞书复用、暂停和解除绑定（2026-09-18）

普通断开使用 `POST /api/channel-gateway/v1/channel-accounts/{account_id}:pause`（204），保留加密凭据，停止运行租约和待发送队列。`POST .../{account_id}:resume`（200，返回安全 AccountView）以凭据版本比较恢复原账号，并发请求只对实际状态转换启动运行实例，不重新扫码，不补发已终止消息。原 `DELETE /channel-accounts/{account_id}` 仍然清凭据，界面称为“解除绑定”；两者保留账号、历史和通知引用，并取消该账号进行中的重新授权会话。

暂停/恢复仅支持飞书，沿用 `qa.write` 集中授权和当前用户所有权。不存在或不属于当前用户返回 404 `ACCOUNT_NOT_FOUND`；其他渠道返回 422 `PROVIDER_NOT_SUPPORTED`；无本地凭据/无法解密返回 409 `FEISHU_REAUTHORIZATION_REQUIRED`；并发状态变化返回 409 `ACCOUNT_STATE_CHANGED`。远端凭据是否被撤销由真实连接结果确认，恢复接口不把本地解密成功当作远端鉴权成功；界面保留独立重新授权入口。

`POST /connection-sessions` 新增严格布尔 `create_new=false`：仅飞书支持 true，不可与 account_id 同传。默认优先复用唯一现有机器人，多项未选返回 409 `ACCOUNT_SELECTION_REQUIRED`；已解除绑定的历史记录不是首次接入。明确 account_id 可恢复暂停连接，缺少凭据则重新授权该机器人。独立 `reauthorize=false`（严格布尔）允许用户明确发起原机器人重新授权，true 仅允许飞书且要求 account_id、禁止 create_new。首次无历史及明确 create_new 才进入创建；已存在幂等请求返回原会话，切换账号或从复用切换新增返回 409 `ACCOUNT_STATE_CHANGED`。

重新授权沿用 SDK `create_only=false`，有可解密 app_id 时指定原应用；最终严格比对 app_id + open_id。身份不同返回 `ACCOUNT_IDENTITY_MISMATCH`，不创建或覆盖其他账号。成功只替换原记录加密凭据；失败、取消和过期均不删除原账号，不重复发送欢迎消息。新增默认值兼容旧客户端；微信/企微原连接逻辑不变。复用既有 requested_account_id 和加密会话状态，无 Schema 或生产依赖变化。


## 飞书账号记录管理

`PATCH /api/channel-gateway/v1/channel-accounts/{account_id}` 接受 `{ "label": "工作号 · 每日简报" }`，去除首尾空白，1–80 字符，拒绝控制字符和未知字段，返回安全 AccountView。仅修改本地备注。

`POST /channel-accounts/{account_id}:archive` 返回 204，从活动列表移除已解绑记录并保留历史。已连接或仍保留凭据的暂停记录返回 409 `ACCOUNT_UNBIND_REQUIRED`；重复删除当前用户自己的记录返回 204；不存在/非当前用户返回 404 `ACCOUNT_NOT_FOUND`。两接口暂仅飞书，其他渠道返回 422 `PROVIDER_NOT_SUPPORTED`，权限 qa.write。归档事务取消该账号仍在进行的授权会话，之后禁止重连、改名和发送；不删除飞书侧应用，不自动改绑通知规则。

AccountView 追加 `binding_status=connected|paused|unbound` 与 `identity={app_id,authorized_name,authorized_id}`，authorized_id 是当前飞书应用内的用户标识，不能用于推断跨应用身份。密钥不在响应中。信息缺失返回空字符串，前端明确显示缺失，可编辑备注。备注、授权人和应用 ID 用于卡片与选择器，避免同名机器人混淆。

Gateway 增量字段 identity_metadata（TEXT，默认空 JSON 对象）和 archived_at（TIMESTAMPTZ，可空）同时支持 SQLite/PostgreSQL；未修改 Core migration。旧记录有可解密凭据时回填标识，解绑保留标识。归档保留所有历史；旧代码回退会重新显示这些已解绑记录，不能自动发送，无需删除新增字段。

重新授权优先使用可解密凭据的 App ID；凭据已清除时可使用白名单标识中保留的 App ID 定位原应用，最终仍核对原 app_id + open_id。旧版本已清空且尚未保留标识的记录无法反推身份，显示未知，允许手工修改备注。

飞书通知接收对象同时由真实工作区消息入库路径登记，与普通渠道复用相同事务内登记逻辑。启动时从已有同账号、同所有者的飞书消息历史幂等补齐缺失对象，仅处理仍已连接且凭据存在的账号，不重放消息、不补发通知、不为微信或企业微信推导接收上下文。

### 连接默认接收对象与飞书群候选

- `GET /api/channel-gateway/v1/channel-accounts/{account_id}/notification-groups`：`qa.read`，仅当前所有者的已连接飞书机器人。`limit=1..100`（默认 100），`cursor` 最长 2048。返回 `items[{recipient_id,label,kind:"group",available}]` 与 `next_cursor`；使用应用身份读取机器人所在群，分页结果缓存于现有接收对象表。
- `PUT /api/channel-gateway/v1/channel-accounts/{account_id}/default-recipient`：`qa.write`，请求 `{recipient_id}`，最长 256，空字符串清除。只能选择此连接已知且可用的对象；保存群对象及发送通知前验证机器人仍在群中。
- 账号视图新增 `default_recipient_id`，详情新增 `default_recipient`。原 `primary_recipient` 的单一候选兼容语义保留，不能当作显式配置默认值。旧账号默认空；已有任务、全局规则、历史快照不会因修改默认值而改写。
- 前端选择连接时复制显式默认对象到草稿，保存后使用固定的账号与对象 ID；发送时不回退到连接的新默认对象。
- `404 ACCOUNT_NOT_FOUND`：不存在、非所有者或已归档。`409 ACCOUNT_STATE_CHANGED`：校验期间凭据/账号状态变化。`409 FEISHU_REAUTHORIZATION_REQUIRED`：需重新授权。`422 NOTIFICATION_TARGET_UNAVAILABLE`：非此账号候选、断开连接、会话不可用或机器人退群。`503 FEISHU_GROUPS_UNAVAILABLE`：平台权限或服务不可用；不返回平台原始异常。输入不合法返回现有 422 校验错误。
- 新飞书授权请求只增加 `im:chat:readonly`。已有机器人缺少权限时需由用户重新授权或在飞书开放平台配置；不启用群对话，不请求读取所有群消息的权限。微信/企微复用现有会话候选，不提供飞书群列表能力。
- SQLite/PostgreSQL 增量添加账号默认 ID 以及目标的名称、类型字段；升级保留旧记录，不推断默认对象。旧版本忽略新增字段；不需要删表或清库。
