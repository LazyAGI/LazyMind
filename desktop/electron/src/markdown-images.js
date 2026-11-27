const fs = require('node:fs/promises');
const { constants } = require('node:fs');
const path = require('node:path');
const { fileURLToPath } = require('node:url');
const { containsPath } = require('./local-folder-access');

function bounded(promise, ms) {
  let timer;
  return Promise.race([promise, new Promise((_, reject) => {
    timer = setTimeout(() => reject(new Error('image read timed out')), Math.max(1, ms));
  })]).finally(() => clearTimeout(timer));
}

async function collectMarkdownImages(filename, references, allowedRoots = []) {
  const images = [], warnings = [];
  const sources = [...new Set(references)].slice(0, 32);
  const deadline = Date.now() + Math.max(15000, Math.min(120000, sources.length * 5000));
  const original = await fs.realpath(filename);
  if (!/\.(md|markdown)$/i.test(original) || !(await fs.stat(original)).isFile()) return { images, warnings };
  const roots = [path.dirname(original)];
  for (const root of allowedRoots) {
    try { roots.push(await fs.realpath(root)); } catch { /* An unavailable grant adds no access. */ }
  }
  let bytes = 0;
  for (const source of sources) {
    if (Date.now() >= deadline) { warnings.push('引用图片读取超时，已跳过剩余图片。'); break; }
    try {
      const result = await bounded((async () => {
        if (typeof source !== 'string' || source.length > 4096 || source.startsWith('//')) throw new Error('invalid path');
        let candidate;
        if (/^file:/i.test(source)) candidate = fileURLToPath(source);
        else {
          if (/^[a-z][a-z0-9+.-]*:/i.test(source) && !path.isAbsolute(source)) throw new Error('unsupported image URI');
          candidate = path.resolve(path.dirname(original), decodeURIComponent(source.split('#')[0]));
        }
        candidate = await fs.realpath(candidate);
        if (!roots.some(root => containsPath(root, candidate))) throw new Error('image outside allowed folders');
        const stat = await fs.stat(candidate);
        if (!stat.isFile() || stat.size < 1 || stat.size > 20 * 1024 * 1024 || bytes + stat.size > 40 * 1024 * 1024) throw new Error('image size limit');
        const ext = path.extname(candidate).toLowerCase();
        const mime = { '.png': 'image/png', '.jpg': 'image/jpeg', '.jpeg': 'image/jpeg', '.gif': 'image/gif',
          '.webp': 'image/webp', '.svg': 'image/svg+xml', '.bmp': 'image/bmp', '.tif': 'image/tiff', '.tiff': 'image/tiff' }[ext];
        if (!mime) throw new Error('unsupported image format');
        const handle = await fs.open(candidate, constants.O_RDONLY | (constants.O_NOFOLLOW || 0));
        try {
          const opened = await handle.stat();
          if (await fs.realpath(candidate) !== candidate || !opened.isFile() || opened.dev !== stat.dev || opened.ino !== stat.ino) throw new Error('image changed');
          const buffer = Buffer.alloc(stat.size + 1);
          const { bytesRead } = await handle.read(buffer, 0, buffer.length, 0);
          if (bytesRead !== stat.size) throw new Error('image changed');
          return { image: { source, name: path.basename(candidate), mime_type: mime, content_base64: buffer.subarray(0, bytesRead).toString('base64') }, size: bytesRead };
        } finally { await handle.close(); }
      })(), Math.min(5000, deadline - Date.now()));
      images.push(result.image);
      bytes += result.size;
    } catch { warnings.push(`图片未导入：${source}`); }
  }
  return { images, warnings };
}
module.exports = { collectMarkdownImages };
