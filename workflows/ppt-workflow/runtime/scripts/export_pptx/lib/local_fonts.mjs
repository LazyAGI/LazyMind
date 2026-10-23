import { readdir, readFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import path from 'node:path';
import os from 'node:os';

// TODO: use cloud object storage + local cache here later; preserve hash IDs.
export const fontLibraryRoot = () => path.resolve(process.env.LAZYMIND_FONT_DIR || path.join(os.homedir(), '.local/share/lazymind/fonts'));

export async function localFontFaces() {
  const root = fontLibraryRoot();
  let names;
  try { names = await readdir(root); } catch (error) {
    if (error.code === 'ENOENT') return [];
    throw error;
  }
  const faces = [];
  for (const name of names.filter(name => /^[a-f0-9]{64}\.json$/.test(name)).sort()) {
    try {
      const record = JSON.parse(await readFile(path.join(root, name), 'utf8'));
      if (!record.embeddable || ![`${record.id}.ttf`, `${record.id}.otf`].includes(record.file)
          || !/^[a-f0-9]{64}$/.test(record.id)) continue;
      const data = await readFile(path.join(root, record.file));
      if (createHash('sha256').update(data).digest('hex') !== record.id) continue;
      for (const family of record.aliases) {
        faces.push({ ...record, family, data, url: record.file });
      }
    } catch (error) {
      process.stderr.write(`[fonts] Invalid local font ${name}: ${error.message}\n`);
    }
  }
  return faces;
}

export async function localFontCss(html) {
  const faces = await localFontFaces();
  return faces.filter(face => html.toLowerCase().includes(face.family.toLowerCase())).map(face =>
    `@font-face{font-family:${JSON.stringify(face.family).replaceAll('<', '\\3c ')};font-weight:${face.weight};font-style:${face.style};src:url(data:font/ttf;base64,${face.data.toString('base64')})}`,
  ).join('\n');
}
