// Resolve only package managers carried by this Desktop installation.
function resolveDSHPnpm(resources) {
  const { statSync } = require('node:fs');
  const { join } = require('node:path');
  for (const relative of [
    'runtime/primary-runtime/dependencies/pnpm/bin/pnpm.mjs',
    'runtime/pnpm/bin/pnpm.mjs',
  ]) {
    const candidate = join(resources, relative);
    try {
      if (statSync(candidate).isFile()) return candidate;
    } catch (error) {
      if (error.code !== 'ENOENT' && error.code !== 'ENOTDIR') throw error;
    }
  }
  throw new Error('Desktop bundled pnpm entry is missing');
}
