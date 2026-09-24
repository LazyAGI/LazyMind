# Mac ARM64 固定复用已发布 RAG 组件

日期：2026-09-23。基于 origin/cst/installer_opt 的 fa6e7094，沿用当天 Windows 固定发布包方案，替换 Mac ARM64 每次动态生成 ZIP 的流程。

## 固定资源与配套环境

- 文件：`lazymind-python-rag-darwin-arm64-cp311-53a1c2e770966b71.zip`。
- URL：`https://modelscope.cn/datasets/CarlosShaoting/lazymind-cst/resolve/master/lazymind-python-rag-darwin-arm64-cp311-53a1c2e770966b71.zip`。
- 大小：54,515,729 字节；SHA-256：`f90b5c00b43943b031d698fc939c81b24d77a738e357bb541fe74d7e768fb8d1`。
- CPython 3.11.15，darwin/arm64。`darwin-arm64-requirements.lock` 从原配套的完整算法环境导出 173 项固定版本。导出前按原算法重新计算全部版本和 RECORD 指纹，结果为 `9e9f17cbea297a15b75a92decdd07663c6c8671d832bff8499c5f762002e633b`，与发布清单一致。

## 构建行为

1. `make desktop-darwin-arm64` 默认后置模式联合安装固定锁与当前业务 requirements；冲突立即失败，不运行 `lazyllm install rag` 动态选版本。
2. Electron 封装时将 runtime 暂移出未完成签名的 app；调用共用 `stage-published-python-components.py`，校验原生平台、架构、Python 版本、完整依赖集合与版本、分组边界。
3. 从固定 URL 下载或读取校验过的构建缓存，验证 ZIP 大小、SHA、manifest、安全解压。移出 optional 文件后实际运行基础与 overlay 导入，失败恢复文件，成功才启用原发布清单。
4. 恢复 runtime 后完成签名。缓存和验证 overlay 不进入安装包；应用首次安装 RAG 从固定清单下载，保留运行时完整校验与失败重试。
5. 不生成新 RAG ZIP、不要求重新上传。将来升级锁定依赖须显式发布并验证新的配套组件，不能跳过校验或只改文件名。

Intel 保留其现有原生构建流程；不能复用 ARM64 文件。Windows 原固定清单、锁和构建入口不变。未修改 Skill、LazyLLM 源码或子模块 gitlink。

## 本地构建与验收

```bash
LAZYMIND_DESKTOP_MAC_ARCH=arm64 \
LAZYMIND_DESKTOP_SHARE_PYTHON=false \
LAZYMIND_DESKTOP_DEFER_PYTHON=true \
LAZYMIND_DESKTOP_SIGNING_MODE=adhoc \
make desktop-darwin-arm64
```

输出 `desktop/dist/LazyMind-darwin-arm64.zip`。这是本地 ad-hoc 测试包；未做 Developer ID 公证。固定锁中的 scipy 原生 wheel 要求 macOS 14+，不声明支持更旧系统。应用内登录、聊天、知识库业务由用户安装测试。

已验证：从 ModelScope 实际下载固定 ZIP，大小/SHA 与清单一致；固定组件单元测试 8 项通过（包含 Mac 架构拒绝与导入失败恢复），桌面构建测试 42 项通过。本机原生 ARM64 完整构建成功，最终 `.app` 的 6 项验证通过：runtime 兼容、精简基础导入、已下载云端 ZIP 的 SHA、manifest/解压、RAG overlay 导入、Milvus 写入/flush/重启检索/删除。验证后 `codesign --verify --deep --strict` 通过，ZIP 内清单与仓库固定清单完全一致，ZIP CRC 完整性检查通过。

测试包：`desktop/dist/LazyMind-darwin-arm64.zip`，655,098,286 字节（624.75 MiB）；SHA-256 `78dc85f0735be73e1d955b86388d9195402c37a609861c20088e6db666766fbb`。报告：`desktop/dist/component-check/darwin-arm64/fixed-published-report.json`。本次未启用 Python 跨环境共享；已上传 RAG ZIP 无需替换。最终精简 runtime 为 1262.70 MiB。

当前分支另一项资源 `lazymind-pdf-NotoSansSC-a3041811a78c361b.ttf` 的云端地址实测 HTTP 404；首次 PDF 导出可能失败。这不影响 RAG 固定 ZIP 的下载校验，本次未更改 PDF 字体机制，也未上传资源。


## 安装试用后的启动和重启修复（2026-09-23）

本机原开发分支的 SQLite 迁移版本比 installer_opt 新，Core 拒绝读取未知迁移，Auth 同样找不到已有 Alembic revision。用户明确要求清空数据后，新库启动成功；未放宽数据库迁移校验。这不能视为新版数据向旧版降级兼容通过。

安装 RAG 后重启曾失败：关闭阶段不断发现新辅助进程，清理超时，后续 up 未执行。桌面端现在在 down 期间暂停 Agent host 自动拉起和状态探测，合并重复重启请求；解绑旧 monitor 的 close 回调并核对进程身份，防止旧退出事件清空新进程状态。错误日志包含 down 的输出。

同时 Python resource_tracker 会继承 PYTHONPATH 并执行 sitecustomize，加载 LazyLLM 时可能再次创建 multiprocessing 资源、递归启动 tracker。现在仅对 stdlib resource_tracker 启动命令跳过应用数据库 hook，普通业务进程仍安装 hook。

下载弹窗在下载中不再允许遮罩点击、Esc 和右上角关闭触发取消，仅保留明确的“取消下载”；请求结束后重新读取服务端状态。Core 新增下载开始、安装完成、取消、失败和已安装复用日志，旧版本日志无法追溯之前的下载尝试次数。

本轮单元验证：桌面构建/重启测试 44 项、前端组件测试 6 项、resource_tracker 回归测试及 Go PythonComponent 测试通过。原生 UI 实测通过：在未启用组件的服务运行期间恢复已下载的固定组件标记，以复现安装完成待重启状态；实际点击“重启本地服务”，16:56:00 日志记录 runtime restart completed，界面从“等待重启”切换为“已启用”，文档解析、Milvus 等服务全部 ready。测试未重新下载 RAG，也未清空当前用户数据。打包原件及更新到测试目录时签名验证通过；实际启动后发现现有 PPT 依赖初始化向 app 内创建 export_pptx/node_modules 链接，导致运行副本的签名失效。原始 ZIP/未启动打包原件签名仍有效；此启动写入问题待修复，不能声明启动后签名通过。

本轮更新 ZIP：655,099,047 字节，SHA-256 `3ebcb53d5fb08b2c2ce21f04f15e7743edb1b8c3a66d5f83d231d74fbe10aa8a`。`desktop/dist/LazyMind.app`（用户此前解压的路径）已更新到修复版，旧应用移入废纸篓。


## 同事交接与用户反馈（2026-09-23）

用户反馈当前 ARM64 测试过的场景均无问题。本记录保留具体自动验证和已知签名/资源边界，不将这条反馈扩大为 Intel、全部 macOS 版本或公证发行验收。

另一台 M 系列 Mac 直接使用已有固定包构建；Intel 首次发布、保存完整锁、上传、接入固定清单及二次构建验收，统一按 [Intel Mac 打包与固定 RAG 组件交接](macos-intel-packaging-handoff.md) 执行。Intel 当前还走动态分包，交接文档列出了待接入的三处代码，不能只添加 URL 就宣布固定完成。
