import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { isGoogleOAuthRedirectUriSupported } from '../../frontend/src/modules/dataSource/oauth/redirectUri.ts';

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

  it('accepts only Google-compatible redirect URI origins', () => {
    expect(isGoogleOAuthRedirectUriSupported(
      'http://localhost:8090/oauth/googledrive/data-source/callback',
    )).toBe(true);
    expect(isGoogleOAuthRedirectUriSupported(
      'http://127.0.0.1:8090/oauth/googledrive/data-source/callback',
    )).toBe(true);
    expect(isGoogleOAuthRedirectUriSupported(
      'http://[::1]:8090/oauth/googledrive/data-source/callback',
    )).toBe(true);
    expect(isGoogleOAuthRedirectUriSupported(
      'https://lazymind.example.com/oauth/googledrive/data-source/callback',
    )).toBe(true);

    expect(isGoogleOAuthRedirectUriSupported(
      'http://10.210.0.49:5023/oauth/googledrive/data-source/callback',
    )).toBe(false);
    expect(isGoogleOAuthRedirectUriSupported(
      'https://10.210.0.49:5023/oauth/googledrive/data-source/callback',
    )).toBe(false);
    expect(isGoogleOAuthRedirectUriSupported(
      'http://lazymind.example.com/oauth/googledrive/data-source/callback',
    )).toBe(false);
    expect(isGoogleOAuthRedirectUriSupported(
      'https://lazymind.local/oauth/googledrive/data-source/callback',
    )).toBe(false);
  });

  it('blocks authorization when the current callback URL is unsupported', () => {
    const connectionSection = readFileSync(
      new URL(
        '../../frontend/src/modules/modelProvider/components/GoogleDriveConnectionSection.tsx',
        import.meta.url,
      ),
      'utf8',
    );
    const connectionPage = readFileSync(
      new URL(
        '../../frontend/src/modules/modelProvider/pages/GoogleDriveConnectionPage.tsx',
        import.meta.url,
      ),
      'utf8',
    );
    const guide = readFileSync(
      new URL(
        '../../frontend/src/modules/modelProvider/pages/GoogleDriveSetupGuide.tsx',
        import.meta.url,
      ),
      'utf8',
    );

    expect(connectionSection).toContain('okButtonProps={{ disabled: !callbackUrlSupported }}');
    expect(connectionSection).toContain('googleDriveInvalidRedirectHint');
    expect(connectionPage).toContain('dataSourceGoogleDriveInvalidCallbackHint');
    expect(guide).toContain('redirectUnsupportedHint');
    expect(guide).toContain('redirectRecoveryHint');
  });
});
