const fs = require('node:fs/promises');
const path = require('node:path');

async function localFileAction(payload, shell) {
  const source = payload?.path;
  if (typeof source !== 'string' || !path.isAbsolute(source) || /[\u0000-\u001f\u007f]/.test(source)) {
    throw new Error('Invalid local file path');
  }
  if (!['open', 'reveal'].includes(payload.action)) throw new Error('Invalid file action');
  const resolved = path.resolve(source);
  await fs.stat(resolved);
  if (payload.action === 'reveal') {
    shell.showItemInFolder(resolved);
  } else {
    const error = await shell.openPath(resolved);
    if (error) throw new Error(error);
  }
  return { ok: true, path: resolved };
}

async function openChatExternal(url, shell) {
  const target = new URL(url);
  if (!['https:', 'http:', 'mailto:'].includes(target.protocol)) throw new Error('Invalid external URL');
  await shell.openExternal(target.href);
}

module.exports = { localFileAction, openChatExternal };
