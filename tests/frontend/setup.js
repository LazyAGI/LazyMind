import { readFileSync } from 'fs';
import { dirname, join } from 'path';
import { fileURLToPath } from 'url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const frontendRoot = join(__dirname, '../../frontend');

export const readFrontendFile = (...parts) =>
  readFileSync(join(frontendRoot, ...parts), 'utf-8');

export const indexHtml = readFrontendFile('index.html');
export const mainEntry = readFrontendFile('src/main.tsx');
export const routerSource = readFrontendFile('src/router/index.tsx');
export const mainLayoutSource = readFrontendFile('src/layouts/MainLayout.tsx');
export const loginSource = readFrontendFile('src/modules/signin/pages/login/index.tsx');
export const formRulesSource = readFrontendFile('src/modules/signin/utils/formRules.ts');
export const runtimeModeSource = readFrontendFile('src/runtime/mode.ts');
export const runtimeFeaturesSource = readFrontendFile('src/runtime/features.ts');
export const runtimeApiBaseSource = readFrontendFile('src/runtime/apiBase.ts');
export const runtimeDesktopBridgeSource = readFrontendFile('src/runtime/desktopBridge.ts');

export const routePaths = Array.from(
  routerSource.matchAll(/<Route\s+[^>]*path=["']([^"']+)["']/g),
  (match) => match[1],
);
