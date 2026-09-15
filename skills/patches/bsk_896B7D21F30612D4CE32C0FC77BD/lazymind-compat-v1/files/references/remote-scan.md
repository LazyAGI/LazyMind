# 可选远程扫描

Snyk Agent Scan 官方说明：https://github.com/snyk/agent-scan

服务需要运行环境中的 `SNYK_TOKEN`，不要将密钥写入提示词、示例或报告。远程扫描会把待扫描的公开 Skill 内容传给 Snyk；不要上传包含私有数据的目录。

用户明确需要远程扫描且凭据具备时，可在具备 shell 执行能力的环境使用：
```sh
uvx snyk-agent-scan@0.5.17 /absolute/path/to/staged/SKILL.md
```

当前 LazyMind 只允许运行包内脚本时，不要把 shell 命令伪装成 run_script 参数；说明还需接入专用远程扫描脚本。缺少工具、凭据或网络时保留 not_tested/blocked，不降低为安全。

旧脚本使用输出关键词和退出码判定安全并自动安装，这种方式容易将说明文字判为风险、或将不完整扫描误判为通过。CLI 输出是实验性协议，远程扫描结果应人工核对扫描覆盖率、认证错误和实际发现，不能单凭退出码放行。
