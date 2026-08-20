import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const readFrontendSource = (relativePath) => readFileSync(
  new URL(`../../frontend/src/${relativePath}`, import.meta.url),
  'utf8',
);

describe('settings document parsing view', () => {
  it('embeds document parsing configuration in the knowledge settings page', () => {
    const knowledgeSettings = readFrontendSource('modules/settings/KnowledgeDataSettings.tsx');

    expect(knowledgeSettings).toContain('className="settings-knowledge-parser-controls"');
    expect(knowledgeSettings).toContain('className="settings-knowledge-parser-services"');
    expect(knowledgeSettings).toContain('<ExternalServicesPage');
    expect(knowledgeSettings).toContain('visibleCategories={["parsing"]}');
    expect(knowledgeSettings).not.toContain(
      'navigate("/settings?section=knowledge&tool=document-parsing")',
    );
    expect(knowledgeSettings).not.toContain('navigate("/model-providers/tools")');
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
