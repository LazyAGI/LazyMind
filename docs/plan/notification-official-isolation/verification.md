# 官方基础验证记录

日期：2026-09-17。目录：`/Users/zouyu/Downloads/LazyMind-notifications-official`。

以下是本次在官方 35ea7181 上重新执行的结果，不沿用旧工作区分支的通过数字。

| 范围 | 结果 |
| --- | --- |
| Core 根包、chat、workflow、taskcenter、scheduler；文件 SQLite，设置跨服务 Python 环境 | 全部通过 |
| Core 上述五包及 migrate；PostgreSQL 独立测试数据库 | 全部通过 |
| Gateway 全套、安全、权限提取 | SQLite/PostgreSQL 各 92 项通过 |
| Core 通知审查回归 race | SQLite/PostgreSQL 的 chat、taskcenter 通过 |
| Desktop 全套 | 190 项通过，0 失败、0 跳过 |
| local/lazymind-cli | 全模块 race 通过 |
| local/local-runtime-manager | 全模块通过 |
| Python 启动 hook | 3 项通过；普通 SQLite、PostgreSQL、resource_tracker |
| 前端通知与受影响回归 | 7 文件/25 项通过；1 项官方设置页 mock 既有失败 |
| 前端修改文件 ESLint、网关修改文件 flake8、Go 格式 | 通过 |
| 四组 OpenAPI 客户端一致性 | fresh |
| Vite 生产构建 | 通过，保留既有大包提示 |
| SQLite migrate | 通知 aggregate 路径既有失败；其他所选迁移测试通过 |

Core PostgreSQL 全量运行保留 4 项既有环境条件跳过：Organizer 浏览器夹具、真实模型、Organizer 产品路径、Redis。通知两库联调设置了真实网关测试 Python，没有用该环境跳过代替通过。

## 官方基线失败

详见 baseline-failures.md 的纯官方复现及修复提案。全量 TypeScript 在官方及本次均报 `src/modules/chat/utils/message.test.ts:49` 的两处 TS1128 语法错误，阻塞完整类型检查；不能据此宣称其余类型检查全部通过。此无关文件未修改。

## 环境与日志

Node 24.19.0；Go 自动工具链；网关测试使用现有 `/tmp/lazymind-notification-review-fixes-venv`。PostgreSQL 使用原专用测试容器，连接凭据仅在子进程环境传递，每项测试隔离 Schema/数据库；未清理用户应用数据库。

日志统一在 `/tmp/notifications-official-*.log`：core-sqlite-e2e、core-postgres（Go JSON）、gateway-sqlite、gateway-postgres、race-sqlite、race-postgres、desktop、cli、runtime、python-startup、frontend-tests、frontend-lint、python-lint、frontend-build、openapi。官方对照目录 `/tmp/lazymind-official-baseline-20260917` 只用于对照，增加了一份临时 SQLite aggregate 复现测试，没有生产修改。

## 桌面构建验证

完整 `make desktop-darwin-arm64` 已成功退出 0，产出 `desktop/dist/mac-arm64/LazyMind.app` 和 `desktop/dist/LazyMind-darwin-arm64.zip`（约 793 MiB）。使用既有工具链 wrapper、冻结 Skill 锁文件、Node 24、RELEASE_BUILD=false 和 adhoc 签名。签名验证通过，包内前端、启动 hook、aggregate 与源码一致，不含 backend/core/localworkspace；本地 LazyLLM 文档保留，包内 Python 3 项启动回归通过。构建未改动锁文件。

用户最终决定只做分支相关改动，不实施额外官方基线修复。生产源码未再改变，上述包保留同一验证状态。未替换用户当前运行的旧应用，也未使用旧工作区数据库启动此包。

## 交付边界

官方基线两项失败按用户要求保留，不再阻塞分支隔离。新分支已上传、旧分支已删除，详见 branch-delivery.md。真实三平台消息送达、Windows 真机和完整 Compose 栈仍未验收。
