import { readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import type { Plugin } from 'vite';

/** Keep CMaps alongside the application, including offline desktop builds. */
export function pdfCmapsPlugin(pdfjsRoot: string): Plugin {
  const directory = path.join(pdfjsRoot, 'cmaps');
  const files = readdirSync(directory).filter(name => name.endsWith('.bcmap') || name === 'LICENSE');
  const assets = new Map(files.map(name => [`pdfjs/cmaps/${name}`, path.join(directory, name)]));
  let base = '/';
  return {
    name: 'pdf-cmaps',
    configResolved(config) { base = config.base; },
    configureServer(server) {
      server.middlewares.use((request, response, next) => {
        const pathname = (request.url || '').split('?')[0];
        const prefix = base === './' || base === '' ? '/' : base;
        const file = pathname.startsWith(prefix) ? assets.get(pathname.slice(prefix.length)) : undefined;
        if (!file) return next();
        response.setHeader('Content-Type', 'application/octet-stream');
        response.end(readFileSync(file));
      });
    },
    generateBundle() {
      for (const [fileName, file] of assets) {
        this.emitFile({ type: 'asset', fileName, source: readFileSync(file) });
      }
    },
  };
}
