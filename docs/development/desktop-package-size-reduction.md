# 桌面安装包瘦身与能力按需安装

状态：**构建裁剪、RAG 后置及案例 warmup 下载已实现；Windows CI 已产出 448.30 MiB 安装包，Mac 本地组件验证已通过，完整业务回归仍待完成**。修改日期：2026-09-20。云端依赖清单保持原样；用户自行上传组件文件到 ModelScope，不由开发脚本发布到云。

在 Mac 上自行打包并上传，请直接按 [Mac 构建、上传与安装验证流程](../../desktop/INSTALL.zh-CN.md) 执行；其中使用最终 `.app` 内的精简环境验证本地及云端 RAG 组件。

**术语澄清：ModelScope 是依赖包托管位置，不是 DashScope SDK。** 本轮保持 DashScope SDK 随基础包安装，不改变通义功能的依赖方式。此前 Linux 测得 SDK 加独占依赖压缩仅约 4.6 MiB，单独后置收益很小，已撤回该拆分。

## 最新变更：Windows 与 Mac ARM64 固定复用已发布 RAG

2026-09-23 起，Windows 后置模式使用仓库固定的 ModelScope `e262c0d2f05fe09d` 组件与配套依赖锁，不再每次构建生成新 ZIP；组件安装界面/API 移除自定义 URL，只展示清单下载来源。旧安装包需要升级才能采用新清单。详见 [固定版本开发记录](windows-published-rag.md)；下文旧的 Windows 动态分包/自定义链接说明属于历史实现，同日 Mac ARM64 已接入固定 `53a1c2e770966b71` 包与 173 项配套依赖锁，见 [Mac 固定版本记录](macos-published-rag.md)。Intel 保留原流程。

## 第二轮实施记录

用户已确认继续考虑五项优化：Windows 排除 Mac 开发二进制、排除 LazyLLM 文档、解释器别名去重、相同 Python 依赖共享、PDF 字体后置。**执行端已实现五项代码及 Intel 原生构建入口，ARM64 已构建；Windows/Intel 原生验收仍待完成。** 具体的“做什么、为什么、怎么做”、Windows x64 / Mac ARM64 / Mac Intel 的适配与验收见 [第二轮跨电脑交接计划](desktop-package-size-next-phase.md)。现有已完成状态与下面历史记录保持不变。

## 本轮修改范围

**不修改 Skill 代码、内容包、素材或安装流程**。NumPy、SciPy、pandas、scikit-learn、UMAP、PyMuPDF、Office 等共用依赖继续留在基础包。科学计算库仅清理 tests/可重建字节码，不删除运行时算法或数据。

| 位置 | 实际改动 |
| --- | --- |
| `desktop/scripts/prune-python-runtime.py` | 按 distribution RECORD 和引用闭包裁剪无关火山服务，保留 Ark runtime、Ark 管理 API、core；清理指定库的测试文件、可重建字节码，生成大小报告；Ark 图片/视频 HTTP mock 验证 |
| `desktop/scripts/build-python-components.py` | 从同一次解析得到的 algorithm 环境拆出 RAG 专用依赖及其独占传递依赖；共享依赖留在基础包；产出 ZIP、catalog、SHA256SUMS 后移出原环境 |
| 两端 build 脚本、`desktop/electron/electron-builder.config.cjs` | 默认精简打包；Windows 在封装 Python payload 前拆包；macOS Developer ID 模式先签组件原生库，再拆包，最后签/封装主应用，安装组件不修改已签名 `.app` |
| 两个 installer workflows | `prune_python`、`defer_python` 默认 true；上传独立组件附件和裁剪报告；读取可选仓库变量 `LAZYMIND_PYTHON_COMPONENT_BASE_URL`；修复 Windows 摘要中的 PowerShell 字面表达式 |
| `local/local-runtime-manager/{python_components,config,process_plan,algorithm_service,core_service}.go` | 根据 catalog 和兼容安装标记加载组件；缺 RAG 时不启动/等待 Milvus、解析、processor、doc-server、扫描和文件监听；保留 Chat/Core 等基础服务 |
| `algorithm/sitecustomize.py`、SQLite hook、runtime loader、knowledge search | 加载组件目录及 `.pth`；无 RAG 时 SqlManager 兼容 hook 不导入 RAG store；普通 Chat 独立启动；RAG 工具缺组件时返回安装提示，避免提前导入 |
| `backend/core/systemdeps/python_components.go`、routes/main | 本地组件状态/安装 API；HTTPS 下载、大小/SHA 校验、ZIP 路径校验、导入检查、取消、安装锁、原子激活；知识库操作给出缺组件提示，已有相关异步任务暂不消费 |
| 设置依赖页、知识库页 | 展示 RAG 下载量、安装状态；输入云端 ZIP 地址、下载、失败重试、取消；安装后明确点击“重启本地服务”；知识库页有安装入口 |

**未实施：** Ark 纯 HTTP 重写、Skill/聚类/reader/渠道整体后置、组件卸载、离线 ZIP 导入、断点续传、百分比下载进度、自动修复磁盘损坏。Ark 仍用原 SDK，但不再随包携带其大量无关云服务。基础 requirements 保留用于构建/云端；构建机仍完整解析依赖，用户端不运行 pip。

**LazyLLM：没有修改源码、没有创建新分支、没有提交或暂存 submodule 指针。** 工作区操作前就存在主仓库 gitlink 与子模块检出版本不一致；已保留。若后续确需改 LazyLLM，须先在其仓库执行 `git switch -c cst/installer_opt`，不能把子模块指针提交到 LazyMind。本次无需另发 LazyLLM。

## 组件文件在哪里，以及如何上传

构建对应平台的 installer 时自动产生以下文件；不需要额外手工构建依赖：

| 平台 | 构建目录 | Actions 附件名 |
| --- | --- | --- |
| Windows x64 | `desktop/dist/python-components/windows-amd64/` | `windows-python-components` |
| macOS arm64 | `desktop/dist/python-components/darwin-arm64/` | `macos-python-components` |
| 本机 Linux 验证样本 | `desktop/dist/python-components/linux-amd64/` | 本地产物，仅用于验证，不可安装到 Windows/macOS |

目录中有 `lazymind-python-rag-<平台>-<架构>-cp311-<revision>.zip`、`python-components.json`、`SHA256SUMS`。**请上传对应平台的原始 RAG ZIP，保持文件名和文件内容不变，不要上传 Actions 的外层总 ZIP 作为组件。** Catalog/校验文件建议一起留档。目录属于忽略的构建输出，不要提交到 git。

现有 FFmpeg 后置安装在 `backend/core/systemdeps/ffmpeg.go:364` 定义同一托管目录：

```text
https://modelscope.cn/datasets/CarlosShaoting/lazymind-cst/resolve/master/
```

Windows FFmpeg 现有完整链接为 `https://modelscope.cn/datasets/CarlosShaoting/lazymind-cst/resolve/master/lazymind-ffmpeg-windows-x64-20260803.zip`。本轮只读取并复用其 URL 方式，不改变 FFmpeg 安装逻辑。

1. 新 RAG 包默认也使用上述 ModelScope 目录。将生成的 RAG ZIP **原名上传到 `CarlosShaoting/lazymind-cst` 数据集的 master 分支根目录**，应用中的默认链接就是该目录加生成文件名；上传后无需再次改代码或重打 installer。尚未上传时链接会 404，属于资源尚未发布。
2. 若上传到其他位置，在应用“设置 → 系统工具 → 依赖”粘贴完整 HTTPS 下载地址即可。也可在 GitHub Variables 或本地构建环境设 `LAZYMIND_PYTHON_COMPONENT_BASE_URL`，后续构建改用指定目录。不设/空值默认复用 FFmpeg 的 ModelScope 地址。
3. 地址必须直接返回 ZIP 内容；需要登录的网页链接不可用，可以使用完整的 HTTPS 签名下载链接。按目标平台上传对应文件，不能混用。
4. 安装完成后点击“重启本地服务”，或退出重开应用。重启会中断在途任务，安装过程不会自动重启。
5. 组件安装到用户 runtime 的 `deps/python-components/<id>/<revision>/site-packages`。既有会话、原始文档、知识库/索引数据不迁移、不删除。

兼容规则：组件绑定同一构建的完整依赖 fingerprint、平台、架构、Python ABI 和文件 SHA。外置 RAG 与基础包之间的 distribution 不重叠；共享依赖保留基础包，避免覆盖版本。版本/平台不符或 ZIP 损坏直接拒绝，失败不替换已安装版本。升级后如果 fingerprint 不同，需要安装新版本匹配的组件；旧组件目录和数据保留。新建干净 runtime 时不会自动继承其他 runtime 已装组件。

## 2026-09-20 本机构建产物

本机通过 WSL 调用宿主 Windows 的 CPython **3.11.15 x64**，按 Windows installer 相同的 algorithm 依赖安装顺序、裁剪和拆包流程，构建了 Windows RAG 组件；Linux 沿用先前实际构建并通过运行验证的组件，本次重新校验其 SHA-256 和 ZIP 完整性。**本次没有构建完整 EXE/DMG。**

| 平台 | 本地文件（相对仓库根目录） | ZIP 大小 | 状态 |
| --- | --- | ---: | --- |
| Windows x64 | `desktop/dist/python-components/windows-amd64/lazymind-python-rag-windows-amd64-cp311-e262c0d2f05fe09d.zip` | 69,342,263 字节 / **66.13 MiB** | 构建及组件安装通过；Milvus 刷新缺陷见下文 |
| Linux x64 | `desktop/dist/python-components/linux-amd64/lazymind-python-rag-linux-amd64-cp311-c85a4604f3a60515.zip` | 93,851,208 字节 / **89.50 MiB** | 既有产物复核通过 |
| macOS ARM64 | 尚无产物 | — | 缺少 Mac 构建/签名环境；推送后由现有 macOS installer workflow 生成 `macos-python-components` 附件 |

Windows SHA-256：`258944a85d5aa29c0eb662ab21888c5c2894fdfb2c503d51f2129efa4e083bed`；Linux SHA-256：`aec8c99581592a2cf6907adfeb6b2da330a80294c5c9ca358ee7533a096bd1d9`。各自目录包含 catalog 和 `SHA256SUMS`，Windows 另含裁剪报告及构建日志。用户已将上述 Windows ZIP 上传到默认 ModelScope 目录；2026-09-20 实际下载返回 HTTP 200，69,342,263 字节及 SHA-256 均与本地 catalog 一致。用户本次不发布 Linux，因此无需上传 Linux 组件。当前已上传的是本机构建组件，后续仍须与 GitHub installer 同次构建的 catalog 核对。

Windows 此次仅统计 algorithm 环境：裁剪前 1,242,114,616 字节，裁剪后 911,266,129 字节；随后 RAG 拆出 271,444,467 字节（258.87 MiB）。不含 Python 解释器、其他服务和桌面资源，不能作为完整安装包体积。

**正式发布请上传与 installer 同一次 GitHub 构建产出的组件。** 本地组件绑定本次环境的依赖 fingerprint，不能保证与随后 CI 重新解析/安装依赖生成的 catalog 一致；即使平台相同、文件能下载，校验不匹配也会拒绝安装。macOS 的正式 Developer ID 组件还需要原生签名，不能拿 Linux/Windows 包改名替代。

本次原生构建还修正了拆包脚本的 Windows 8.3 短路径比较（例如 `CUISHA~1`）：统一解析实际路径后验证环境归属；组件打包单测 4 项通过。Skill 和 LazyLLM 源码未修改，未暂存或提交子模块指针。

Windows 原生验证：6 项组件测试通过，包含真实 ZIP 经 HTTPS 下载、大小/SHA、解压、导入、激活、重复安装与坏包不替换旧版本。拆包后的基础环境可导入 DashScope/NumPy/pandas/scikit-learn/PyMuPDF；挂载组件后可导入 RAG 依赖，Milvus 建集合、插入和即时向量检索成功。

**发现的既有依赖缺陷，尚未修复：** `milvus-lite==3.0.0` 在 Windows 删除集合时触发 flush，`milvus_lite/storage/manifest.py` 使用 `os.rename(tmp_path, target_path)` 覆盖已存在的 `manifest.json`，报 `WinError 183`。另外单独安装未经裁剪的原始 wheel，仅对原始 `Manifest` 连续执行两次 `save()` 即可复现；原始文件与组件内文件 SHA-256 一致。因此不能将此次结果记为 Windows RAG 持久化/全链路通过，正式发布前需处理该缺陷，并补测显式 flush、重启后检索及删除集合。此次没有通过修改第三方源码掩盖失败，也未更改 RAG 组件内容或 checksum。

验证日志在 Windows 输出目录的 `verification.log`、`stock-milvus-verification.log`、`milvus-smoke.log`。完整 smoke 日志还记录了测试临时目录清理时 Windows 已加载 `.pyd` 文件被锁定的报错；该清理错误不属于应用组件安装失败。

## 2026-09-20 Mac ARM64 本地补测

以 `7587bcb7` 为基础，使用 Node 20.20.2、pnpm 10.34.5、Python 3.11.15 和主仓库记录的 LazyLLM 源码，构建本地 ad-hoc 应用及配套组件。Mac 封装另修复了在尚未签名的 `.app` 内启动 Python 导致系统弹出损坏提示的问题：拆包前将 runtime 临时移到应用包外，完成后恢复，再执行原有签名流程；失败时也恢复 runtime。

- RAG ZIP：`lazymind-python-rag-darwin-arm64-cp311-53a1c2e770966b71.zip`，54,515,729 字节（51.99 MiB）。
- SHA-256：`f90b5c00b43943b031d698fc939c81b24d77a738e357bb541fe74d7e768fb8d1`。
- 最终应用的 catalog 与组件 catalog 一致；本地组件验证六阶段全部通过，包含 Milvus 显式 flush、重启检索和删除集合。报告：`desktop/dist/component-check/darwin-arm64/local-report.json`。
- 裁剪测试 13 项通过、2 项 Windows 专属跳过；Desktop 构建脚本测试 41 项通过。应用 ad-hoc 签名校验通过。
- 首轮导入验证发现 Numba 的 `.nbc/.nbi` 缓存会写入应用内，`-B` 无法禁止这种缓存。验证脚本现将 `NUMBA_CACHE_DIR` 指向报告目录，runtime-manager 将其指向用户 runtime 缓存；定向环境测试通过。清理测试缓存并重新签名后，再执行组件验证及签名复查。
- 用户已上传 ModelScope；实际云端下载后六阶段验证全部通过，验证后应用签名复查通过，报告为 `desktop/dist/component-check/darwin-arm64/cloud-report.json`。尚未执行完整业务界面回归或 Developer ID 签名/公证；此组件应与本次应用配套使用。
- 换电脑交接、完整 ARM64 构建步骤及尚待实现的 Intel 适配清单见 [Mac 打包文档](../../desktop/INSTALL.zh-CN.md)。当前没有可直接运行的 Intel Mac 完整打包入口。

## 体积实测与历史估算

2026-09-20 用户提供两次 GitHub 构建摘要，并核对公开运行页面及本地提交历史：主仓构建使用 `0e7b4fc0f`，优化分支以该提交为基础，仅追加本轮优化和 Windows junction 修复。当前采用这组结果作为对照：

| 构建 | 提交 | Windows installer |
| --- | --- | ---: |
| [主仓原版](https://github.com/LazyAGI/LazyMind/actions/runs/35496179155) | `0e7b4fc0f` | **546.08 MiB** |
| [三个优化开关开启](https://github.com/CarlosShaoting/LazyRAG/actions/runs/35503879386) | `7587bcb7a` | **448.30 MiB**（470,079,386 字节） |
| 实际差值 | | **减少 97.78 MiB / 17.91%** |

优化版文件：`LazyMind-windows-x64-installer-0.3.0-alpha.0-7587bcb7.exe`；SHA-256：`85f70f95542454e2eae41b26371a002f2e0b142e163fd19c73c22717ab500063`；签名状态 `NotSigned`。这两次是相同源码基线、不同仓库的构建；仍可能存在动态依赖版本或构建环境差异，不等同于严格锁定所有依赖的 A/B 实验。未下载并逐项审计最终 EXE 内容，不能仅凭摘要确认全部文件组成或业务功能。

这次 Python 裁剪报告为 **1369.05 → 1040.38 MiB**，减少 **328.67 MiB** 展开文件（缓存 102.64 MiB、测试文件 40.85 MiB、无关火山服务 185.18 MiB）。**报告产生于 RAG 拆包之前**，所以其中仍列出 spaCy、PyArrow、FAISS 等；这些行不能用于判断最终安装包是否仍包含完整 RAG。RAG 后置实际内容以同次构建 `windows-python-components` 的 catalog 为准。展开文件体积、单独组件 ZIP 和 NSIS installer 的压缩方式不同，不能直接相减。

本轮减少的是初始安装包下载量。后续启用 RAG 和首次 warmup 案例下载仍会产生额外流量；不能把 97.78 MiB 差值解释为启用全部功能后的总流量减少。

- 历史 Windows installer：用户提供的 9 月 9 日产物 **480.91 MiB**；未拿到该文件独立复测。该版本比本次主仓基线旧，不再用作本轮优化收益的主要对照。
- 真实 Ark SDK `5.0.50` 隔离环境：展开文件 **214.73 → 29.55 MiB**，移除 **185.18 MiB / 25,801 个文件**；保留的 Ark 图片/视频路径通过 mock 请求。
- Linux/Python 3.11 完整 algorithm 验证环境：第一轮裁剪前约 **1233.20 MiB**，裁剪后约 **1007.77 MiB**；随后 RAG 移出约 **285.87 MiB**，DashScope SDK 留在基础包，基础环境约 **721.90 MiB**。这些是展开体积，不是 installer 大小，也不含全部桌面资源。
- 该次实际组件 ZIP：RAG 约 **89.5 MiB**。生成日期、依赖版本、是否含 bytecode 和平台会影响精确结果，最终看对应 catalog 的 `sizeBytes`。
- 历史低置信度估算 **360–425 MiB** 基于旧的 480.91 MiB 包及跨平台依赖样本，现已由 **448.30 MiB 实测**替代。旧计划中 280–350 MiB 涉及更多共享库/资源后置，也不作为本轮结果。

## GitHub 打包和对照方法

Windows CI 裁剪阶段 `__future__.cpython-311.pyc` 报 `WinError 2` 的修复：uv 创建的 `cpython-3.11-*` 目录联接指向 `cpython-3.11.15-*`，Python 3.11 的 `is_symlink()` 和 `os.walk(followlinks=False)` 没有排除该联接，造成重复统计和重复删除。扫描和空目录清理现统一跳过 Windows reparse points；待删除文件已消失时允许跳过，权限错误仍然报错。原生 Windows 回归先复现相同异常，修复后 15 项裁剪测试全部通过；Linux 13 项通过/2 项 Windows 专属测试跳过，相关 Desktop Node 测试 43 项通过。这是构建脚本修复，与尚未修复的 Milvus `WinError 183` 是两个问题。

修复推送后应在 Actions **重新点 Run workflow，选择 `cst/installer_opt` 最新提交**；失败任务的 “Re-run jobs” 仍使用旧提交，不会获得修复。无需修改 runner 的 `D:\a\...` 路径，也无需关闭三个优化开关。

1. 推送 LazyMind 的本轮改动，**不要加入 `algorithm/lazyllm` 的既有指针差异**。在 Actions 手动运行 **Windows Desktop Installer**，选分支/提交，保持 `prune_python=true`、`defer_python=true`。macOS 同理。普通分支 push 不一定触发 installer workflow。
2. 下载主 installer、`windows-python-components` 和 `windows-python-size-report`（macOS 对应替换前缀）。主 installer 的 `Size` 才是最终下载量；裁剪报告记录的是拆组件之前的 Python 清理收益，catalog 另外记录后置组件体积，不可混算。
3. 如需同提交完整对照，等精简构建结束后再运行一次，**`prune_python`、`defer_python`、`defer_history` 三个开关都设 false**。只关 `prune_python` 仍会后置组件。构建依赖仍有版本范围，比较前核对报告版本；同 ref 并发构建会互相取消。
4. 本地完整对照同时设置 `LAZYMIND_DESKTOP_PRUNE_PYTHON=false`、`LAZYMIND_DESKTOP_DEFER_PYTHON=false`、`LAZYMIND_DESKTOP_DEFER_HISTORY=false`，清理生成目录后完整重建。`resume` 不会恢复已移走的包，不能用于从精简模式切换回 full。
5. 留档：提交、平台、三个开关、依赖版本、installer 大小、RAG 组件大小/哈希、安装耗时、冷启动耗时和磁盘占用。全部组件启用后的总占用不保证下降。

## 不打安装包，先在本机复测

新增 `desktop/scripts/verify-python-components.py`。它只使用 Python 标准库启动验证，必须用**已拆包 runtime 的 algorithm Python**运行；不会向基础环境装包，也不会连接现有 Milvus 或修改用户知识库。默认从 runtime catalog 的 ModelScope URL 下载真实 ZIP，每次使用临时目录及临时数据库，结果和分阶段日志保留到 `desktop/dist/component-check/<平台>-<架构>/`。

**当前这台机器可以直接在 Windows PowerShell 执行**（复用本次已构建的临时 runtime，无需安装 LazyMind 客户端）：

```powershell
$repo = '\\wsl.localhost\Ubuntu\home\cuishaoting\testRag\LazyMind'
$runtime = Join-Path $env:TEMP 'lazymind-components-20260920\runtime'
$python = Join-Path $runtime 'deps\python\algorithm\Scripts\python.exe'
& $python -B (Join-Path $repo 'desktop\scripts\verify-python-components.py') --runtime $runtime
```

以后已有构建环境时，将 `$runtime` 改成对应的 `desktop\build\windows-x64\runtime`。上述临时环境若被清理，需要重新准备；脚本不会偷偷改用系统 Python 或下载 pip 依赖。若只想验证本地 ZIP，可追加 `--bundle-dir (Join-Path $repo 'desktop\dist\python-components\windows-amd64')`，这样不访问云端。

验证内容：

1. 核对解释器所属 runtime、平台、架构和 Python ABI。
2. 确认基础环境确实不含 RAG；检查 DashScope、NumPy、pandas、scikit-learn、UMAP、PyMuPDF、Word/PPT/Excel 依赖可导入。
3. 下载或读取实际组件，校验大小、SHA-256、manifest 与安全解压路径。
4. 在独立子进程挂载组件，导入 LazyLLM RAG、spaCy、Milvus 等依赖。
5. 启动临时 Milvus，建集合、插入、即时检索、显式 flush；停止后重新启动并加载集合，检索原数据，最后删除集合。

全部成功返回退出码 0；任何一步失败返回 1，`report.json` 中 `passed=false`，不会跳过错误再标绿。Windows 已知的 Milvus `WinError 183` 会在落盘步骤暴露。日志目录为每次独立生成，报告记录其路径。此脚本**不等同于 Core 组件安装 API 测试**；此前 Windows 原生 Go 测试已覆盖实际 API 内部下载/安装路径。本脚本侧重让开发者直接复测真实精简环境和依赖持久化行为。

2026-09-20 实跑记录：Linux 本地 ZIP 的全部 6 阶段通过，包含显式 flush、重启后加载并检索旧数据及删除集合；Windows 使用用户刚上传的 ModelScope ZIP，前 5 阶段通过，最后阶段即时检索成功后在显式 flush 处报 `WinError 183`，退出码为 1。两端报告分别在 `desktop/dist/component-check/{linux,windows}-amd64/report.json`。测试子进程退出后清理临时数据库和组件文件，修正了此前临时测试程序在主进程内加载 `.pyd` 导致无法清理的问题。

需要操作业务界面时，已有 Local 开发环境可以在 WSL 仓库根目录执行：

```bash
make local-up
# 浏览器打开 http://127.0.0.1:8090
# 如需源码 Electron 窗口，再执行：
make desktop-dev
```

这两个入口不生成 EXE，但普通 Local 开发环境通常有完整依赖，因此业务 UI 测试还需结合上面的精简环境检查。源码启动需要原有开发依赖和匹配的 LazyLLM 源码；当前工作区此前发现的旧子模块/前端依赖问题见“自动验证与限制”，本次未将源码 UI 启动记为通过。运行已有 Local 环境会使用其已有数据，组件检查脚本则使用临时数据。

仍需安装包验证：NSIS 安装/卸载、真实首次 warmup、Python payload 解压、Electron 重启桥接，以及 macOS 签名；脚本不调用真实模型 API，也不证明聊天/文件解析等全部业务功能通过。

## 你需要测试哪些功能

| 优先级 | 操作 | 通过标准 |
| --- | --- | --- |
| 必测：未装任何组件 | 干净安装，登录，普通/流式对话、模型校验、退出重开；普通千问和豆包各一次 | 无缺模块错误；不等 Milvus，不长时间卡启动页；不自动下载依赖 |
| 必测：未装 RAG | 打开知识库、尝试上传/检索、需要 RAG 的 PDF/Office 附件解析 | 提示安装组件并有设置入口；不能进入无限重试。纯文本、图片和已有独立 DOCX 阅读路径保留 |
| 必测：安装 RAG | 上传对应 ZIP 到云，填 URL 安装；安装并重启 | 基础聊天继续可用；重启后本地解析/Milvus/扫描等服务启用；安装状态跨重启保留 |
| 必测：RAG 功能 | 新建知识库，导入 PDF、DOCX、XLSX；等解析/索引完成；中文/英文检索；重启后再检索 | 返回实际文档内容；数据完整；embedding 与 reranker 均可用 |
| 必测：旧数据升级 | 用旧完整包创建知识库后升级精简包；查看已有记录，再安装匹配 RAG、重启检索 | 会话/文档/索引不丢；安装前提示组件，安装后恢复功能，不隐式迁移或降级 Milvus |
| 建议：通义 | 千问普通聊天、图像生成/编辑；安装 RAG 后测试原有通义语音文档解析、跨模态 embedding | DashScope SDK 仍在基础包，无额外 SDK 下载步骤，原有供应商功能可用 |
| 必测：Ark | 豆包文生图、参考图、多图；文生视频/参考图视频，轮询并播放；普通聊天/embedding | 创建/查询/解析/落盘均正常，保留 SDK 模块足够 |
| 必测：安装异常 | 错 URL、404、错误平台/版本 ZIP、损坏文件、断网、取消后重试、重复点击；安装完成但未重启时查看状态 | 有明确错误/等待重启提示，不激活半成品，不替换旧组件；取消后可重试；任务未被自动重启 |
| 建议：共用依赖 | 常用 Agent/MCP/工作流、内置 Skill、Skill Review 聚类；普通附件和复杂文档样例 | Skill 资源/共用依赖仍在，原有功能不因裁剪测试文件损坏；依赖 RAG 的功能安装组件后可用 |
| macOS 必测 | Developer ID 签名包启动；下载 RAG 组件、重启，解析并检索 | 原生库无 library validation/签名错误，主 `.app` 签名仍可验证 |

如果只有精简版出现问题，用同提交的三个开关均为 false 的包对照，保存组件 catalog、裁剪报告及相关服务日志。取消下载不提供断点续传，重试从头下载。已安装文件被手工删除/磁盘损坏的自动修复不在本轮范围。

## 自动验证与限制

最终验证记录：裁剪单测 11 项、组件打包单测 4 项通过；Desktop 145 项中 139 通过/6 跳过；前端组件测试 3 项通过；独立 Python 环境定向回归 20 项通过/5 个硬编码旧源码路径的 probe 未纳入该次运行；Core systemdeps（含真实组件安装）及 runtime-manager 测试通过。

- 分包单元测试覆盖传递/共享依赖、反向依赖保护、RECORD 文件归属、console launcher 保留、license、ZIP/hash/catalog。
- Core 测试覆盖安全解压、路径穿越、大小/SHA、不兼容 ABI、安装标记、取消、重复安装、旧版本保留、本地权限和缺 RAG 响应；真实 Linux RAG 组件通过本地 HTTPS 服务走生产安装路径和 Python 导入校验；从 RAG overlay 启动 Milvus，实际写入并完成向量检索。
- runtime-manager 测试覆盖缺组件时的进程计划、安装标记和 fingerprint、RAG 安装后激活；现有 manager 测试也运行。
- Python 验证：无 RAG、保留 DashScope 的真实环境可导入 Chat、SQLite proxy，并导入 NumPy/pandas/sklearn/UMAP/PyMuPDF；安装 overlay 后知识检索、能力提示、runtime loader、SQLite 单元回归通过。
- 前端验证安装地址、失败重试、安装后显式重启和知识库安装链接；Desktop 构建/裁剪测试、脚本语法和 YAML 检查。
- 本地子模块检出不是主仓库记录的版本，旧 `.venv` 还存在 editable 路径注入。部分现有 Python 测试硬编码该路径会报现有 `host_file`/writer API 不匹配；另从 gitlink 创建了 `/tmp` 测试副本，并使用独立 Python 3.11 环境验证，没有改子模块。发布 wheel `lazyllm==1.3.0` 自身缺少当前 Chat 路由引用的 writer pandoc 模块；branch 构建带 gitlink 源码能通过，tag 仅用 wheel 的既有兼容问题需单独处理，不能视为已验证。
- 本次尚未验证 Windows 完整 EXE/macOS 签名组件的实际安装；Windows 原生组件构建/安装验证见上方本机构建记录。真实模型调用未执行，需要上表真机回归。前端全量 `tsc --noEmit` 存在多处原有类型错误，不作为本轮通过项；新增代码另做定向检查和测试。本机 `vite build` 因既有依赖环境缺 `lexical` 失败，不能记为全量构建通过。


## 补充：Workflow 演示案例改为 warmup 下载

2026-09-20 按追加要求，将既有 ModelScope 五案例包从安装包移到安装后 warmup 下载。
当前 ZIP 为 **74,408,904 字节 / 70.96 MiB**；这部分原来构建时下载后又内置到安装包，现在只带元数据。
案例本身仍按原流程独立打包、上传 ModelScope；**没有删除 Workflow 定义/执行脚本，也没有修改 Skill**。

- 两端构建脚本调用的 `stage-history-injection-package.mjs` 默认只复制下载描述；`write-runtime-manifest.mjs` 写入 `historyInjectionDownload`，不再要求内置 ZIP。
- `local-runtime-manager` 在 warmup 的 Python 准备之后、Core 启动前下载、校验和解压案例；复用 SHA 缓存及安装标记，不修改应用签名目录。
- 普通首次启动也走同一路径；下载失败保留旧案例、允许应用启动并在下次启动重试。取消启动会取消下载，校验失败的临时文件不会启用。单次下载上限 3 分钟。
- 构建开关：Actions `defer_history=true` / 环境变量 `LAZYMIND_DESKTOP_DEFER_HISTORY=true` 为默认；false 恢复内置案例。完整体积对照需要 **`prune_python=false`、`defer_python=false`、`defer_history=false`** 三个开关都关。
- 相对上一轮可再移出约 71 MiB 的压缩资源；实际 installer 节省看 CI，因为外层压缩可能不同。首次联网 warmup 仍需下载这些字节，并不等于总下载流量减少。
- 现有 ModelScope 链接、案例打包格式、版本更新流程及手工回归清单见 [`docs/history-injection.md`](../history-injection.md)。当前已发布包可以继续使用，无需重新上传同一批案例。

新增验证覆盖：无网络构建仅带元数据、清除旧内置包、两平台 manifest、本地 HTTP 下载校验、缓存/解压结果复用、断网启动、取消、坏包保护、升级重试，以及既有 Core 案例导入测试。真实 ModelScope 发布包下载、SHA 校验和五案例解压测试已通过，清理下载缓存后能复用已解压结果。Desktop 测试 147 项中 141 通过/6 跳过；Core historyinjection、runtime-manager 全量及案例定向 race 测试通过。真机还需确认 warmup 后五个案例和图片/PPT 等产物可打开。

## 后续优化候选（未实施）

1. 用 GitHub 同提交、三个开关开/关产物，确认 Windows 实际减少多少，再按剩余大目录排序。
2. Ark 当前通过裁剪去掉无关云服务，保留 SDK 接口；纯 HTTP 改写需另在 LazyLLM 的 `cst/installer_opt` 分支完成并验证其独立发布版本。
3. 继续审计 reader、渠道 CLI、重复环境的真实引用。若能在不改变 Skill 的前提下独立启用，再沿用同一“构建组件 ZIP → 上传 ModelScope → 校验安装 → 重启”的机制后置。
4. 本轮不推进 Skill 资源、Skill Review 聚类和共用科学计算栈后置，避免干扰同事开发。组件卸载、离线导入、断点续传和损坏修复可另做。
