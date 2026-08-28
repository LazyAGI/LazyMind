import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const root = path.resolve(import.meta.dirname, '../..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');

describe('Model provider API Key validation', () => {
  const page = read('frontend/src/modules/modelProvider/pages/ModelProvidersPage.tsx');

  it('keeps API Key optional when saving a custom Base URL group', () => {
    expect(page).toContain('const isCustomBaseUrl = !isDefaultProviderBaseUrl');
    expect(page).toContain('if (!isCustomBaseUrl && !apiKey && !existingGroup?.apiKeyConfigured)');
    expect(page).toContain('verify: Boolean(apiKey)');
  });

  it('requires a request-local API Key only for the default Base URL', () => {
    const verifyModalStart = page.indexOf('title={t("modelProvider.verifyGroupTitle"');
    const verifyModalEnd = page.indexOf('title={t("modelProvider.addCustomModelTitle"');
    const verifyModal = page.slice(verifyModalStart, verifyModalEnd);

    expect(page).toContain('const apiKeyRequiredForGroup = isDefaultProviderBaseUrl(provider, group.baseUrl)');
    expect(page).toContain('if (apiKeyRequiredForGroup && !requestApiKey) {');
    expect(page).not.toContain('if (!requestApiKey) {');
    expect(page).toContain('api_key: requestApiKey');
    expect(verifyModal).toContain('required={verifyApiKeyRequired}');
    expect(verifyModal).toContain('required: verifyApiKeyRequired');
    expect(verifyModal).toContain('modelProvider.verifyApiKeyExtra');
    expect(verifyModal).toContain('modelProvider.verifyApiKeyOptionalExtra');
  });
});
