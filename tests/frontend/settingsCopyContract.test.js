import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const readFrontendSource = (relativePath) => readFileSync(
  new URL(`../../frontend/src/${relativePath}`, import.meta.url),
  'utf8',
);

describe('settings copy contract', () => {
  it('keeps the reviewed Chinese and English settings descriptions', () => {
    const zhCN = readFrontendSource('i18n/locales/zh-CN.ts');
    const enUS = readFrontendSource('i18n/locales/en-US.ts');

    [
      '关闭后不再开始新的定时调度或立即执行；正在运行的任务不会被强制终止。',
      '选择新文档入库时默认使用的解析服务，将 PDF 或扫描件转换成可检索文本。',
      '连接外部 MCP 服务，验证连接、发现工具，并决定 LazyMind 可以调用哪些工具。',
      '在配置的本地路径内进行 glob 匹配、grep 搜索、文件读取和精确文本替换。',
      '在对话中读取并使用已保存的个人记忆和偏好；关闭后不再把这些内容带入回答。',
      '分别启停当前账号的个人技能和工作流。',
    ].forEach((copy) => expect(zhCN).toContain(copy));

    [
      'Turning this off prevents new scheduled or immediate runs from starting; tasks already running are not force-stopped.',
      'Choose the default parsing service for newly imported documents to convert PDFs or scans into searchable text.',
      'Connect external MCP services, verify connections, discover tools, and choose which tools LazyMind may call.',
      'Run glob matching, grep searches, file reads, and precise text replacements within configured local paths.',
      'Read and use saved personal memories and preferences in conversations; when off, this content is no longer included in responses.',
      'Enable or disable personal skills and workflows for the current account independently.',
    ].forEach((copy) => expect(enUS).toContain(copy));
  });

  it('renders localized MCP detail copy and standalone MCP master summary', () => {
    const settingsSource = readFrontendSource('modules/settings/index.tsx');

    expect(settingsSource).toContain(
      'integratedHeader(t("settingsPage.sections.mcp"), t("settingsPage.overview.mcpDesc"))',
    );
    expect(settingsSource).toContain('<p>{controls[key].summary}</p>');
  });

  it('overrides reviewed built-in tool descriptions by stable tool id', () => {
    const toolSource = readFrontendSource(
      'modules/modelProvider/components/ToolManagementSection.tsx',
    );

    [
      'kb: "settingsPage.systemTools.toolDescriptions.kb"',
      'data_sources: "settingsPage.systemTools.toolDescriptions.dataSources"',
      'external_db: "settingsPage.systemTools.toolDescriptions.externalDb"',
      'writer_create: "settingsPage.systemTools.toolDescriptions.aiWriting"',
      'writer_revision: "settingsPage.systemTools.toolDescriptions.aiRevision"',
      'calculator: "settingsPage.systemTools.toolDescriptions.calculator"',
    ].forEach((mapping) => expect(toolSource).toContain(mapping));
    expect(toolSource).toContain(
      'descriptionKey ? t(descriptionKey) : tool.description',
    );
  });

  it('shows the developer mode action for the current switch state', () => {
    const settingsSource = readFrontendSource('modules/settings/index.tsx');
    const zhCN = readFrontendSource('i18n/locales/zh-CN.ts');
    const enUS = readFrontendSource('i18n/locales/en-US.ts');

    expect(settingsSource).toMatch(
      /developerActive\s*\?\s*"settingsPage\.developer\.disableTitle"\s*:\s*"settingsPage\.developer\.enableTitle"/,
    );
    expect(settingsSource).toMatch(
      /enabled\s*\?\s*"settingsPage\.confirm\.developerEnableContent"\s*:\s*"settingsPage\.confirm\.developerDisableContent"/,
    );
    expect(zhCN).toContain('disableTitle: "关闭开发者模式"');
    expect(zhCN).toContain('developerEnableContent: "开启后将启用开发者模式相关功能');
    expect(zhCN).toContain('developerDisableContent: "关闭后将停用开发者模式相关功能');
    expect(zhCN).toContain('developerEnableContentWithoutEvo: "开启后将启用工具管理、内部 ID 和完整执行过程。"');
    expect(zhCN).toContain('enableDescWithoutEvo: "激活工具管理、内部 ID 和完整执行过程。"');
    expect(enUS).toContain('disableTitle: "Disable developer mode"');
    expect(enUS).toContain('developerEnableContent: "Turning this on enables developer-mode features');
    expect(enUS).toContain('developerDisableContent: "Turning this off disables developer-mode features');
    expect(enUS).toContain('developerEnableContentWithoutEvo: "Turning this on enables tool management, internal IDs, and full execution traces."');
    expect(enUS).toContain('enableDescWithoutEvo: "Activate tool management, internal IDs, and full execution traces."');
  });
});
