# 验收清单

- [x] Git bundle、二进制 patch、untracked 归档和哈希清单已保存。
- [x] 新分支从官方 35ea7181 建立。
- [x] 排除工作区审批测试与桌面重新授权修复。
- [x] 39 个通知新增生产/测试文件与已审查来源逐字节相同。
- [x] 新增 diff 没有 localworkspace/LocalWorkspace/local_workspace/localExecution 引用。
- [x] SQLite Core 五个业务包及跨服务通知联调通过。
- [x] PostgreSQL Core 六个相关包含 migrate 通过。
- [x] Gateway 两数据库各 92 项通过。
- [x] Desktop 190 项通过，无跳过。
- [x] CLI race、本地运行管理器、Python 启动三项通过。
- [x] 前端生产构建、修改文件 lint、OpenAPI 一致性通过。
- [x] 用户明确排除官方既有 SQLite aggregate 与设置页 mock 修复，保留原样并记录失败。
- [x] 最终两数据库迁移、并发及前端结果记录完整，既有失败明确披露。
- [x] 官方基础候选 macOS 包重建、签名与资源核验通过。
- [x] 最终决定不做额外兼容修复；生产源码与已核验桌面包保持一致。
- [ ] 新功能分支提交、上传并校验官方祖先。
- [ ] 新交付完成后删除旧通知分支，保留原始备份和其他分支。

真实平台账号发送、Windows 真机及完整 Compose 验收沿用此前未验证边界。全量 TypeScript 被官方 message.test.ts 的两处语法错误阻塞；纯官方副本同样失败，未为消除基线错误修改无关聊天测试。
