# 飞书接收对象为空修复（2026-09-18）

现场已连接账号运行正常，已收到一条消息，但 channel_notification_targets 中没有对象。原因是飞书运行器使用 claim_feishu_workspace_and_ingest，而通知登记只接入了通用 ingest_batch。

修复复用同一个事务内登记方法，将飞书工作区成功入库路径接入；登记前核对账号、平台及所有者。失败的工作区版本校验和重复消息不新增对象。既有共享初始化流程从同账号、同所有者的已收到飞书消息补齐历史遗漏，限定连接中且凭据存在的账号，不复活归档记录、不变更通知规则、不重新发送历史消息。

没有新增 Schema、API、生产依赖或架构层。修改共享 Gateway 存储，SQLite 继续复用已有适配器。新建 test_feishu_notification_targets.py 的 7 个场景覆盖真实入库方法、可选对象及上下文、幂等、跨用户查询拒绝、伪造入库身份拒绝、工作区版本冲突、双库升级/重复初始化/历史保留，以及其他平台不回填。

验证：SQLite 全网关加安全测试 170 通过；PostgreSQL 全网关 166 通过；相关 flake8 通过。日志 /tmp/lazymind-feishu-target-{sqlite,postgres,lint}.log。未重复无关前端测试，前端代码未改。

已用同一受测初始化方法为当前验收库补齐 1 个已有接收对象，未直接写入臆造 ID、未修改用户配置、未对外发送消息。桌面包已重建，签名通过；打包后的修改文件与源码一致。原验收环境启动后 overall ready，15 个服务 running，Local 前端构建已恢复。

桌面实际验收：回到“浏览器通知验收”的通知配置，恢复原草稿的成功/失败结果摘要、桌面和飞书渠道开启状态及原发送账号。下拉框已显示来自既有消息的真实接收对象，点击后在选择框和配置摘要中均显示选中值。停留在已选接收对象的草稿状态，没有点击保存通知配置，没有触发任务或发送消息。

构建日志：/tmp/lazymind-feishu-target-desktop-build.log、/tmp/lazymind-feishu-target-local-build.log。未提交或推送。
