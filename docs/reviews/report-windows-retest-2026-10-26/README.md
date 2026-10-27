# Windows 原生补测（2026-10-26）

主仓库 `7154e8451`（另加本轮 Windows 启动脚本修复及测试），LazyLLM `abf630b80681d34f5e645368f23d5045e312fcae`。没有修改或提交子模块指针。

## Manager 原生测试

在 WSL 交叉编译 `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c`，由 Windows PowerShell 执行生成的 Windows EXE，参数 `-test.v -test.timeout=180s`。测试产生的临时文件位于 Windows 临时目录，源码从 WSL share 读取；这不代表在 NTFS 完成源码冷构建或整套部署。

输出：`manager-windows.txt`。主包顶层用例 197 PASS、4 SKIP、0 FAIL（不将子用例重复计数）。4 项跳过分别是：未开启外部发布包网络验证、8090 已被现有服务占用、Windows PATH 无 sleep 命令、Unix chmod 专用测试。包含真实 PowerShell 配置解析、Windows 文件锁错误、子进程 Job 分配和已有环境保护等测试；部分测试使用模拟 runner，不能替代真实安装。

## 配置编码

Windows PowerShell 5.1.26100.9711，默认代码页 936；NTFS 临时 UTF-8 无 BOM 配置含中文注释、中文值、注释后的 ASCII 键。调用实际 `Import-EnvFile`，修复前中文值不保留、ASCII 键正常；显式 UTF8 读取后两者正常。永久回归：`local/local-runtime-manager/windows_env_test.go`，由 manager 的 Windows `go test ./...` 收集。

## Doctor

移入 `cst/fix_1026` 已验证但当前分支缺失的 Go module 读取诊断。同一个实际 manager go.mod 在 NTFS 临时目录通过；在当前 WSL share 上失败时给出 Windows 本地盘或 WSL local-up 的可操作提示。不会将这个环境限制当成 Windows 全平台故障。

## 迁移与实际服务

- `migration-windows.txt` / `migration-docker.txt`：真实迁移进程中断、回滚及重启。
- `auth-regressions.txt`：225 passed、27 subtests passed。
- `migration-postgres.txt`：无外网的临时 PostgreSQL 16 容器，真实 upgrade head、重复 upgrade、版本读取成功；测试容器已移除。
- `native-health.json`：独立 Windows 认证库重建后的实际健康检查，仅保存状态，不含会话令牌。
- `frontend-memory-failure.txt`：限制 2 GiB 堆后，真实 Windows 前端构建的明确堆内存耗尽错误；这不是 pnpm 缺包错误。

首次 NTFS 冷启动已完成 manager/Core/proxy 等 Go 编译、Pandoc 与 Skill catalog 准备、原生受管 Python/venv 创建及前端依赖安装。Python 下载较慢，停止首轮启动前实际安装目录已出现；另外经 WSL 下载了同版本官方安装包并配置 uv 的 file:// 镜像备用，因此不把重跑过程标为纯粹 Windows 冷下载验证。完整运行受前端构建内存和 RAG pip MemoryError 阻塞，不标记整套部署成功。

入库日志统一为 LF 并去除行尾空白，测试结果与错误内容未改动。
