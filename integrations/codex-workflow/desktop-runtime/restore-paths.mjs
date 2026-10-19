import fs from 'node:fs';
import path from 'node:path';
import { checkPrivate, makePrivate } from './platform.mjs';

// Desktop may write its selected CLI into generated MCP manifests/config.toml.
// Restore only this installation's exact path; preserve every unrelated edit.
export function restoreLauncherText(text, launcher, cli) {
  const owned = JSON.stringify(launcher);
  const original = JSON.stringify(cli);
  return text.replace(/((?:"CODEX_CLI_PATH"\s*:|^\s*CODEX_CLI_PATH\s*=)\s*)("(?:[^"\\]|\\.)*")/gm,
    (match, prefix, value) => value === owned ? prefix + original : match);
}

export function restoreGeneratedPaths(config, launcher) {
  const changed = [];
  for (const file of [path.join(config.codexHome, 'config.toml'), ...Object.values(config.sourceManifests ?? {})]) {
    if (!fs.existsSync(file)) continue;
    const info = fs.lstatSync(file);
    if (!info.isFile() || (process.platform === 'win32' ? !checkPrivate(file) : info.uid !== process.getuid())) throw new Error(`Refusing to modify an unowned generated config: ${file}`);
    const before = fs.readFileSync(file, 'utf8');
    const after = restoreLauncherText(before, launcher, config.cli);
    if (after === before) continue;
    const temporary = `${file}.lazymind-${process.pid}.tmp`;
    fs.writeFileSync(temporary, after, { mode: info.mode & 0o777, flag: 'wx' });
    if (process.platform === 'win32') makePrivate(temporary);
    // Desktop must be closed; this catches edits made since the read above.
    if (fs.readFileSync(file, 'utf8') !== before) { fs.unlinkSync(temporary); throw new Error(`Config changed during restore: ${file}`); }
    fs.renameSync(temporary, file); changed.push(file);
  }
  return changed;
}
