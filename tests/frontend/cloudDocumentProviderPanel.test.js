import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

describe('cloud document provider panel', () => {
  it('keeps local directory management without displaying a directory count', () => {
    const panel = readFileSync(
      new URL(
        '../../frontend/src/modules/modelProvider/components/CloudDocumentProviderPanel.tsx',
        import.meta.url,
      ),
      'utf8',
    );

    expect(panel).toContain('modelProvider.cloudDocuments.manageLocal');
    expect(panel).not.toContain('model-provider-cloud-doc-directory-count');
    expect(panel).not.toContain('modelProvider.cloudDocuments.directoryCountUnit');
  });
});
