import fs from 'node:fs';
import path from 'node:path';
import { processIdentity } from './config.mjs';
import { checkPrivate } from './platform.mjs';

export function selectBridge(root) {
  const records = [];
  for (const name of fs.readdirSync(root).filter(name => /^bridge-\d+\.json$/.test(name))) {
    let record;
    try { checkPrivate(path.join(root, name)); record = JSON.parse(fs.readFileSync(path.join(root, name), 'utf8')); } catch { continue; }
    const identity = processIdentity(record.pid);
    const parentIdentity = processIdentity(record.ppid);
    if (!identity || !parentIdentity || identity !== record.identity || parentIdentity !== record.parentIdentity) continue;
    if (record.dir !== path.join(root, `bridge-${record.pid}`) || !record.group) throw new Error('Invalid bridge registration');
    records.push(record);
  }
  if (!records.length) throw new Error('No live Desktop bridge; reopen Desktop');
  if (new Set(records.map(record => record.group)).size !== 1) throw new Error('Ambiguous Desktop contexts; refusing arbitrary routing');
  return records.sort((a, b) => a.created - b.created || a.pid - b.pid)[0];
}
