import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const readFrontendSource = (relativePath) => readFileSync(
  new URL(`../../frontend/src/${relativePath}`, import.meta.url),
  'utf8',
);

describe('settings document parsing view', () => {
  it('configures document parsing from the default models page', () => {
    const knowledgeSettings = readFrontendSource('modules/settings/KnowledgeDataSettings.tsx');
    const defaultServices = readFrontendSource('modules/modelProvider/pages/DefaultServicesPage.tsx');

    expect(defaultServices).toContain('activeFocusCategory === "parsing"');
    expect(defaultServices).toContain('visibleCategories={[activeFocusCategory]}');
    expect(defaultServices).toContain('parsingControl={activeFocusCategory === "parsing" ? parsingControl : undefined}');
    expect(knowledgeSettings).not.toContain('settings-knowledge-parser-controls');
    expect(knowledgeSettings).not.toContain('<ExternalServicesPage');
  });

  it('shows only the document parsing service category', () => {
    const toolSettings = readFrontendSource('modules/settings/KnowledgeToolSettings.tsx');

    expect(toolSettings).toContain('"document-parsing"');
    expect(toolSettings).toContain('? ["parsing"]');
    expect(toolSettings).toContain('includeBuiltinTools={false}');
    expect(toolSettings).toContain('includeDependencies={false}');
    expect(toolSettings).toContain('includeMcp={false}');
  });
});
