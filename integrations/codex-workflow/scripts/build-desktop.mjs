import { execFileSync } from 'node:child_process';
import { copyFileSync, mkdirSync, readdirSync, rmSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const directory = fileURLToPath(new URL('..', import.meta.url));
rmSync(new URL('../dist/desktop/', import.meta.url), { recursive: true, force: true });
execFileSync(process.execPath, [fileURLToPath(new URL('../node_modules/tsdown/dist/run.mjs', import.meta.url)),
  ...['adapter', 'mcp-proxy', 'health', 'prepare', 'manage', 'install'].map(name => `desktop-runtime/${name}.mjs`),
  '--format', 'esm', '--platform', 'node', '--out-dir', 'dist/desktop', '--no-dts',
], { cwd: directory, stdio: 'inherit' });


// Ship the complete runtime inside the CLI; released users do not need pnpm.
const embedded = new URL('../../../local/lazymind-cli/internal/codexruntime/assets/', import.meta.url);
rmSync(embedded, { recursive: true, force: true });
mkdirSync(embedded, { recursive: true });
for (const file of readdirSync(new URL('../dist/desktop/', import.meta.url))) {
  if (file.endsWith('.mjs')) {
    copyFileSync(new URL('../dist/desktop/' + file, import.meta.url), new URL(file, embedded));
  }
}
