# 解绑记录和账号标识交付（2026-09-18）

用户要求“直接生产最后测试”，本次已完成实现、统一回归、桌面重建和接口验证。删除采用保留历史的归档方式，未物理删除数据，也未修改用户已有真实连接。

## 交付行为

- 设置 → 连接 → 飞书：账号显示备注、授权人名称及应用 ID；展开可查看完整应用 ID、应用内用户 ID。通知配置选择器复用同一标识格式，区分同名机器人。
- “修改备注”仅修改 LazyMind 中的显示名称，不改变飞书身份或通知引用。允许 1–80 个字符，拒绝空白、控制字符及额外字段。
- “解除绑定”清除密钥，显示“已解绑”；该状态新增“删除记录”。删除前重新查询任务和默认配置引用；确认后从连接列表及可选账号中移除，保留消息、通知历史。其他机器人不受影响，不自动替换引用。
- 已连接、仍有凭据的暂停记录不可删除。重复删除幂等；跨用户返回 404；归档后取消原授权会话、禁止重连和迟到授权回调恢复账号。
- 保留的公开应用 ID 用于定位原应用重新授权，最终仍严格校验原 app_id + open_id。

本次启动后读取到的两条旧记录均已解绑，旧版本已清空其凭据，无法自动恢复授权人和应用 ID。页面明确显示信息缺失，用户可修改备注；重新授权原机器人成功后补齐真实标识。没有从哈希猜测身份，也没有存储或展示密钥。

## 实现与兼容

复用 channel_accounts、label、现有凭据解密、账号服务、引用查询、集中 qa.write 授权和生成 API 客户端。共享存储新增 identity_metadata（仅三个公开标识，TEXT 默认 {}）及 archived_at（可空）两个增量字段，SQLite 和 PostgreSQL 使用各自既有初始化迁移机制。不新增生产依赖或服务，不改 Core 数据库迁移。

代码回退保留新增字段即可；旧版本可能再次显示已归档且已解绑的记录，但凭据仍为空。不可通过删除历史或清表回退。本次没有归档恢复入口，不操作飞书侧应用。

主要文件：

- backend/channel-gateway/channel_gateway/{app.py,common/application/providers.py,common/domain/channel.py,common/infrastructure/postgres.py,common/infrastructure/sqlite.py,feishu/accounts.py,feishu/ports.py}
- frontend/src/modules/channelGateway/{api.ts,pages/ChannelConnectionPage.tsx}
- frontend/src/modules/notifications/{RuleEditor.tsx,index.scss}、中英文通知文案
- frontend/scripts/openapi/specs/channel-gateway.yaml、生成客户端和 api/backend/task-notifications.md
- tests/backend/channel-gateway/test_account_management.py、frontend/src/modules/notifications/AccountManagement.test.tsx

## 统一验证

| 范围 | 结果 |
| --- | --- |
| 文件 SQLite：全部渠道网关 + 既有密钥安全测试 | 163 通过 |
| PostgreSQL：全部渠道网关 | 159 通过 |
| 前端通知与渠道模块 | 11 文件、58 通过 |
| Python flake8、前端 ESLint（本次影响范围） | 通过 |
| 权限提取 | PATCH 备注、POST :archive 均 qa.write |
| OpenAPI 客户端生成、Local 前端构建 | 通过 |
| 桌面构建及 macOS ad-hoc 签名 | 通过 |
| 原验收环境启动 | overall ready，15 个服务 running |
| 打包后后端源码 | 本次 7 个后端文件逐一校验一致 |
| 真实受保护访问链路 | 查询账号 200；不存在账号的备注/归档均 404 ACCOUNT_NOT_FOUND |
| git diff --check | 通过 |

新增后端测试覆盖 25 个场景，前端 7 个场景：旧数据升级与重复初始化、重启保留、历史保留、同名账号区分、引用查询失败阻止删除、输入边界、跨用户拒绝、其他渠道不支持、并发删除与授权、迟到授权回调、客户端禁止篡改展示身份。SQLite 使用独立文件；PostgreSQL 使用独立测试 schema，没有重置业务数据库。

既有安全测试由禁止所有凭据字段值出现在视图，调整为禁止真正的 token/app_secret/secret，同时继续断言不返回密文及身份哈希；公开应用 ID 与授权人 ID 是本次明确展示字段。重新授权测试同步要求解绑后仍用保留的原应用 ID，身份匹配和历史保留断言不变。

PostgreSQL 最初运行受本机无用 GSS 认证协商影响，每次连接约 1 秒；已正常中断并以测试 DSN 的 gssencmode=disable 重跑完整 159 项，14.17 秒全部通过。只调整本机隔离测试连接，未变更生产配置或测试断言。

前端定向 TypeScript 检查已修复本次新增输入框事件类型，剩余 6 个错误均为既有 authservice-client/api.ts 的未使用导入；全仓检查此前另受未修改的 chat/utils/message.test.ts:49 语法错误阻塞。没有宣称全仓类型检查通过，也未修复这些无关文件。事件类型注解为打包后补充，已核对编译后的差异仅为箭头参数的可选括号，不改变已打包行为。依赖的 6 条 Python 弃用警告保留。

测试日志：

- /tmp/lazymind-account-management-sqlite-final.log
- /tmp/lazymind-account-management-postgres-complete.log
- /tmp/lazymind-account-management-frontend-final.log
- /tmp/lazymind-account-management-python-lint.log
- /tmp/lazymind-account-management-eslint-final.log
- /tmp/lazymind-account-management-typecheck-scoped.log
- /tmp/lazymind-account-management-desktop-build.log

## 人工验收

桌面已用原验收配置启动，应用为 desktop/dist/mac-arm64/LazyMind.app，分发包为 desktop/dist/LazyMind-darwin-arm64.zip；Local 前端构建已恢复。当前 Mac 锁屏，未完成实际画面检查。未自动修改、解绑、归档真实账号，也未发送外部消息或扫码创建机器人。

解锁后：

1. 打开设置 → 连接 → 飞书，展开一条旧账号，确认“已解绑”、公开标识及“修改备注”“删除记录”入口；无法恢复的旧标识显示信息缺失。
2. 为两条同名记录分别设置不同备注，刷新页面确认保留；备注不改变账号 ID。
3. 选择确实不再需要的已解绑记录，点击“删除记录”，检查确认框中的任务/默认配置引用；取消时记录仍保留。
4. 确认删除后该记录消失，另一个账号仍存在；刷新后仍不显示。已有通知历史应保留；引用此账号的通知配置提示不可用，需主动选择有效账号，不自动切换。
5. 对一个有效机器人普通断开，再重新连接，仍无需扫码，身份不变；已连接或暂停时没有“删除记录”入口。
6. 在测试机器人上执行解除绑定，确认授权人和应用标识仍保留；重新授权后保持原身份。此步骤需要用户实际飞书授权，自动测试未替代平台实测。

需要再次启动时：

```bash
cd /Users/zouyu/Downloads/LazyMind-notifications-official
./docs/plan/notification-official-isolation/start-acceptance.command
```

未提交或推送，既有工作区修改保留。


## 解锁后的桌面人工验收（2026-09-18）

使用正在运行的 LazyMind 桌面版，通过真实设置页面操作，未使用接口或数据库替代 UI 操作。

已通过：

- 飞书列表展示两条原有已解绑记录；展开后展示授权人、应用 ID、应用内用户 ID、状态、引用及操作入口。旧记录缺失身份显示“信息缺失”，不伪造名称。
- 在当前桌面窗口下，账号详情、四个操作按钮和删除弹窗完整可见，无横向裁切；长标识能够容纳。
- 将第一条记录临时改为“验收临时备注 · 第一条机器人”，通过保存按钮提交；列表刷新后备注保留，账号 ID 不变，第二条记录未变化。
- 空白备注的保存按钮禁用。
- 点击删除入口，确认框明确说明保留聊天/通知历史、不自动改绑、不删除飞书应用，并显示实际 0 个引用；取消后两条记录均保留。
- 随后通过同一 UI 将备注恢复原值，重新加载整个桌面页面后两条原记录及原名称均确认保留。
- 消息通知页正确显示飞书未连接，没有把两条已解绑记录当作可用连接。未修改通知总开关或任务规则。

发现两处文案缺陷，尚未修正：

1. 修改备注弹窗的确认按钮显示“保存通知配置”，应改为“保存备注”或“保存”。来源：ChannelConnectionPage.tsx 的备注弹窗复用了 notifications.save。
2. 飞书记录列表标题显示“已连接账号”，实际包含两条已解绑记录，而页头统计为 0 个已连接账号；建议列表标题使用“已添加账号”或“账号记录”，保留页头在线连接数量语义。来源：notifications-zh-CN.ts 的 accounts 文案。

本轮只做验收和记录，没有修改生产代码。没有确认归档任何真实记录，也没有重新授权或创建飞书机器人。因此真实 UI 的归档提交、非零引用影响展示、有效账号身份回填和免授权重连仍未完成平台实测；相关自动化结果见上文，不能等同于人工通过。应用最后停留在飞书连接页，临时备注已恢复。


## 人工验收文案修复（2026-09-18）

用户要求修复验收发现的两处文案。备注弹窗改为复用既有 common.save，中英文分别为“保存”/“Save”，没有新增重复翻译键；中文账号列表标题改为“账号记录”，英文原有 Accounts 已符合含义，连接数量统计仍保持原语义。

仅调整两处前端生产代码/文案，并同步既有备注保存测试的按钮定位，不改保存逻辑、API、数据或权限。通知和渠道前端 11 文件 58 项测试通过，涉及文件 ESLint 通过。桌面重建、签名及 Local 前端构建通过，应用用原验收配置启动。当前 15 个服务 running，overall ready。桌面实际确认“账号记录”标题显示正确。

两条原记录的归档时间为本轮构建前，存储中仍保留，活动列表为空符合归档语义；本轮没有删除、恢复或新增真实账号。由于当前无活动账号，本轮未能在桌面重新打开备注弹窗，按钮调整由既有前端保存测试及通用中英文翻译验证，不能宣称已完成该弹窗的桌面实测。

本轮日志：/tmp/lazymind-account-copy-tests.log、/tmp/lazymind-account-copy-lint.log、/tmp/lazymind-account-copy-desktop-build.log、/tmp/lazymind-account-copy-local-build.log。
