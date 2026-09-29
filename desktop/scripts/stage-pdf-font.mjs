import { createHash } from 'node:crypto';
import { readFileSync, writeFileSync, mkdirSync, copyFileSync, rmSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
export function stagePdfFont(runtime, output = path.join(repo, 'desktop/dist/pdf-font')) {
  const descriptor = JSON.parse(readFileSync(path.join(repo, 'desktop/pdf-font.json'), 'utf8'));
  const source = path.join(repo, 'frontend/public/fonts/NotoSansSC-wght.ttf');
  const data = readFileSync(source);
  if (data.length !== descriptor.sizeBytes || createHash('sha256').update(data).digest('hex') !== descriptor.sha256) {
    throw new Error('PDF font changed: update and review desktop/pdf-font.json before building');
  }
  mkdirSync(output, { recursive: true });
  copyFileSync(source, path.join(output, descriptor.filename));
  copyFileSync(path.join(repo, 'frontend/public/fonts/NotoSansSC-OFL.txt'), path.join(output, 'NotoSansSC-OFL.txt'));
  copyFileSync(path.join(repo, 'desktop/pdf-font.json'), path.join(output, 'pdf-font.json'));
  writeFileSync(path.join(output, 'SHA256SUMS'), `${descriptor.sha256}  ${descriptor.filename}\n`);
  mkdirSync(path.join(runtime, 'config'), { recursive: true });
  writeFileSync(path.join(runtime, 'config/pdf-font.json'), JSON.stringify(descriptor, null, 2) + '\n');
  rmSync(path.join(runtime, 'app/frontend/dist/fonts/NotoSansSC-wght.ttf'), { force: true });
  console.log(`PDF font upload: ${path.join(output, descriptor.filename)} (${descriptor.sizeBytes} bytes)`);
}
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  if (!process.argv[2]) throw new Error('usage: stage-pdf-font.mjs <runtime>');
  stagePdfFont(path.resolve(process.argv[2]));
}
