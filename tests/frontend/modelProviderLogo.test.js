import { existsSync, readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const readFrontendSource = (path) => readFileSync(
  new URL(`../../frontend/src/modules/modelProvider/${path}`, import.meta.url),
  'utf8',
);

describe('model provider logos', () => {
  it('uses one bundled provider-logo mapping in both model settings views', () => {
    const modelProvidersPage = readFrontendSource('pages/ModelProvidersPage.tsx');
    const defaultModelPanel = readFrontendSource('components/DefaultModelConfigPanel.tsx');
    const providerBranding = readFrontendSource('providerBranding.ts');

    expect(modelProvidersPage).toContain('import { getProviderLogoUrl } from "../providerBranding"');
    expect(defaultModelPanel).toContain('import { getProviderLogoUrl } from "../providerBranding"');
    expect(providerBranding).not.toMatch(/https?:\/\//);
    for (const provider of [
      'anthropic',
      'deepseek',
      'doubao',
      'glm',
      'kimi',
      'minimax',
      'openai',
      'openrouter',
      'qwen',
      'sensenova',
      'siliconflow',
    ]) {
      expect(providerBranding).toContain(`"${provider}.svg"`);
      expect(existsSync(new URL(
        `../../frontend/public/provider-icons/${provider}.svg`,
        import.meta.url,
      ))).toBe(true);
    }
  });

  it('uses a standalone vector for SenseNova that Electron can decode reliably', () => {
    const senseNovaLogo = readFileSync(new URL(
      '../../frontend/public/provider-icons/sensenova.svg',
      import.meta.url,
    ), 'utf8');

    expect(senseNovaLogo).not.toContain('data:image');
    expect(senseNovaLogo).toContain('id="sensenova-stripes"');
    expect(senseNovaLogo).toContain('id="sensenova-cutout"');
    expect(senseNovaLogo).toContain('#5927dc');
    expect(senseNovaLogo).toContain('#00e8b2');
  });

  it('uses the official high-resolution MinerU favicon', () => {
    const externalServicesPage = readFrontendSource('pages/ExternalServicesPage.tsx');

    expect(externalServicesPage).toContain(
      'logoUrl: "https://mineru.net/favicon-96x96.png"',
    );
    expect(externalServicesPage).not.toContain(
      'logoUrl: "https://www.google.com/s2/favicons?domain=mineru.net&sz=96"',
    );
  });

  it('keeps the white PaddleOCR logo visible on a contrasting background', () => {
    const styles = readFrontendSource('index.scss');

    expect(styles).toContain(`.model-provider-service-logo-cyan {
  border-color: rgba(8, 145, 178, 0.32);
  background: #0891b2;

  .model-provider-service-logo-icon {
    color: #fff;
  }
}`);
  });
});
