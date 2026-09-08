// Explicit offline recovery of the one legacy LazyMind event. Never run at install time.
import { readFile, writeFile, lstat, open, rename, unlink } from 'node:fs/promises';
import { basename, dirname, resolve } from 'node:path';
import { createHash, randomUUID } from 'node:crypto';
import { zstdDecompressSync, zstdCompressSync, constants } from 'node:zlib';

const args = process.argv.slice(2);
const fileAt = args.indexOf('--file');
const input = fileAt >= 0 ? args[fileAt + 1] : undefined;
if (!input || args.some((v,i) => i !== fileAt + 1 && !['--file','--apply','--offline'].includes(v))) throw Error('Use --file <session.jsonl.zstd> [--apply --offline]');
if (args.includes('--apply') && !args.includes('--offline')) throw Error('Stop DSH first; --apply requires the explicit --offline acknowledgement');
const file = resolve(input);
if (!['session.jsonl','session.jsonl.zstd'].includes(basename(file))) throw Error('Select one DSH session.jsonl or session.jsonl.zstd file');
const stat = await lstat(file);
if (!stat.isFile() || stat.isSymbolicLink() || stat.size > 128 * 1024 * 1024) throw Error('Expected one regular session log of at most 128 MiB');
const original = await readFile(file);
const hash = bytes => createHash('sha256').update(bytes).digest('hex');
const beforeHash = hash(original);
const compressed = file.endsWith('.zstd');
function decodeFrames(bytes) {
  const chunks=[];let offset=0,total=0;
  while(offset<bytes.length) {
    const frame=zstdDecompressSync(bytes.subarray(offset), {info:true,maxOutputLength:128*1024*1024-total});
    if(frame.engine.bytesWritten<=0)throw Error('Zstandard decoder made no progress');
    offset+=frame.engine.bytesWritten;total+=frame.buffer.length;chunks.push(frame.buffer);
  }
  return Buffer.concat(chunks);
}
const decoded = compressed ? decodeFrames(original) : original;
const text = decoded.toString('utf8');
if (!Buffer.from(text).equals(decoded)) throw Error('Session log is not valid UTF-8');
const lines = text.split('\n');
const changed = [];
for (let i=0;i<lines.length;i++) {
  if (!lines[i].trim()) continue;
  const row = JSON.parse(lines[i]);
  if (row.type !== 'lazymind-workflow/open') continue;
  if (Object.hasOwn(row,'ignorable')) {
    if (row.ignorable !== true) throw Error('A legacy event explicitly requires interpretation; automatic repair is not applicable');
    continue;
  }
  if (!Number.isSafeInteger(row.seq) || typeof row.data !== 'object' || row.data === null) throw Error('Unexpected legacy event shape');
  lines[i] = JSON.stringify({...row,ignorable:true});
  changed.push({line:i+1,seq:row.seq,type:row.type});
}
const report = {mode:args.includes('--apply')?'apply':'preview',file,before_sha256:beforeHash,changed_records:changed};
if (args.includes('--apply') && changed.length) {
  const backup = `${file}.before-lazymind-${beforeHash.slice(0,16)}.bak`;
  try {await writeFile(backup,original,{flag:'wx',mode:0o600});}
  catch(error) {if(error.code !== 'EEXIST' || hash(await readFile(backup)) !== beforeHash) throw error;}
  // Retain DSH's separate checksummed header and event frames for header-only reads.
  const opts = {params:{[constants.ZSTD_c_checksumFlag]:1}};
  const output = compressed ? Buffer.concat([
    zstdCompressSync(Buffer.from(lines[0]+'\n'),opts),
    zstdCompressSync(Buffer.from(lines.slice(1).join('\n')),opts),
  ]) : Buffer.from(lines.join('\n'));
  const temp = `${file}.repair-${randomUUID()}`;
  try {
    const writer = await open(temp,'wx',stat.mode & 0o777);
    try {await writer.writeFile(output);await writer.sync();} finally {await writer.close();}
    const current=await lstat(file);
    if (current.ino!==stat.ino || current.size!==stat.size || current.mtimeMs!==stat.mtimeMs || hash(await readFile(file))!==beforeHash) throw Error('Session log changed during repair; no replacement was made');
    await rename(temp,file);
    if (process.platform !== 'win32') {
      const directory=await open(dirname(file),'r');
      try {await directory.sync();} finally {await directory.close();}
    }
  } finally {await unlink(temp).catch(error=>{if(error.code!=='ENOENT')throw error;});}
  report.backup=backup;
  report.after_sha256=hash(output);
  report.restore='With DSH stopped, replace this one log with the reported backup to restore the original bytes.';
}
console.log(JSON.stringify(report,null,2));
