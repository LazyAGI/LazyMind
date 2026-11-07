# Performance Report：带审阅的月度报告

四行合成渠道数据，经真实模型转换为六步 Workflow，交付[可读报告](result.md)。来源、许可、固定版本和完整命令见 [英文说明](README.md)；不暗示上游认可。

本次六步全部完成：第五步从供应方 402 在同一 Session 内重试恢复，第六步通过公开接口启动；最终 Task 为 succeeded，Session 完成。报告经事实审阅和 Artifact 编辑后回读一致。独立计算验证了 34 行、136 个数值，包括 CPM 差值 0.95。

```sh
python3 examples/performance-report/prepare_request.py --output /tmp/performance-example
python3 examples/performance-report/check_metrics.py
```

准备脚本校验固定来源并生成 ZIP 和新转换请求，不手写 Workflow。按英文说明提交至隔离实例，再检查新生成的输入 ID、状态和实际输出。模型转换可能产生不同计划；验收脚本校验报告附录的数字，不能替代正文审阅。

这是有人参与的成功案例：模型原稿的 Social 收入、指标方向和实验状态表述需要修订。未验证审批，不宣称无人介入即可保证事实正确，也未执行真实营销操作。原始日志不进入仓库。
