# 数据集管理前端技术方案

## 1. 方案目标

基于以下两份文档实现前端数据集管理模块：

- `dataset-management-frontend-prd.md`
- `dataset-management-ui-wireframe.md`

本方案面向前端开发落地，覆盖路由、页面结构、组件拆分、接口占位、状态管理、文件导入解析、行内展开编辑、校验与测试。

关键约束：

- 数据集入口放在资源库菜单中。
- 详情页进入后直接展示样本表格，不重复展示数据集概览信息。
- 样本编辑不使用抽屉，不做单元格编辑；统一点击行后展开编辑区。
- 前端不提供“从问答记录导入”界面；算法回流由后端接口写入，前端只展示 `flowback` 来源。
- 文件型数据源一对一绑定上传文件，一次只上传一个文件。
- `DatasetItem` 字段以数据集模版为主。

## 2. 技术栈与依赖

现有前端技术栈：

- React 18
- React Router v6
- Vite
- TypeScript
- Ant Design 5
- generated OpenAPI client
- `xlsx`
- `zustand`
- `ahooks`

文件解析建议：

- Excel：使用现有 `xlsx`
- CSV：优先使用 `xlsx` 的 CSV 解析能力，避免新增依赖
- JSON：使用原生 `JSON.parse`

如果后续 CSV 需要更强的容错能力，再评估是否引入 `papaparse`。

## 3. 路由与菜单

建议新增模块目录：

```text
frontend/src/modules/datasetManagement
```

建议路由：

```text
/dataset-management
/dataset-management/:datasetId
```

路由注册位置：

```text
frontend/src/router/index.tsx
```

新增：

```tsx
import DatasetListPage from "@/modules/datasetManagement/pages/list";
import DatasetDetailPage from "@/modules/datasetManagement/pages/detail";

<Route path="dataset-management" element={<DatasetListPage />} />
<Route path="dataset-management/:datasetId" element={<DatasetDetailPage />} />
```

资源库菜单入口位置：

```text
frontend/src/layouts/MainLayout.tsx
```

在 `resourceNavItems` 中新增：

```tsx
{
  key: "/dataset-management",
  label: "数据集管理",
  icon: <DatabaseOutlined />,
}
```

后续需要 i18n 时补充：

```text
frontend/src/i18n/locales/*
```

## 4. 目录结构

建议目录：

```text
frontend/src/modules/datasetManagement
├── api.ts
├── constants.ts
├── shared.ts
├── index.scss
├── pages
│   ├── list
│   │   ├── index.tsx
│   │   └── index.scss
│   └── detail
│       ├── index.tsx
│       └── index.scss
├── components
│   ├── DatasetFormModal.tsx
│   ├── DatasetItemTable.tsx
│   ├── DatasetExpandedRowEditor.tsx
│   ├── DatasetImportModal.tsx
│   ├── DatasetTemplateDownload.tsx
│   ├── DatasetFieldMapper.tsx
│   ├── DatasetImportPreview.tsx
│   ├── DatasetImportResult.tsx
│   ├── SourceTypeTag.tsx
│   └── QuestionTypeSelect.tsx
└── utils
    ├── datasetImport.ts
    ├── datasetValidation.ts
    └── datasetFormat.ts
```

## 5. 类型设计

### 5.1 Dataset

```ts
export interface Dataset {
  id: string;
  name: string;
  description?: string;
  owner_id: string;
  group_id: string;
  created_at: string;
  updated_at: string;
}
```

列表接口可聚合返回额外展示字段：

```ts
export interface DatasetListItem extends Dataset {
  knowledge_bases?: Array<{ id: string; name: string }>;
  sample_count?: number;
  source_stats?: {
    upload?: number;
    manual?: number;
    flowback?: number;
  };
}
```

### 5.2 DatasetItem

```ts
export type DatasetItemSource = "upload" | "flowback" | "manual";

export interface DatasetItem {
  id: string;
  dataset_id: string;
  case_id?: string;
  question: string;
  question_type: string;
  ground_truth: string;
  key_points?: string;
  reference_context?: string;
  reference_doc?: string;
  reference_doc_ids?: string[];
  reference_chunk_ids?: string[];
  generate_reason?: string;
  is_deleted?: boolean;
  source: DatasetItemSource;
  source_session_id?: string;
  created_at: string;
  updated_at: string;
  created_by: string;
}
```

### 5.3 Import Row

```ts
export interface DatasetImportRow {
  rowIndex: number;
  raw: Record<string, unknown>;
  normalized: Partial<DatasetItem>;
  errors: string[];
}

export interface FieldMapping {
  [sourceField: string]: keyof DatasetItem | "";
}
```

必填字段：

```ts
export const REQUIRED_ITEM_FIELDS = [
  "question",
  "question_type",
  "ground_truth",
] as const;
```

## 6. 接口设计 TODO

接口设计当前先标记为 TODO。等后端接口、OpenAPI 或联调约定确定后，再补充具体请求路径、请求参数、响应结构和错误码。

前端可先预留模块级接口封装文件：

```text
frontend/src/modules/datasetManagement/api.ts
```

### 6.1 数据集能力 TODO

前端需要以下能力，具体接口待定：

- 获取数据集列表。
- 创建数据集。
- 更新数据集基础信息。
- 删除数据集。
- 获取数据集详情或列表聚合信息。
- 获取或维护数据集关联知识库。

### 6.2 样本能力 TODO

前端需要以下能力，具体接口待定：

- 获取样本列表。
- 新增样本。
- 更新样本。
- 删除样本。
- 批量删除样本。
- 按关键词、问题类型、来源筛选样本。

### 6.3 文件导入能力 TODO

前端需要以下能力，具体接口待定：

- 下载 Excel、CSV、JSON 模版。
- 上传单个文件并生成文件型数据源。
- 确认导入标准化后的样本数据，或提交字段映射由后端解析。
- 获取导入记录。
- 获取导入结果和失败原因。

### 6.4 暂定原则

- 方案中不固定接口路径。
- 方案中不固定 payload 结构。
- 方案中不固定响应结构。
- 前端组件先围绕页面状态和领域类型设计，接口层通过 `api.ts` 隔离，后续替换为 generated client 或真实请求实现。

## 7. 页面设计

### 7.1 数据集列表页

页面职责：

- 展示数据集列表
- 搜索数据集名称/描述
- 新建数据集
- 编辑基础信息
- 删除数据集
- 进入数据集详情

主要状态：

```ts
const [keyword, setKeyword] = useState("");
const [pagination, setPagination] = useState({ current: 1, pageSize: 20 });
const [createModalOpen, setCreateModalOpen] = useState(false);
const [editingDataset, setEditingDataset] = useState<DatasetListItem | null>(null);
```

表格列：

- 数据集名称
- 描述
- 关联知识库
- 样本数
- 来源统计
- 更新时间
- 操作

交互：

- 点击“进入”跳转 `/dataset-management/:datasetId`
- 编辑基础信息复用 `DatasetFormModal`
- 删除前使用 `Modal.confirm`

### 7.2 新建数据集弹窗

表单顺序：

```text
数据集名称
数据集描述
关联知识库
创建方式
```

创建方式：

- `manual`
- `upload`

当选择 `manual`：

- 不展示文件上传区域
- 点击创建后提交创建请求，具体接口 TODO
- 成功后跳转详情页

当选择 `upload`：

- 在创建方式下方展示模版下载和上传区域
- 一次只允许选择一个文件
- 点击下一步进入导入流程

推荐流程：

1. 用户填基础信息并选择上传文件创建
2. 用户选择文件
3. 前端先提交创建请求，具体接口 TODO
4. 前端进入导入 Modal 的字段映射步骤
5. 导入完成后跳转详情页

这样数据源可以明确绑定到已创建的数据集。

### 7.3 数据集详情页

页面职责：

- 面包屑返回
- 样本操作栏
- 搜索和筛选
- 样本表格
- 行内展开编辑
- 文件导入
- 导入记录入口

详情页不展示数据集概览块。概览信息只在列表页展示。

主要状态：

```ts
const [items, setItems] = useState<DatasetItem[]>([]);
const [expandedItemId, setExpandedItemId] = useState<string | null>(null);
const [editingDraft, setEditingDraft] = useState<DatasetItemFormValues | null>(null);
const [dirty, setDirty] = useState(false);
const [selectedRowKeys, setSelectedRowKeys] = useState<React.Key[]>([]);
const [filters, setFilters] = useState({
  keyword: "",
  question_type: undefined,
  source: undefined,
});
```

表格列：

- `case_id`
- `question`
- `question_type`
- `ground_truth`
- `reference_doc`
- `source`
- `updated_at`

行展开规则：

- 表格本身只读展示
- 点击行展开当前行下方编辑区
- 同一时间只允许一行展开编辑
- 切换编辑行前，如果 `dirty = true`，弹确认
- 保存成功后刷新当前行
- 保存失败保留草稿并展示错误

### 7.4 样本新增

建议复用同一个行内展开编辑区：

- 点击“新增样本”
- 在表格顶部插入一条临时行
- 自动展开编辑区
- 保存成功后替换为后端返回的正式样本
- 取消则移除临时行

临时 ID：

```ts
const TEMP_ITEM_ID = "__new__";
```

新增默认值：

```ts
{
  case_id: "",
  question: "",
  question_type: "",
  ground_truth: "",
  source: "manual",
}
```

### 7.5 行内展开编辑区

组件：

```text
DatasetExpandedRowEditor
```

字段布局建议：

- 第一行：`question`、`question_type`、`case_id`
- 大文本：`ground_truth`
- 大文本：`key_points`
- 大文本：`reference_context`
- 普通输入：`reference_doc`
- 普通输入：`reference_doc_ids`
- 普通输入：`reference_chunk_ids`
- 大文本：`generate_reason`
- 只读展示：`source`

数组字段输入规则：

- 展示为逗号分隔字符串
- 保存前转成字符串数组
- 空字符串转为空数组

## 8. 文件导入技术设计

### 8.1 导入 Modal 状态机

```ts
type ImportStep = "selectFile" | "fieldMapping" | "preview" | "result";
```

状态：

```ts
interface ImportState {
  open: boolean;
  step: ImportStep;
  file?: File;
  fileType?: "xlsx" | "xls" | "csv" | "json";
  sourceFields: string[];
  rawRows: Record<string, unknown>[];
  fieldMapping: FieldMapping;
  previewRows: DatasetImportRow[];
  onlyShowErrors: boolean;
  result?: ImportResult;
}
```

### 8.2 文件类型识别

```ts
function getFileType(file: File) {
  const name = file.name.toLowerCase();
  if (name.endsWith(".xlsx")) return "xlsx";
  if (name.endsWith(".xls")) return "xls";
  if (name.endsWith(".csv")) return "csv";
  if (name.endsWith(".json")) return "json";
  if (name.endsWith(".numbers")) return "numbers";
  return "unknown";
}
```

`.numbers` 直接提示：

```text
暂不支持 Numbers 文件，请先导出为 Excel 或 CSV 后再上传。
```

### 8.3 Excel/CSV 解析

使用 `xlsx`：

```ts
import * as XLSX from "xlsx";

async function parseSpreadsheetFile(file: File) {
  const arrayBuffer = await file.arrayBuffer();
  const workbook = XLSX.read(arrayBuffer, { type: "array" });
  const firstSheetName = workbook.SheetNames[0];
  const sheet = workbook.Sheets[firstSheetName];
  return XLSX.utils.sheet_to_json<Record<string, unknown>>(sheet, {
    defval: "",
  });
}
```

CSV 也可以先用 `file.text()` 再：

```ts
const workbook = XLSX.read(csvText, { type: "string" });
```

### 8.4 JSON 解析

支持两种结构：

```json
[{ "question": "..." }]
```

```json
{ "items": [{ "question": "..." }] }
```

校验：

- 根节点必须是数组，或对象里存在 `items` 数组
- 每条必须是对象

### 8.5 字段自动映射

系统字段：

```ts
const DATASET_ITEM_FIELDS = [
  "case_id",
  "question",
  "question_type",
  "ground_truth",
  "key_points",
  "reference_context",
  "reference_doc",
  "reference_doc_ids",
  "reference_chunk_ids",
  "generate_reason",
  "is_deleted",
] as const;
```

自动映射策略：

- 完全同名优先
- 忽略大小写匹配
- 支持少量别名

别名建议：

```ts
const FIELD_ALIASES = {
  question: ["query", "问题"],
  question_type: ["问题类型"],
  ground_truth: ["answer", "标准答案", "答案"],
  reference_chunk_ids: ["reference_chunks", "chunk_ids"],
};
```

### 8.6 数据标准化

保存前统一转换：

- 字符串字段：`String(value ?? "").trim()`
- 数组字段：数组原样保留；字符串按逗号拆分并 trim
- `is_deleted`：支持 `true/false/1/0/是/否`
- `source`：导入时统一设为 `upload`

### 8.7 导入校验

行级错误：

- `question` 为空
- `question_type` 为空
- `ground_truth` 为空
- `is_deleted` 无法识别
- JSON 行结构不是对象

字段映射错误：

- `question` 未映射
- `question_type` 未映射
- `ground_truth` 未映射

确认导入时：

- 默认只提交无错误行
- 如果全部行都有错误，禁止确认导入
- 失败行在预览页保留，支持“只看错误”

## 9. 模版下载

前端展示三种模版下载按钮：

- Excel 模版
- CSV 模版
- JSON 模版

优先走后端模版下载能力，保证模版与服务端一致。具体接口 TODO。

如果后端暂未提供，可以前端临时生成：

- CSV：拼接表头和一行示例
- JSON：下载示例数组
- Excel：用 `xlsx` 生成 workbook

模版字段：

```text
case_id, question, question_type, ground_truth, key_points, reference_context, reference_doc, reference_doc_ids, reference_chunk_ids, generate_reason, is_deleted
```

## 10. 数据来源展示

来源枚举：

```ts
const SOURCE_LABEL_MAP = {
  upload: "上传",
  manual: "手动",
  flowback: "回流",
};
```

展示组件：

```text
SourceTypeTag
```

建议颜色：

- `upload`：blue
- `manual`：green
- `flowback`：purple

前端不能手动修改 `source`。

## 11. 关联知识库

`Dataset` 主表不包含关联知识库字段，前端按聚合字段或独立能力处理。具体接口 TODO。

新建/编辑数据集时：

- 关联知识库非必填
- 支持多选
- 保存时随数据集创建/更新一起提交，或调用独立关联能力，具体接口 TODO

列表展示时：

- 使用列表接口聚合字段 `knowledge_bases`
- 如果没有关联，展示 `-`

## 12. 状态管理策略

MVP 不需要全局 store，优先页面内局部状态。

适合局部状态的内容：

- 列表搜索
- 分页
- 当前展开行
- 编辑草稿
- 导入步骤
- 选中行

可提取为 hooks：

```text
useDatasetList
useDatasetItems
useDatasetImport
useUnsavedConfirm
```

如后续多个模块复用数据集选择能力，再考虑 zustand。

## 13. 错误处理与用户反馈

统一使用 Ant Design：

- `message.success`
- `message.error`
- `Modal.confirm`
- `Alert`
- `Table` empty state
- `Spin` 或骨架屏

错误场景：

- 列表加载失败
- 创建失败
- 保存样本失败
- 删除失败
- 文件格式不支持
- 文件解析失败
- 必填字段未映射
- 导入部分失败

保存失败策略：

- 不清空表单
- 保留编辑草稿
- 高亮错误字段

## 14. 安全与健壮性

展示安全：

- 用户上传内容全部按纯文本展示
- 不使用 `dangerouslySetInnerHTML`
- 长文本用 ellipsis 和展开编辑区展示

导入安全：

- 限制文件类型
- 建议限制文件大小
- `.numbers` 明确提示不支持
- 一次只允许一个文件

编辑安全：

- 离开编辑态前检查未保存变更
- 删除样本和删除数据集都二次确认
- `source`、`source_session_id` 只读

## 15. 可访问性与交互细节

表格：

- 行可点击，需要有 hover 态
- 展开行应有清晰背景区分
- 保存/取消按钮固定在展开区底部右侧
- 展开编辑区字段 label 必须清晰

键盘：

- `Esc` 可触发取消编辑确认
- `Ctrl/Cmd + Enter` 可保存当前编辑
- Tab 顺序按字段自然流动

未保存确认触发点：

- 切换编辑行
- 翻页
- 修改筛选条件
- 刷新列表
- 返回列表
- 路由跳转

## 16. 测试计划

### 16.1 单元测试建议

重点测试纯函数：

- 文件类型识别
- JSON 解析
- Excel/CSV 标准化
- 字段自动映射
- 必填校验
- 数组字段解析
- `is_deleted` 解析

### 16.2 页面测试场景

数据集列表：

- 加载成功
- 空状态
- 搜索
- 创建
- 编辑
- 删除
- 进入详情

新建数据集：

- 手动创建
- 上传文件创建
- 关联知识库为空也可创建
- 上传 `.numbers` 提示不支持

详情页：

- 默认展示样本表格
- 不展示数据集概览块
- 来源筛选
- 问题类型筛选
- 点击行展开编辑
- 保存成功
- 保存失败保留草稿
- 未保存切换行触发确认
- 删除单条
- 批量删除

导入：

- Excel 导入
- CSV 导入
- JSON 数组导入
- JSON `{ items: [] }` 导入
- 必填字段未映射
- 必填字段为空行高亮
- 只看错误
- 确认导入成功后刷新样本表格

### 16.3 手工验收

对照 PRD 第 15 节逐项验收。

## 17. 开发拆分

建议分 5 个前端任务包：

### 17.1 路由与列表页

- 新建模块目录
- 增加路由
- 增加资源库菜单入口
- 实现数据集列表页
- 实现基础搜索、空状态、删除确认

### 17.2 新建/编辑数据集

- 实现 `DatasetFormModal`
- 实现关联知识库多选
- 实现创建方式联动
- 实现手动创建流程
- 实现上传文件创建入口

### 17.3 详情页与样本表格

- 实现详情页
- 实现样本表格
- 实现来源/问题类型筛选
- 实现行展开编辑区
- 实现新增、编辑、删除、批量删除

### 17.4 文件导入

- 实现导入 Modal 状态机
- 实现模版下载
- 实现 Excel/CSV/JSON 解析
- 实现字段映射
- 实现数据预览和错误行
- 实现确认导入

### 17.5 完整联调与体验收尾

- 接口联调
- 错误态补齐
- loading 态补齐
- 未保存确认
- i18n 文案补齐
- TypeScript 和 lint 修复

## 18. 后端依赖确认

需要后端确认：

- Dataset 创建接口是否接收 `knowledge_base_ids`
- 关联知识库是否独立接口维护
- `question_type` 是否固定枚举
- `case_id` 是否前端可传，还是后端生成
- 文件导入采用前端解析提交 JSON，还是后端解析文件
- `reference_doc_ids`、`reference_chunk_ids` 存数组还是字符串
- 删除数据集和删除样本是否逻辑删除
- 算法回流写入时是否保证 `question_type`、`ground_truth` 必填

## 19. 不做事项

MVP 不做：

- 前端手动“从问答记录导入”界面
- 抽屉式样本编辑
- 表格单元格直接编辑
- 多文件合并为一个数据源
- `.numbers` 直接导入
- 数据集导出
- 版本管理
- 数据质量评分
- 训练集/验证集/测试集拆分
