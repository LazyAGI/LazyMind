import { lstatSync, readdirSync, existsSync, readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import path from 'node:path';
function size(root) {
  if (!existsSync(root)) return 0;
  const stat = lstatSync(root);
  if (stat.isSymbolicLink()) return 0;
  return stat.isDirectory() ? readdirSync(root).reduce((sum, name) => sum + size(path.join(root, name)), 0) : stat.size;
}
const [runtime, output, ...artifacts] = process.argv.slice(2);
if (!runtime || !output) throw new Error('usage: report-runtime-size.mjs <final-runtime> <report> [artifacts...]');
const categories = ['app', 'runtimes/python', 'deps/python/algorithm', 'deps/python/auth-service',
  'deps/python/channel-gateway', 'deps/python/shared', 'bin', 'builtin-skills', 'featured-skills'];
const report = { runtime: path.resolve(runtime), totalBytes: size(runtime), categories: Object.fromEntries(categories.map(p => [p,size(path.join(runtime,p))])),
  artifacts: Object.fromEntries(artifacts.filter(existsSync).map(p=>[path.basename(p),size(p)])) };
for (const [key, file] of [['rag','python-components.json'],['pdfFont','pdf-font.json'],['sharing','python-sharing.json']]) {
  const p = path.join(runtime, 'config', file);
  if (existsSync(p)) report[key] = JSON.parse(readFileSync(p,'utf8'));
}
mkdirSync(path.dirname(output), {recursive:true});
writeFileSync(output, JSON.stringify(report,null,2)+'\n');
console.log(`Final runtime: ${(report.totalBytes/1048576).toFixed(2)} MiB; ${output}`);
