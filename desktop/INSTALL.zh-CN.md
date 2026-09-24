# Mac 本地构建、上传依赖与安装验证

适用于 **Apple Silicon Mac（M 系列，ARM64）**。2026-09-23 起，ARM64 默认使用已发布的固定 RAG 包，按“构建 → 自动校验固定组件 → 安装试用”执行，**不再每次构建生成或上传 RAG ZIP**。Workflow 演示案例仍在首次启动 warmup 时下载。

## Mac ARM64 固定云端版本（2026-09-24 更新）

- 固定文件：`lazymind-python-rag-darwin-arm64-cp311-00d718af0b2065c5.zip`。
- 固定清单：`desktop/python-components/darwin-arm64.json`；配套 172 项依赖锁：`darwin-arm64-requirements.lock`。
- 本机/同事的 M 系列 Mac 执行 `make desktop-darwin-arm64`。默认后置模式安装锁定依赖，构建时下载或校验缓存中的已发布 ZIP，验证基础与 overlay 导入后写入固定清单。依赖冲突或校验失败停止构建。
- 用户安装后在应用内下载 RAG，界面只展示固定来源。已上传文件无需替换；`LAZYMIND_PYTHON_COMPONENT_BASE_URL` 不控制 ARM64 固定来源。
- 输出：`desktop/dist/LazyMind-darwin-arm64.zip`；解压得到 `LazyMind.app`，退出旧应用后放到“应用程序”安装测试。默认 ad-hoc 签名，仅供本地测试，没有 Developer ID 公证。
- 缓存：`desktop/cache/published-python/darwin-arm64/`；`desktop/dist/python-components/darwin-arm64/` 输出固定清单、SHA 和来源说明，不生成新 ZIP。
- Intel 仍使用独立的原生 x64 构建流程，不可使用上述 ARM64 组件。

配套机制、下载地址与验证结果见 [Mac 固定组件记录](../docs/development/macos-published-rag.md)。**下文旧的 ARM64“同次构建生成/上传 RAG”步骤仅保留作历史记录，已由本节替代**。PDF 字体等其他资源沿用分支现有机制。

## Windows RAG 固定云端版本（2026-09-23 更新）

Windows 开启 `defer_python` 后，固定复用已上传的 `lazymind-python-rag-windows-amd64-cp311-e262c0d2f05fe09d.zip`，**不再动态生成 RAG ZIP，也不需要每次构建重新上传**。构建机按 `desktop/python-components/windows-amd64-requirements.lock` 安装配套版本，再从固定 URL 获取并验证组件；依赖不兼容时构建失败，不偷偷切换成新包。

GitHub Actions 的 `windows-python-components` 现在提供固定 `python-components.json`、`SHA256SUMS` 和来源说明，正常的干净构建不会生成新的内层 ZIP。已发布的旧 417 MiB 安装包仍使用旧清单，必须安装包含本次修改的新 installer 才会使用固定版本；修改网页链接不会更新已安装程序的清单。

安装组件时界面只显示下载来源，点击“下载并安装”即可；下载失败可以重试/取消，不能编辑地址，API 也不接受自定义 URL。Mac ARM64 同样使用固定来源，Intel 暂保留原分包机制。PDF 字体、FFmpeg、Workflow 案例上传机制不变。

固定下载地址：

```text
https://modelscope.cn/datasets/CarlosShaoting/lazymind-cst/resolve/master/lazymind-python-rag-windows-amd64-cp311-e262c0d2f05fe09d.zip
```

大小 69,342,263 字节；SHA-256 `258944a85d5aa29c0eb662ab21888c5c2894fdfb2c503d51f2129efa4e083bed`。不要覆盖这个文件的内容或改名。普通业务更新复用；需要升级依赖时，由开发者明确更新锁文件与已发布组件清单并完成原生验证，不能绕过哈希。详细范围和测试见 [固定版本开发记录](../docs/development/windows-published-rag.md)。下文涉及“同次构建 RAG 上传”的流程不再适用于 Windows 和 Mac ARM64 固定版本。

## 后续交付平台与实施交接

目标发布平台为 **Windows x64 一个版本、Mac ARM64 与 Mac Intel x64 两个架构版本**。ARM64 继续按第 1～8 节操作，Intel 原生构建使用第 9 节新入口（本机没有 Intel 真机，尚待原生验收）。第二轮瘦身实现与平台验收记录见 [第二轮开发交接计划](../docs/development/desktop-package-size-next-phase.md)。

## 0. 换电脑或交给同事打包：先确认架构

在同事的 Mac 原生终端运行：

```bash
uname -m
node -p process.arch
sw_vers -productVersion
```

| 目标电脑 | 架构 | 当前流程 |
| --- | --- | --- |
| M 系列 Mac | `arm64` | 本文第 1～8 节可用；不同代 M 芯片无需分别打包 |
| Intel Mac | `x86_64`，Node 显示 `x64` | 使用第 9 节原生 x64 入口，首次生成独立 `darwin-amd64` RAG 包，再按第 9 节接入固定复用；真机验收待完成 |

**按目标架构选择入口，并在对应原生 Mac 上构建。** ARM64 使用 `make desktop-darwin-arm64`，Intel 使用 `make desktop-darwin-x64`。脚本检查宿主、Node、Go 和 Python 架构，拒绝 Rosetta 或交叉混用；内部共用流程按目标选择飞书 CLI、manifest、Electron 和输出路径。不能把 ARM64 ZIP 改名为 x64。

CPU 架构与 macOS 版本是两项独立要求。本次 ARM64 构建使用的 `scipy==1.17.1` wheel 标记为 `macosx_14_0_arm64`，因此不能声称支持 macOS 13 及更旧系统；这也不代表已验证所有 macOS 14+ 版本。若需要兼容更旧系统，要另行约束依赖版本、检查原生库最低系统版本并在目标系统实测。

### 交接给另一台机器的内容

1. **完整的同一代码版本**，包括本轮 Mac 修复：构建时将 runtime 暂移出未签名 `.app` 后拆包；验证脚本和 runtime-manager 将 Numba 缓存写到应用外。只复制本文或只拉取旧的 `7587bcb7` 不包含这些本地修复。
2. 主仓库记录的 LazyLLM 子模块版本，按第 1 节初始化；不要复制开发者的 `.venv`、`node_modules` 或旧 build 目录。
3. ModelScope 数据集位置，以及需要的签名方式。ad-hoc ZIP 可用于本地测试；Developer ID/公证构建需要那台机器自己的证书和发布配置，不把证书或密钥放进 Git。
4. 构建完成交回：应用 ZIP/DMG、同次 RAG ZIP、catalog、SHA256SUMS、本地/云端验证报告和构建日志。即使同一提交重新解析依赖，也不能默认沿用另一台机器的组件。

如果修复尚未推送，可在原构建机仓库根目录导出已跟踪文件的修改补丁，并通过文件传输交给同事：

```bash
# 原构建机：记录基线并导出本地修改（输出到仓库外）
git rev-parse HEAD > ../lazymind-mac-build-base.txt
git diff --binary HEAD -- desktop/INSTALL.zh-CN.md \
  desktop/electron/electron-builder.config.cjs \
  desktop/scripts/verify-python-components.py \
  local/local-runtime-manager/runtime_env.go \
  local/local-runtime-manager/runtime_env_test.go \
  docs/development/desktop-package-size-reduction.md \
  > ../lazymind-mac-build.patch
```

同事先检出记录的基线，然后执行 `git apply --check /path/to/lazymind-mac-build.patch`，确认成功后执行 `git apply /path/to/lazymind-mac-build.patch`。如果这些修改已经提交并推送，直接检出包含修复的提交，不要重复应用补丁。

## 1. 准备构建环境和代码

需要 Xcode Command Line Tools、Git、make、Node.js 20、pnpm 10、Go 1.25 或更新的兼容工具链，以及 uv。与仓库 CI 保持一致可减少环境差异；Python 3.11.15 由构建脚本通过 uv 准备，不需要手动安装 Python 依赖。桌面构建不需要 Docker。

```bash
uname -m                  # 必须是 arm64；不要在 Rosetta 的 x86_64 终端中构建
xcode-select -p            # 未安装时先执行 xcode-select --install
git --version
make --version
node --version            # 使用 Node.js 20
node -p process.arch       # 应为 arm64
pnpm --version            # 使用 pnpm 10
go version
uv --version
```

新电脑首次获取代码（本次分支在个人仓库 `CarlosShaoting/LazyRAG`，本机命名为 `upstream` 的官方仓库没有此分支）：

```bash
git clone --branch cst/installer_opt --single-branch \
  https://github.com/CarlosShaoting/LazyRAG.git LazyMind-installer-opt
cd LazyMind-installer-opt
git submodule update --init algorithm/lazyllm
git rev-parse HEAD
git submodule status algorithm/lazyllm
```

已有独立构建仓库时，先确认工作区改动已妥善保存，再更新当前分支：

```bash
git pull --ff-only
git submodule update --init algorithm/lazyllm
git status --short
```

按第 0 节确认 Mac 修复已包含在提交中，或应用交接补丁。建议用独立克隆做构建，避免切换同事正在开发且有未提交修改的工作区。

**不要把 `algorithm/lazyllm` 子模块指针提交到 LazyMind。** 本轮不要求修改 LazyLLM 源码，也不要求修改 Skill。上面的 submodule 命令是在 Mac 获取主仓库记录的源码版本；不要用另一台机器未提交的旧检出替代。

## 2. 选择一种打包方式

先在当前终端明确开启本轮优化，指定用户已在使用的 ModelScope 目录：

```bash
export LAZYMIND_DESKTOP_PRUNE_PYTHON=true
export LAZYMIND_DESKTOP_DEFER_PYTHON=true
export LAZYMIND_DESKTOP_DEFER_HISTORY=true
export LAZYMIND_PYTHON_COMPONENT_BASE_URL='https://modelscope.cn/datasets/CarlosShaoting/lazymind-cst/resolve/master/'
export LAZYMIND_RELEASE_BUILD=false
# 可选：纯 Python 相同依赖共享；默认 false，仅审计不移动
export LAZYMIND_DESKTOP_SHARE_PYTHON=true
```

`LAZYMIND_RELEASE_BUILD=false` 使用本地源码构建方式，随应用带上主仓库记录的 LazyLLM 源码。本地测试不需要创建 release tag。

**自己先测试：选择 ZIP。** 不需要 Developer ID 证书，使用 ad-hoc 签名：

```bash
LAZYMIND_DESKTOP_PACKAGE_KIND=zip \
LAZYMIND_DESKTOP_SIGNING_MODE=adhoc \
make desktop-darwin-arm64
```

需要保存完整构建日志时，可用下面命令替代上面的构建命令；`pipefail` 确保构建失败不会被 `tee` 掩盖：

```bash
mkdir -p desktop/dist/build-logs
set -o pipefail
LAZYMIND_DESKTOP_PACKAGE_KIND=zip \
LAZYMIND_DESKTOP_SIGNING_MODE=adhoc \
make desktop-darwin-arm64 2>&1 | tee desktop/dist/build-logs/build-mac-arm64.log
```

脚本依次下载并校验飞书 CLI、编译 Go 服务、构建前端、安装 Python 3.11.15 及依赖、裁剪 Python、准备内置资源和案例下载描述、组装 Electron、拆出 RAG、签名并压缩应用。首次构建需要联网且下载较多；看到依赖安装完成不代表最终打包完成。结束时必须返回退出码 0，且打印 `.app` 和 ZIP/DMG 路径。

**已有 Developer ID Application 证书：可以选择签名 DMG。** 先确认登录钥匙串中的身份，再构建：

```bash
security find-identity -v -p codesigning
make desktop-darwin-arm64-dmg
```

本地 DMG 命令执行 Developer ID 签名，但**不自动提交 Apple 公证**。正式发布的签名/公证流程见 [Desktop README 的 macOS signed DMG](README.md#macos-signed-dmg)。ad-hoc ZIP 用于本机/内部验证，不代表已经完成正式分发验证。

两种方式选一种即可。切换 ZIP/DMG、签名身份或重新构建后，需要使用新构建配套的 RAG ZIP；不要把前一次的组件默认当作新安装包的配套文件。

## 3. 找到安装包与本次 RAG 组件

| 文件/目录 | 用途 |
| --- | --- |
| `desktop/dist/mac-arm64/LazyMind.app` | 本次完整应用，可在本机直接启动 |
| `desktop/dist/LazyMind-darwin-arm64.zip` | 选择 ZIP 构建时的应用分发包 |
| `desktop/dist/LazyMind-macos-arm64.dmg` | 选择 DMG 构建时的应用分发包 |
| `desktop/dist/python-components/darwin-arm64/lazymind-python-rag-darwin-arm64-cp311-<revision>.zip` | **需要上传到 ModelScope 的 Mac RAG 组件** |
| 同目录的 `python-components.json`、`SHA256SUMS` | 配套清单和校验信息，留档即可 |

目录可能留有旧构建的 ZIP。以下命令对比最终 `.app` 与输出 catalog，并打印**本次唯一应该上传的文件名、默认 URL 和 SHA-256**：

```bash
node <<'NODE'
const fs = require('node:fs');
const dir = 'desktop/dist/python-components/darwin-arm64';
const app = JSON.parse(fs.readFileSync('desktop/dist/mac-arm64/LazyMind.app/Contents/Resources/runtime/config/python-components.json', 'utf8')).components.rag;
const out = JSON.parse(fs.readFileSync(`${dir}/python-components.json`, 'utf8')).components.rag;
for (const key of ['filename', 'revision', 'baseFingerprint', 'sha256', 'platform', 'arch', 'pythonAbi', 'url']) {
  if (app[key] !== out[key]) throw new Error(`安装包与组件不匹配：${key}`);
}
if (out.platform !== 'darwin' || out.arch !== 'arm64') throw new Error('不是 Mac ARM64 组件');
console.log(`上传文件：${dir}/${out.filename}`);
console.log(`下载地址：${out.url}`);
console.log(`大小：${(out.sizeBytes / 1024 / 1024).toFixed(2)} MiB`);
console.log(`SHA-256：${out.sha256}`);
NODE

(cd desktop/dist/python-components/darwin-arm64 && shasum -a 256 -c SHA256SUMS)
```

**安装包与组件必须来自同一次构建。** Mac 组件不能替代已上传的 Windows 组件；本次不发布 Linux，无需上传 Linux 组件。

## 4. 上传前，验证本地组件

Mac 在 Electron 组装 `.app` 时才拆出 RAG；Developer ID 模式还会先签组件内的原生库。请使用**最终 `.app` 内的 Python 和 catalog**，不要用 `desktop/build/darwin-arm64/runtime` 中尚未拆包的中间环境，也不要手动再对已签名 `.app` 运行拆包脚本。

```bash
MAC_RUNTIME="$(pwd)/desktop/dist/mac-arm64/LazyMind.app/Contents/Resources/runtime"
MAC_PYTHON="$MAC_RUNTIME/deps/python/algorithm/bin/python"

"$MAC_PYTHON" -B desktop/scripts/verify-python-components.py \
  --runtime "$MAC_RUNTIME" \
  --bundle-dir "$(pwd)/desktop/dist/python-components/darwin-arm64" \
  --report "$(pwd)/desktop/dist/component-check/darwin-arm64/local-report.json"

# 验证导入没有向应用内写缓存、破坏签名
codesign --verify --deep --strict --verbose=2 desktop/dist/mac-arm64/LazyMind.app
```

成功应返回退出码 0，并且报告为 `"passed": true`。检查包括基础依赖导入、RAG ZIP 校验/解压、RAG 导入、Milvus 写入/落盘/重启后检索/删除集合。测试使用临时数据库，保留分阶段日志，不修改用户知识库。

若报错，先保留报告、日志和本次组件清单定位问题；不要将失败记录当作功能验收通过。当前已有 Windows Milvus `WinError 183` 问题记录，Mac 是否通过以这台 Mac 的实际结果为准。

## 5. 手动上传 ModelScope

1. 登录 ModelScope，打开数据集 [CarlosShaoting/lazymind-cst](https://modelscope.cn/datasets/CarlosShaoting/lazymind-cst)。
2. 将第 3 步打印的 **Mac RAG 原始 ZIP** 上传到 **`master` 分支根目录**。
3. 保持文件名和 ZIP 内容不变，不解压后上传、不再次压缩，也不要把整个组件目录套成另一个 ZIP。
4. `python-components.json`、`SHA256SUMS` 和测试报告本地留档；应用运行不要求将它们上传。应用 ZIP/DMG 是给用户安装的另一个分发文件，不要把它填写为 RAG 下载地址。
5. 已上传的 Windows RAG 文件保留原样。现有 FFmpeg 和 Workflow 五案例包已经在云端，本轮没有更新这些内容，无需重复上传。

默认完整链接为：

```text
https://modelscope.cn/datasets/CarlosShaoting/lazymind-cst/resolve/master/<第3步打印的Mac组件文件名>
```

链接必须无需登录即可直接下载 ZIP。采用默认目录且文件名不变时，**上传后不需要改代码或重新打包**；该链接已经写入本次应用。若换托管位置，由开发者更新构建清单中的固定来源并重新打包；安装界面不再提供 URL 编辑。

## 6. 验证刚上传的云端文件

继续使用第 4 步的两个变量，去掉 `--bundle-dir` 后，脚本会实际下载 catalog 中的 ModelScope URL：

```bash
"$MAC_PYTHON" -B desktop/scripts/verify-python-components.py \
  --runtime "$MAC_RUNTIME" \
  --report "$(pwd)/desktop/dist/component-check/darwin-arm64/cloud-report.json"

codesign --verify --deep --strict --verbose=2 desktop/dist/mac-arm64/LazyMind.app
```

再次确认退出码 0、`passed=true`。这一步验证云端文件与本次安装包清单相符，而不只是网页能打开；测试会下载完整组件。

## 7. 安装应用并按实际用户流程测试

先关闭已有 LazyMind Desktop/Local 实例，避免同一数据目录和端口冲突。ZIP 解压后将 `LazyMind.app` 放到“应用程序”；DMG 则打开后拖入“应用程序”。也可以先直接启动本次构建目录中的应用：

```bash
open desktop/dist/mac-arm64/LazyMind.app
```

按以下顺序检查：

1. 首次启动等待 warmup。Workflow 演示案例应自动下载、校验和导入；核对五个案例及图片/PPT 产物可打开。
2. 尚未安装 RAG 时，普通聊天、模型配置、常用附件功能应可使用；知识库相关操作应提示安装组件，而不是一直转圈。
3. 打开“设置 → 系统工具 → 依赖”，找到 RAG，确认默认地址是第 3 步打印的 Mac ZIP，点击安装。
4. 安装完成后点击“重启本地服务”，或退出后重新打开应用。重启前先结束正在进行的任务。
5. 新建测试知识库，上传 PDF/Word，等待解析完成，验证检索和知识问答；退出重开应用后再次检索同一份文档，并测试删除测试文档/知识库。
6. 回归普通聊天、常用 Workflow、Skill、Ark 图片/视频功能。已有同版本组件时可能直接显示已安装，不需要为了测试清除已有数据。

脚本通过只证明依赖与临时 Milvus 路径通过；界面、真实模型调用、首次 warmup、签名和安装行为仍以上述实际试用为准。

## 8. 常见问题与重新构建

| 现象 | 检查方式 |
| --- | --- |
| RAG 默认 URL 返回 404 | 对照第 3 步文件名，确认上传在 `master` 根目录且公开可读 |
| 下载后大小/SHA 或兼容性校验失败 | 上传对应 `.app` 同次构建的原始 ZIP；安装界面不提供自定义 URL |
| 没有生成 `darwin-arm64` 组件 | 检查构建是否完整成功、`LAZYMIND_DESKTOP_DEFER_PYTHON` 是否为 true，以及最终 `.app` 是否有 catalog |
| 验证脚本提示基础环境仍有 RAG | 检查使用的是最终 `.app` 的 Python，而不是中间 build runtime 或系统 Python |
| DMG 提示找不到签名身份 | 使用已配置 Developer ID 的钥匙串，或改用第 2 步的 ad-hoc ZIP 做本机测试 |
| macOS 提示来源/签名问题 | 区分本地 ad-hoc 包、Developer ID 签名包与已公证包；正式分发遵循现有签名/公证流程 |
| 构建拆包时弹出“应用已损坏”，随后文件消失 | 确认包含将 runtime 暂移到 `.app` 外拆包的修复；被移入废纸篓的构建产物需要重新封装 |
| 验证后签名报新增 `.nbc/.nbi` 文件 | 确认使用新版验证脚本，且 runtime-manager 设置 `NUMBA_CACHE_DIR` 到用户缓存；仅加 `-B` 不能阻止 Numba 缓存 |
| 修改代码后重新打包 | 重做第 3～6 步；按新 catalog 选择组件，不改写旧文件名来冒充新组件 |

后续若改用 GitHub 构建安装包，也要上传那次 Actions 的 `macos-python-components` 附件中配套的原始 RAG ZIP，不能默认沿用这次本地产物。详细实现、体积记录和已知问题见 [开发文档](../docs/development/desktop-package-size-reduction.md)。

## 9. Intel Mac 同事打包与固定 RAG 交接

完整操作见 [Intel Mac 打包与固定 RAG 组件交接](../docs/development/macos-intel-packaging-handoff.md)。该文档是 Intel 首次发布与后续复用的执行入口，包含环境检查、首次打包、从完整中间环境导出依赖锁、上传校验、固定入口接入和第二次构建验收。

- M 系列同事执行 `make desktop-darwin-arm64`，复用已有 `53a1c2e770966b71` 固定包，无需重传。
- Intel 同事在原生 `x86_64` Mac 执行 `make desktop-darwin-x64`，首次生成自己的 `darwin-amd64` 组件；不可使用 ARM64 ZIP。
- **当前 Intel 固定入口尚未接入**。首次打包成功不等于已经固定：必须保存配套清单和完整依赖锁、上传验证，然后按交接文档第 5 节改三处代码，再进行第二次构建确认不生成新 ZIP。
- Intel 原生构建、系统兼容和业务验收由同事完成。本机没有 Intel 真机，不预先声称通过。
- 先拉取 `origin/cst/installer_opt` 最新完整代码，确认包含 Mac 固定清单、配套锁和本轮重启修复；只使用 `fa6e7094` 或更早提交不足以复现。

## 10. 本次 ARM64 已完成的参考结果

2026-09-20，以 `7587bcb7` 加本地 Mac 修复构建：

- RAG 文件：`lazymind-python-rag-darwin-arm64-cp311-53a1c2e770966b71.zip`。
- 大小：54,515,729 字节（51.99 MiB）；SHA-256：`f90b5c00b43943b031d698fc939c81b24d77a738e357bb541fe74d7e768fb8d1`。
- 已上传默认 ModelScope 目录；从云端实际下载后，六阶段验证全部通过，应用签名复查通过。
- 应用为本地 ad-hoc 测试包；没有完成 Developer ID/公证、全部业务界面或其他 macOS 版本验证。

以上是首次发布基线。2026-09-23 起 ARM64 固定使用这份 RAG 清单和配套依赖锁，普通重新构建仍应引用同一文件名与 SHA；应用 ZIP 的 SHA 可以变化。Intel 必须建立自己的发布基线。

## 11. 第二轮资源、共享开关与 Windows 操作

### Python 共享和最终体积报告

`LAZYMIND_DESKTOP_SHARE_PYTHON` 默认 `false`：审计候选，不改变三个环境。设置 `true` 才共享相同内容的纯 Python wheel；GitHub workflow 的对应输入是 `share_python`。Windows 与 Intel 的原生搬迁验收完成前，发布构建可保持关闭。切换回不共享模式应执行完整干净构建，`resume` 不会恢复已移走的包。

报告位于最终 runtime 的 `config/python-sharing.json`，包含共享包、环境、实际节省字节和跳过原因；相同包不是全局暴露给所有环境，而是各环境通过相对 `.pth` 引用自己的共享目录。RAG 拆包先于共享，版本/RECORD 内容不改，catalog 继续绑定同一逻辑依赖集。

最终体积报告：`desktop/build/<目标>/final-runtime-size.json`，区分应用、解释器、各 venv、shared、外置 RAG、字体与最终压缩产物。Mac 的报告在封装后生成；Windows 在 payload 封装前统计实际展开 runtime，完成后补记 EXE/ZIP 大小。不能把展开体积直接当成安装包节省。

### PDF 字体资源（已发布至 Hugging Face）

三个平台共用 [Hugging Face 数据集](https://huggingface.co/datasets/LazyAGI/LazyMind/tree/main) 中的 **TTF 原文件**及 `NotoSansSC-OFL.txt` 许可证。2026-09-24 已上传，下载地址固定到提交 `211752ef6607f899e94f1b01e4c0bfb7285240ac`。不要把 ZIP 上传包地址配置成字体地址，下载器不会解压 ZIP。

- 文件名：`lazymind-pdf-NotoSansSC-a3041811a78c361b.ttf`。
- 大小：17,772,300 字节（16.95 MiB）。
- SHA-256：`a3041811a78c361b1de50f953c805e0244951c21c5bd412f7232ef0d899af0da`。
- 备用下载 URL：`https://huggingface.co/datasets/LazyAGI/LazyMind/resolve/211752ef6607f899e94f1b01e4c0bfb7285240ac/lazymind-pdf-NotoSansSC-a3041811a78c361b.ttf`。

`desktop/pdf-font.json` 保留 ModelScope 为主源，以此 HF 地址为备用源，构建时会写入 runtime 的 `config/pdf-font.json`。旧安装包仍携带原 ModelScope 地址，不能仅靠这次上传自动修复；需要更新其运行时字体 catalog，或后续安装包含新 catalog 的版本。不要直接修改已签名应用包中的文件。用户缓存位于 runtime 的 `deps/pdf-font/<sha>/`，第二次可离线复用，损坏缓存会重新下载。普通 PDF 阅读不触发下载，Web/Docker 静态字体保持原样。

上传后先核对真实下载字节，再在应用中验证中文可搜索 PDF、翻译导出、重启后离线导出与 Mac 签名：

```bash
curl --fail --location --proto '=https' --proto-redir '=https' \
  'https://huggingface.co/datasets/LazyAGI/LazyMind/resolve/211752ef6607f899e94f1b01e4c0bfb7285240ac/lazymind-pdf-NotoSansSC-a3041811a78c361b.ttf' \
  --output /tmp/lazymind-pdf-font-cloud.ttf
shasum -a 256 /tmp/lazymind-pdf-font-cloud.ttf
# 应与上面的完整 SHA-256 一致
```

### Windows x64 原生构建与搬迁验收

在原生 Windows PowerShell、仓库根目录，安装现有 Windows 构建要求的 Node/pnpm、Go、uv 和 Git；不用 WSL Python 代替 Windows Python。先检出同一提交并初始化 LazyLLM 子模块。

```powershell
$env:LAZYMIND_RELEASE_BUILD = 'false'
$env:LAZYMIND_DESKTOP_PRUNE_PYTHON = 'true'
$env:LAZYMIND_DESKTOP_DEFER_PYTHON = 'true'
$env:LAZYMIND_DESKTOP_DEFER_HISTORY = 'true'
# 开启共享用于原生验收；未验收的发布包可保持 false
$env:LAZYMIND_DESKTOP_SHARE_PYTHON = 'true'
powershell.exe -NoProfile -ExecutionPolicy Bypass -File desktop/scripts/build-windows-x64.ps1 installer
```

固定 RAG 清单与来源说明在 `desktop/dist/python-components/windows-amd64/`，此目录不再生成新 RAG ZIP；字体目录同上。新构建移除 staging 中的 Mac Core 开发二进制和 LazyLLM 文档，不删除源文件。解释器别名规范化使用唯一真实捆绑 Python，先检查三个 venv 都能启动，再删除 junction，输出 `runtime/config/python-aliases.json`；异常直接停止构建。

在 Windows 上还需要实际安装 EXE 到含中文/空格路径，确认原构建路径不可用时 warmup 能完成，三个 Python 服务、登录、聊天、飞书、Skill 与 RAG 均能启动；覆盖重复启动、覆盖升级、`resume-installer` 和 `python-runtime.zip` 内容检查。本机仅做了 Windows runtime-manager 测试二进制的交叉编译，不能替代这些原生运行测试。

已有 Windows `milvus-lite==3.0` 的 `WinError 183` 尚未修复，本轮没有静默改第三方源码或旧组件校验值；显式 flush/重启持久化仍需单独验收。


### 2026-09-24 RAG 与模型依赖更新

Mac ARM64 已发布含 gRPC 的 RAG 组件：`lazymind-python-rag-darwin-arm64-cp311-00d718af0b2065c5.zip`，65,714,900 字节，SHA-256 为 `394a6d6b370eb6342d7524ee77fc7e8552fe83d3808ce003ece8f061a27538c5`。主地址为 ModelScope 原数据集，备用地址为 HF 的 `LazyAGI/LazyMind/resolve/main/` 下同名文件。Windows/Intel 本轮未发布新原生组件。

应用下载 RAG 和字体时优先访问 ModelScope；HTTP/网络错误或完整性失败会切换 HF。有备用源时，主源 10 秒未收到内容，或随后 15 秒窗口速度低于 64 KiB/s，也会切换。直接验证实际 HTTPS 下载，不依赖 ICMP ping。两个源共用文件名、大小和 SHA-256；失败内容不会激活，缓存通过校验后可离线复用。构建期下载组件同样支持备用源，主源 socket 超时 10 秒、15 秒速度窗口低于 64 KiB/s 时回退。

新版 Mac catalog 使用 `slim-providers-v1`：按冻结 lock 安装构建依赖，再移除桌面不需要的 OpenSearch，将 gRPC 随 RAG 后置。应用 requirements 及 Mac/Windows 冻结 lock 均已移除火山 SDK，新构建不再下载或安装它；缓存与依赖测试目录的通用裁剪仍保留。该配置要求配套 LazyLLM 的 `cst/install_opt` 分支（Doubao 图片/视频改用 requests，聊天及 embedding 保持原有路径）；主仓子模块已固定到配套提交 `fb0da5aa`，分支 Actions 会检出该提交。源码不匹配会在构建校验时失败。

开发新组件可设置 `LAZYMIND_DESKTOP_REBUILD_PYTHON_COMPONENTS=true`；正常构建使用已发布 catalog。不要把新 ZIP 改成旧文件名。新版本完整安装包尚未重建。

火山 SDK 的逐服务裁剪和 `--verify-ark` 校验已删除，保留不依赖 SDK 的 `--verify-doubao` 校验。旧缓存环境的整包清理兼容逻辑仍保留。已有 RAG ZIP 不含火山 SDK，此次无需重打或重新上传。此前报告中的 185.18 MiB 属于历史裁剪收益，不能重复计入本次收益。

### 精选案例资源按需下载

当前分支集成 PR #778，远端 Skill ZIP 不再进入 installer。构建以 `--catalog-only --frozen-lockfile` 生成预览目录；Skill 安装/使用请求才获取实际包。

精选图片/HTML 使用 `desktop/featured-assets.json` 固定清单：46 个案例 ZIP、MS 主源和 HF 备用源、ZIP 及逐文件 SHA-256。`stage-featured-assets.py` 校验编译素材与发布清单一致，生成小封面缩略图后移出完整素材。素材变化时构建失败，必须显式重新发布对应资源，不能静默使用旧包。

发布步骤：先用 `builtin-skill-bundle --catalog-only --frozen-lockfile` 生成完整 featured 目录，再运行 `stage-featured-assets.py <runtime> --publish <zip-output>`，上传输出 ZIP 到 HF 数据集 `featured-assets/` 目录，保留文件名，提交更新后的清单。正常构建不带 `--publish`，无需下载案例 ZIP。HTTP `/showcase-assets/` 在 desktop 通过 Core 提供缩略图或按案例缓存的资源；其他环境仍由原静态路由处理。

缓存位置：`<用户 runtime>/cache/featured-assets/<ZIP SHA-256>/`。断网时已有缓存和首页封面仍可使用；首次未缓存的详情资源下载失败后，恢复网络并刷新页面重试。图片和 HTML 仅作为案例展示素材分发，HTML 预览继续使用前端 sandbox iframe。

精选下载顺序：Chat 历史样例先下载并导入，再由 Core 后台预取首页 Chat 前 8 个精选、Work 前 8 个精选，最后按展示顺序下载其余可见案例。顺序与首页 placement.order 一致；后台预取不阻塞界面就绪。下载失败跳过，点击详情时自动重试；关闭应用取消下载，下次启动复用已校验缓存。
