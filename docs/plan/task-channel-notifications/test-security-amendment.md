# 投递来源校验：获准执行的测试补充

阶段二发现首批网关 Fixture 的缺口：测试构造 `task_id=scheduled-run` 等示例标识，却没有提供对应的 Core 事件。若生产接口直接接受这些请求，具有普通写权限的客户端即可把任意正文伪装成任务结果。单独校验渠道账号所有权不足以证明任务通知真实存在。

拟调整仅涉及以下已批准测试辅助代码，原有断言全部保留：

1. `tests/backend/channel-gateway/conftest.py` 增加内存中的 **Core 网络边界 Fake**。它保存测试创建的规范通知事件，模拟现有任务通知查询及用户通知总开关；未知事件返回空列表，未知请求直接报错。Core/网关真实 Store、处理器、账号服务和 DeliveryWorker 不替换。
2. `test_notification_delivery.py` 的 `enqueue` 辅助方法在请求前登记规范 Core 事件。真实生产接收接口仍需向 Core 查询校验，再执行原 Outbox 入队。正常测试使用的任务标识不再等同于“无需校验来源”。
3. 新增安全用例，直接发送未经登记或篡改的请求，不能调用会登记事件的 `enqueue` 辅助方法。

辅助代码变化示意：

```python
def enqueue(gateway, payload):
    # Represents a previously committed Core event at the external service boundary.
    gateway.core_events.publish(payload, owner='owner')
    response = gateway.client.post(f'{PREFIX}/task-notifications', json=payload)
    # Existing success, identity and subsequent storage/delivery assertions remain unchanged.
```

新增断言：

| 用例 | 预期 |
| --- | --- |
| 未登记任务事件 | 拒绝，Outbox 不新增记录 |
| 已登记事件正文或标题被篡改 | 拒绝，错误不返回原正文 |
| 已登记事件的账号、收件人、平台或执行标识被替换 | 拒绝，不产生跨目标投递 |
| 其他用户的事件 | 拒绝，不能读取或发送该用户通知 |
| Core 查询超时或拒绝认证 | 安全失败，不能凭客户端正文继续发送 |
| 入队后总开关关闭 | Worker 不发送并记录跳过，再开启不补发 |
| 合法事件重放 | 保留现有唯一通知/Outbox 断言 |

生产实现将复用 Core 已有任务通知查询、网关 `LazyMindClient` 和集中服务认证。Fake 只模拟网络响应，不伪造生产依赖或关闭来源校验。已有测试请求中没有真实 Secret，也不增加真实平台发送。

2026-09-15 执行记录：用户在提出本具体补充方案后回复“继续工作”，按该方案补齐 CoreEvents 网络边界 Fixture、enqueue 辅助登记及新增安全断言，原有断言保持不变。两库均执行通过，具体结果见 implementation-report.md。
