# 2026-10-26 report 补测证据

结论见 [开发报告](../../development/report.md) 最后一节。本目录为本次实测证据，不是新增 CI 测试套件。

- `python.log` / `manager.log` / `proxy.log` / `core.log` / `frontend.log`：已有回归的本次结果。
- `runner-current-head.log`：HEAD 更新为 582e7bce3 后重跑受影响范围。
- `probes.py` / `queue_crash.py` / `extra.py` / `http_probe.py` / `office.py`：隔离临时目录诊断脚本；对应 JSON 保存观测结果。脚本默认把结果写入 `/tmp/report-retest-1026`，重跑前须创建此目录。
- `manager_probe_test.go.txt`：放到 local/local-runtime-manager 临时 `_test.go` 文件后 `go test -run '^TestReport' -count=1 -v`。
- `auth_probe_test.go.txt`：放到 local/local-proxy/internal/auth 临时 `_test.go` 文件后执行该包 TestReport。
- `frontend_probe.test.ts.txt`：放到 frontend/src/runtime 临时 `.test.ts` 后由 frontend 的 Vitest 执行。

上述临时诊断文件已从源码测试目录移除，未修改既有断言或 doc_check。

Python 脚本在仓库根目录执行，使用 `PYTHONPATH=algorithm:algorithm/lazyllm local/build/deps/python/algorithm/bin/python`。Office 脚本还需本机已安装的 soffice，设置 `OFFICE_CONVERT_TRANSLATION_FONT_PATH` 指向有效字体、`OFFICE_CONVERT_TIMEOUT_SECONDS=45`。本次字体为 macOS 系统 Arial Unicode.ttf，脚本仅在临时目录增加 libreoffice 命令包装；这不是生产部署修改。默认环境导入服务会因缺少默认 CJK 字体候选而失败，详见报告。

没有调用真实模型，没有修改真实密码，没有进行 Windows 实机测试。报告中的“缺陷复现”不能因为诊断脚本以 0 退出就计为缺陷修复。
