import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const readFrontendSource = (path) => readFileSync(
  new URL(`../../frontend/src/modules/modelProvider/${path}`, import.meta.url),
  'utf8',
);

describe('model provider logos', () => {
  it('uses a favicon domain that serves the GLM logo in both model settings views', () => {
    const modelProvidersPage = readFrontendSource('pages/ModelProvidersPage.tsx');
    const defaultModelPanel = readFrontendSource('components/DefaultModelConfigPanel.tsx');

    expect(modelProvidersPage).toContain('[/glm|bigmodel|zhipu/, "zhipuai.cn"]');
    expect(defaultModelPanel).toContain('[/glm|bigmodel|zhipu/, "zhipuai.cn"]');
  });

  it('uses the OpenRouter favicon in both model settings views', () => {
    const modelProvidersPage = readFrontendSource('pages/ModelProvidersPage.tsx');
    const defaultModelPanel = readFrontendSource('components/DefaultModelConfigPanel.tsx');

    expect(modelProvidersPage).toContain('[/openrouter/, "openrouter.ai"]');
    expect(defaultModelPanel).toContain('[/openrouter/, "openrouter.ai"]');
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
