# Intel Mac 打包与固定 RAG 组件交接

更新：2026-09-23。适用分支：`cst/installer_opt`。目标是 Intel 原生应用与 `darwin-amd64/cp311` RAG 组件。M 系列 Mac 使用已有 ARM64 固定方案，不需要重新发布依赖；不同代 M 芯片无需分别打包。

## 当前状态与交付目标

- Mac ARM64 已固定复用 `lazymind-python-rag-darwin-arm64-cp311-53a1c2e770966b71.zip`，本机原生构建、组件验证和界面重启通过；用户反馈已测试的场景没有问题。
- Intel 的原生构建入口已经存在，但尚未在 Intel 真机验收。**当前代码仍会为 Intel 动态生成 RAG ZIP，没有自动切换固定包。** 本文第 5 步需要同事在 Intel 构建通过后完成代码接入，再交回修改。
- 最终要求：Intel 首次验证发布一次自己的 RAG ZIP；后续普通业务改动始终使用同一发布清单和依赖锁，不再自动生成新 RAG ZIP、要求重复上传。
- 不能把 ARM64 ZIP 改名给 Intel 用，也不能复用 Windows ZIP。Python 原生扩展按平台/架构区分。

## 1. 同步完整代码，确认原生构建环境

先取得包含本轮 ARM64 固定包、安装弹窗、重启和 resource_tracker 修复的**完整代码版本**，不要只拿文档。本轮交接改动与本文一起提交到 `origin/cst/installer_opt`；先拉取该分支最新版本。仅拉到 `fa6e7094` 不包含后续 Mac 修复。核对 `git rev-parse HEAD`，确认已包含 `desktop/python-components/darwin-arm64.json`、配套锁及本交接文档，再开始。

新克隆可使用：

```bash
git clone --branch cst/installer_opt --single-branch https://github.com/CarlosShaoting/LazyRAG.git
cd LazyRAG
git submodule update --init --recursive
```

已有干净 checkout：`git switch cst/installer_opt`，然后 `git pull --ff-only origin cst/installer_opt`。不要覆盖其他人的未提交修改，不改 LazyLLM 子模块 gitlink。

需要 Xcode Command Line Tools、Node 20、pnpm 10、兼容的 Go（至少仓库要求版本）和 uv。Python 3.11.15 由脚本安装。验证：

```bash
uname -m                 # x86_64
node -p process.arch      # x64
go env GOHOSTARCH GOARCH  # 两行均为 amd64
sw_vers -productVersion
xcode-select -p
node --version
pnpm --version
go version
uv --version
```

必须使用原生 Intel Mac；M 系列的 Rosetta 终端不代替 Intel 真机验收。记录实际 macOS 版本与原生 wheels 的最低系统要求，不沿用 ARM64 的系统兼容结论。前端测试若遇到 jsdom/undici 与 Node 20 的运行时兼容问题，可用 Node 24 跑测试；打包仍使用已验证的 Node 20/pnpm 10 工具链。

## 2. 首次打包：只为建立 Intel 发布基线生成一次 RAG ZIP

在仓库根目录执行：

```bash
export LAZYMIND_RELEASE_BUILD=false
export LAZYMIND_DESKTOP_PRUNE_PYTHON=true
export LAZYMIND_DESKTOP_DEFER_PYTHON=true
export LAZYMIND_DESKTOP_DEFER_HISTORY=true
export LAZYMIND_DESKTOP_SHARE_PYTHON=false
export LAZYMIND_DESKTOP_PACKAGE_KIND=zip
export LAZYMIND_DESKTOP_SIGNING_MODE=adhoc
make desktop-darwin-x64
```

不要关闭 `DEFER_PYTHON`：关闭会把 RAG 装进主应用，不是固定复用方案。首次基线使用 ad-hoc 签名；Developer ID/公证发行需另做原生组件签名与下载后运行验收。

| 内容 | 路径 |
| --- | --- |
| 测试应用 | `desktop/dist/mac/LazyMind.app` |
| 应用 ZIP | `desktop/dist/LazyMind-darwin-x64.zip` |
| Intel RAG ZIP、清单和 SHA | `desktop/dist/python-components/darwin-amd64/` |
| 尚未拆分的完整中间环境 | `desktop/build/darwin-x64/runtime/` |
| 最终体积报告 | `desktop/build/darwin-x64/final-runtime-size.json` |

`x64` 是构建/Electron 名称，`amd64` 是清单/组件名称；不要把路径混用。RAG 实际文件名必须以生成的 `python-components.json` 为准，不预先编造 hash 或 revision。

## 3. 立即冻结配套环境：必须在再次构建之前完成

**用完整的中间 algorithm Python 导出锁，不能从已拆掉 RAG 的最终 app 导出。** 再次构建会重建中间环境；先保存清单、全部依赖版本和原 RAG ZIP。下面命令同时核对完整版本/RECORD 指纹是否等于组件基线，防止拿错环境：

```bash
desktop/build/darwin-x64/runtime/deps/python/algorithm/bin/python -B - <<'PY'
import importlib.metadata as metadata
import importlib.util
import json
from pathlib import Path
import sysconfig

script = Path('desktop/scripts/build-python-components.py')
spec = importlib.util.spec_from_file_location('components', script)
c = importlib.util.module_from_spec(spec)
spec.loader.exec_module(c)
catalog = json.loads(Path('desktop/dist/python-components/darwin-amd64/python-components.json').read_text())
assert (catalog['platform'], catalog['arch'], catalog['pythonAbi']) == ('darwin', 'amd64', 'cp311')
dists = {c.canonicalize_name(d.metadata['Name']): d
         for d in metadata.distributions(path=[sysconfig.get_path('purelib')])}
identity = {key: catalog[key] for key in ('schemaVersion', 'platform', 'arch', 'pythonAbi')}
versions = {name: dist.version for name, dist in sorted(dists.items())}
records = {name: c.digest((dist.read_text('RECORD') or '').encode()) for name, dist in dists.items()}
fingerprint = c.digest(c.encoded({'identity': identity, 'versions': versions, 'records': records}))
assert fingerprint == catalog['baseFingerprint'], 'Environment does not match the published baseline'
entry = catalog['components']['rag']
for name, version in entry['packages'].items():
    assert versions[name] == version, (name, version, versions.get(name))
out = Path('desktop/python-components')
out.mkdir(parents=True, exist_ok=True)
(out / 'darwin-amd64.json').write_text(json.dumps(catalog, indent=2) + '\n')
(out / 'darwin-amd64-requirements.lock').write_text(
    '# Full Intel algorithm environment paired with ' + entry['filename'] + '\n' +
    ''.join(f'{name}=={version}\n' for name, version in versions.items()))
print('Locked distributions:', len(versions))
print('Filename:', entry['filename'])
print('URL:', entry['url'])
print('SHA256:', entry['sha256'])
PY
```

不要手写预计的依赖数量，不能直接复制 ARM64/Windows 的锁。清单中的 revision、baseFingerprint、packages、SHA 和大小必须保留原值。确认锁文件纳入版本管理（仓库全局 `*.lock` 忽略规则需要已有的目录例外）。

## 4. 本地验证、上传一次、验证真实云端

```bash
MAC_RUNTIME="$PWD/desktop/dist/mac/LazyMind.app/Contents/Resources/runtime"
MAC_PYTHON="$MAC_RUNTIME/deps/python/algorithm/bin/python"
"$MAC_PYTHON" -I -B desktop/scripts/verify-python-components.py \
  --runtime "$MAC_RUNTIME" \
  --bundle-dir "$PWD/desktop/dist/python-components/darwin-amd64" \
  --report "$PWD/desktop/dist/component-check/darwin-amd64/local-report.json"
(cd desktop/dist/python-components/darwin-amd64 && shasum -a 256 -c SHA256SUMS)
codesign --verify --deep --strict desktop/dist/mac/LazyMind.app
```

通过后，将清单指定的 **RAG ZIP 原文件**上传到 ModelScope 数据集 `CarlosShaoting/lazymind-cst` 的 `master` 根目录，保持文件名与内容不变。不是上传应用 ZIP，也不是把组件目录再压一层。保留其他平台已有文件。

默认 URL 形式为 `https://modelscope.cn/datasets/CarlosShaoting/lazymind-cst/resolve/master/` 加实际文件名。实际托管位置若不同，只调整 Intel 清单中的 URL，仍须匹配原文件 SHA/大小/manifest；固定模式不通过环境变量临时替换来源。

上传后执行下面的**真实下载**验证（不带 `--bundle-dir`）：

```bash
"$MAC_PYTHON" -I -B desktop/scripts/verify-python-components.py \
  --runtime "$MAC_RUNTIME" \
  --report "$PWD/desktop/dist/component-check/darwin-amd64/cloud-report.json"
codesign --verify --deep --strict desktop/dist/mac/LazyMind.app
```

需通过基础导入、平台兼容、云端下载 SHA/大小、manifest/解压、RAG 导入和 Milvus 写入/flush/重启检索/删除。失败时修复原生依赖或发布基线，不能删除校验让测试通过。

## 5. 接入 Intel 固定来源：当前还必须修改这三处代码

参照 ARM64 已实现的逻辑扩展，**仅复制 JSON/锁文件还不会自动生效**：

| 文件 | 必须完成的接入 |
| --- | --- |
| `desktop/scripts/build-darwin-arm64.sh` | 此文件实际上是两个 Mac 架构的共用脚本。为 `TARGET_ARCH=x64` 的后置模式联合安装 `darwin-amd64-requirements.lock`、algorithm 两份业务 requirements，并校验 LazyLLM 版本；不再调用动态 `lazyllm install rag`。ARM64 继续使用自己的锁，非后置模式保持原行为。 |
| `desktop/electron/electron-builder.config.cjs` | `splitPythonComponents` 中目前 `published` 只判断 arm64。Intel 后置构建也应调用 `stage-published-python-components.py`，传入 Intel 的 `--catalog`、`--lock`、`--cache`，输出仍为 `dist/python-components/darwin-amd64`。不要再走 `build-python-components.py`。保留 runtime 移出未签名 app、验证、恢复、签名的顺序。 |
| `desktop/scripts/stage-published-python-components.py` | 当前原生平台白名单仅 Windows amd64 和 Darwin arm64；验证后增加 `('darwin', 'amd64')`。继续要求 CPython 3.11.15、清单与真实宿主架构匹配、完整锁匹配、分组边界不变、云端校验、真实基础/overlay 导入及失败恢复。不要放宽成任意平台。 |

Intel 调用应使用以下参数（在 Electron 已移出 app 的 algorithm Python 下调用，由 hook 执行，不直接在未签名 app 中运行）：

```text
stage-published-python-components.py <staged-runtime>
  --output <repo>/desktop/dist/python-components/darwin-amd64
  --catalog <repo>/desktop/python-components/darwin-amd64.json
  --lock <repo>/desktop/python-components/darwin-amd64-requirements.lock
  --cache <repo>/desktop/cache/published-python/darwin-amd64
```

补充 Intel 清单/锁匹配、错误架构拒绝、版本变化拒绝、导入失败恢复的测试，并在 Intel 上跑真实验证。修改固定策略时同步更新 Windows/Mac 交接说明，不改 Skill 或 LazyLLM 源码。

## 6. 再次构建：证明真正固定复用，不是继续动态生成

1. 将首次生成的 RAG ZIP 移到单独的发布备份目录，保留云端原文件与仓库固定清单/锁；让 `desktop/dist/python-components/darwin-amd64/` 中没有旧 ZIP，避免误把旧产物当新产物。
2. 按第 2 步重新执行完整 `make desktop-darwin-x64`。这次应该安装锁定依赖并验证已发布 ZIP，不生成新 RAG ZIP。
3. 组件输出目录应只有固定清单、`SHA256SUMS` 和 `PUBLISHED-COMPONENT.txt`。最终 app 的 `config/published-python-verification.json` 应显示 `source: published`，固定文件名与 SHA 不变。
4. 对最终 app 再跑第 4 步云端验证；解压应用安装测试，确认下载来源是 Intel 固定文件，点击空白不会取消下载，安装后“重启本地服务”能变为“已启用”。
5. 对未安装 RAG 的聊天/附件、安装后的 PDF/Office 入库检索、重启持久化、常用 Skill、实际使用的 Ark 功能做业务回归。测试旧数据升级要单独备份；不要将开发分支较新数据库直接当成旧安装分支的干净环境，不默认删除同事已有数据。
6. 以后正常打包仍是 `make desktop-darwin-x64`，不需要每次上传 RAG。只有明确升级 Python/锁定依赖或改变组件内容时，才建立并发布新的配套版本。

## 7. 交回内容与已知边界

交回代码 commit、macOS/Node/Go/Python 版本、Intel 应用 ZIP、首次发布的 RAG ZIP 与云端 URL、固定 JSON/完整依赖锁、SHA256SUMS、两次构建日志、最终云端验证报告、业务测试结果。大产物不要提交 Git；新锁和清单必须提交。普通后续构建不再要求提供新 RAG ZIP。

样例和 PDF 字体是跨平台资源，不为 Intel 重新生成平台专属版本。样例在首次启动准备阶段下载，Core 启动时导入，后续识别已有内容。PDF 字体 URL 曾返回 404，交付前单独确认；ARM64 用户反馈测试通过不等于所有远端资源已核验。

**仍需核查的签名边界：** 原始打包应用签名通过；ARM64 运行副本曾被 PPT 初始化新增 `workflows/ppt-workflow/runtime/scripts/export_pptx/node_modules` 链接而破坏签名。Intel 需在实际启动/使用后复查签名，不将“打包时签名通过”写成“运行后通过”。此问题与 RAG 固定包不同，应记录或修复后再宣称发行验收完成。ad-hoc 测试通过也不代表 Developer ID/公证或所有 macOS 版本通过。
