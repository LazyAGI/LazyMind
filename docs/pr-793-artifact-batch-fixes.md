# PR #793：批量产物保存修复与验证

## Review 与修改

对照 #793 原提交 `56f7260a6` 和 `cst/product_prs` 的 `57f1b0f94`：没有文本冲突，也没有涉及此前回退的 Writer/Markdown 编辑器。两个可复现的批量保存问题已修复。

### 同名文件在整批准备时相互覆盖

原实现按 basename 复制到工作目录；批量准备 `a/report.txt=FIRST`、`b/report.txt=SECOND` 后，两个发布事件都只能读到 SECOND。

现在外部文件每次复制到任务工作目录下新建的独立 `.artifact-*` 子目录，保留原文件名，使用独占创建，不覆盖其他输入、旧产物或失败批次的文件。文件、图片、file_list 共用此逻辑。工作目录根层已有文件仍按原协议直接引用。文件授权解析同步声明复制目标的父目录；授权后的源文件读取校验保留，受限 POSIX 分支继续使用固定父目录句柄和 O_NOFOLLOW/O_EXCL。

失败批次可能留下未发布的独立副本，但不会改写已经发布的副本；后续重试可正常保存。这不是数据库事务，不替代 Core 的冻结和发布校验。

### 列表顺序查询异常被误当成空列表

原实现把查询异常缓存为 `[]`，导致同批后续显式 sort_order 覆盖请求均失去 list_index，以追加形式保存并返回成功。

现在查询异常、缺少 order_list 或返回格式错误会在预校验阶段抛出工具错误，整批不发产物事件、不消耗序号，也不写草稿。仅缓存成功查询；重试重新查询后按真实索引覆盖。确认返回空列表时保留原有行为，普通未指定 sort_order 的追加和单值产物不受影响。patch/discard 共用查询函数，因此查询失败也会明确报错，不再操作无法确定的目标草稿。

## 测试

新增回归覆盖：不同目录的同名文件/图片/file_list、保留原文件名、重复保存后的旧副本不变、失败批次保护旧副本且可重试、查询失败/异常响应不发布、重试恢复覆盖索引、成功的列表顺序缓存、确认空列表的既有行为，以及文件授权解析的新目标目录。真实工具中间件测试通过已发布路径验证文件，不再假设副本固定放在工作目录根层。

运行仓库 Python 测试环境，安装仓库声明的 LazyLLM 子模块和 Pandoc 3.11：

```bash
LAZYLLM_INIT_DOC=1 PYTHONPATH=algorithm:algorithm/lazyllm python -m pytest tests/algorithm/ -q
make lint-python PYTHON=python
```

修复分支串行全量验证：3954 passed、17 skipped、21 subtests passed；Python lint 和 git diff --check 通过。两套全量并行运行时出现过聊天流/并发测试失败，因此改为串行验收。

另在隔离 worktree 将修复后的 #793 与 `cst/product_prs` 合并，再执行相同的全量 Python 测试。合并后的串行全量结果：4054 passed、17 skipped、21 subtests passed。没有把两个 PR 的业务代码混合提交。
