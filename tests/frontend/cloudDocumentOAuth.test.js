import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

describe('cloud document OAuth entry', () => {
  it('does not read the data-source wizard form outside the wizard', () => {
    const oauthEngine = readFileSync(
      new URL(
        '../../frontend/src/modules/dataSource/hooks/management/createOAuthEngine.ts',
        import.meta.url,
      ),
      'utf8',
    );
    const cloudDocumentsHook = readFileSync(
      new URL(
        '../../frontend/src/modules/modelProvider/hooks/useCloudDocumentProviders.ts',
        import.meta.url,
      ),
      'utf8',
    );

    expect(oauthEngine).toContain(
      'options?.draftWizardOpen === false ? {} : form.getFieldsValue(true)',
    );
    expect(cloudDocumentsHook).toContain('draftWizardOpen: false');
  });

  it('uses the shared data-source OAuth URL module for the Notion guide', () => {
    const notionGuide = readFileSync(
      new URL(
        '../../frontend/src/modules/modelProvider/pages/NotionSetupGuide.tsx',
        import.meta.url,
      ),
      'utf8',
    );

    expect(notionGuide).toContain('@/modules/dataSource/oauth/urls');
    expect(notionGuide).not.toContain('../oauth/urls');
  });
});
