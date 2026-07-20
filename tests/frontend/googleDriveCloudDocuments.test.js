import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

describe('Google Drive cloud-document placement', () => {
  it('keeps authorization under cloud documents rather than system tools', () => {
    const cloudPanel = readFileSync(
      new URL('../../frontend/src/modules/modelProvider/components/CloudDocumentProviderPanel.tsx', import.meta.url),
      'utf8',
    );
    const toolPanel = readFileSync(
      new URL('../../frontend/src/modules/modelProvider/components/ToolManagementSection.tsx', import.meta.url),
      'utf8',
    );

    expect(cloudPanel).toContain('handleManageGoogleDrive');
    expect(toolPanel).not.toContain('GoogleDriveConnectionSection');
  });

  it('documents the Google Audience test-user recovery flow', () => {
    const guide = readFileSync(
      new URL('../../frontend/src/modules/modelProvider/pages/GoogleDriveSetupGuide.tsx', import.meta.url),
      'utf8',
    );
    const zhLocale = readFileSync(
      new URL('../../frontend/src/i18n/locales/zh-CN.ts', import.meta.url),
      'utf8',
    );

    expect(guide).toContain('https://console.cloud.google.com/auth/audience');
    expect(zhLocale).toContain('点击 Add users');
    expect(zhLocale).toContain('重新点击“连接 Google Drive”');
  });
});
