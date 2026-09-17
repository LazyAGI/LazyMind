# macOS 启动修复记录（2026-09-17）

## 结果

独立验收包已实际打开到模型配置页面。首次安装预热完成，maintenance runtime stopped and verified；随后正常启动的 15 个服务全部 running，overallStatus=ready。未跳过预热、伪造完成标记或修改登录/权限校验。

- 修复包：`/Users/zouyu/Downloads/LazyMind-acceptance-repaired-20260917/LazyMind.app`
- 隔离验收目录：`/Users/zouyu/Downloads/LazyMind-manual-05uNt5`
- 原仓库 `desktop/dist` 包未修改；审查中的既有改动保留，未提交或推送。

## 原因与处理

1. Go 1.27 构建的 Caddy v2.10.2 加载实际配置时发生 ModuleMap panic。使用 Go 1.25.0 构建同一版本，实际 Caddyfile 校验及页面服务启动通过。
2. 之前为规避 GitHub API 403 而建议的 release 模式移除了本地 LazyLLM，但当前聊天代码依赖本地源码中的 WriterExecutionTools。验收包恢复本地 LazyLLM；其中 lazyllm/docs 是运行模块，不能按文档目录删除。源码和依赖版本未更换。
3. algorithm/sitecustomize.py 在 multiprocessing.resource_tracker 中加载 SQLite 业务补丁，间接导入 LazyLLM，再次启动资源清理进程。服务关闭后持续产生孤儿进程，导致首次安装预热失败。用户已明确批准三项回归测试及该文件最小修复；启动钩子现仅对该标准库辅助进程跳过业务补丁，普通业务进程保留原逻辑。

修复未变更 SQL、Schema、认证、通知业务接口或数据库选择。普通 SQLite 与 PostgreSQL 场景的启动钩子选择有回归覆盖；本次没有重新运行完整 PostgreSQL 集成测试。

## 验证

使用桌面包自带的 Python 3.11 运行新测试，修改前普通 SQLite 与 PostgreSQL 场景通过，资源清理进程隔离测试稳定失败；修改后全部通过。系统自带 Python 3.9 不具备 sys.orig_argv，不是本次桌面运行时，验收测试应使用包内解释器。

```sh
cd /Users/zouyu/Downloads/LazyMind-main
export LAZYMIND_ACCEPTANCE_APP=/Users/zouyu/Downloads/LazyMind-acceptance-repaired-20260917/LazyMind.app
PYTHONDONTWRITEBYTECODE=1 "$LAZYMIND_ACCEPTANCE_APP/Contents/Resources/runtime/deps/python/algorithm/bin/python" \
  tests/backend/desktop/test_python_startup_hook.py -v
```

同时运行 tests/algorithm/test_sqlite_proxy.py 和 tests/algorithm/common/test_sqlite_proxy_config.py，共 6 项通过。初次尝试使用现有 Python 3.12 测试环境收集算法回归时缺少 toml；改用桌面 Python 及其实际依赖，只借用现有测试环境中的 pytest 后通过，没有添加生产依赖。flake8 和 git diff --check 通过。

启动后再次执行 `codesign --verify --deep --strict` 通过。测试曾在包内生成额外 Python 字节码；只移除了签名清单中不存在的这批缓存，保留已签名资源。后续在包内运行 Python 检查时设置 PYTHONDONTWRITEBYTECODE=1。

验收构建包装脚本 acceptance-build-go.sh 的 shell 语法及 5 项命令转发检查通过；通过包装脚本真实打包 29 个内置 Skill、26 个精选能力成功。包装脚本保留非 release 模式，仅对 Caddy 和 Skill 使用 Go 1.25.0，其他构建允许 auto 工具链，避免阻塞要求 Go 1.26 的 process-compose。

完整修正后的 make 构建尚未重新执行；本次实际启动验证针对独立修复包。正式发布包仍需使用修正流程重新构建、签名并验收。本次没有执行真实模型任务或向外部渠道发送通知。

## 继续当前人工验收

应用已启动，不必再次执行启动命令。在原终端更新包路径，后续 m / nreq 继续使用原隔离目录：

```sh
export LAZYMIND_ACCEPTANCE_ROOT=/Users/zouyu/Downloads/LazyMind-manual-05uNt5
export LAZYMIND_ACCEPTANCE_APP=/Users/zouyu/Downloads/LazyMind-acceptance-repaired-20260917/LazyMind.app
export LAZYMIND_ACCEPTANCE_BIN="$LAZYMIND_ACCEPTANCE_APP/Contents/Resources/runtime/bin/lazymind"
```

先完成模型配置，再按完整人工验收手册继续检查任务与通知。应用正常启动不代表所有人工验收项已通过。
