# Bug Reproduction Brief：外部执行、Workflow 整理

这个案例已完成真实缺陷复现和六步报告生成，交付[审阅后的复现简报](result.md)。两次测试都退出 1，stderr 一致，均为预期的 `expected 80, actual -1900` 断言；这表示缺陷复现成功，而不是 Workflow 失败。

```sh
python3 examples/bug-reproduction/prepare_request.py --output /tmp/bug-example
```

先审阅小型合成测试。脚本只在临时目录执行该测试两次，保留退出码和完整输出，校验固定上游来源及许可，再构造新转换请求。后续 API 命令、版本、Session 和验收步骤见[英文说明](README.md)。

本次真实运行复用了已由模型生成的固定 Revision，六步均首次成功，最终 Artifact 经事实审阅、编辑及回读。复现脚本请求重新自动转换，因此新计划可能不同。

执行边界：两次命令由 Codex 外部执行器完成，LazyMind 消费绑定证据并整理报告；不宣称 LazyMind 内置沙箱执行。已有脚本工具在宿主进程加载，不能当作受限命令沙箱。本例没有放宽权限、修改 Runtime 或新增执行引擎。原始日志和模型草稿不进入 Git。
