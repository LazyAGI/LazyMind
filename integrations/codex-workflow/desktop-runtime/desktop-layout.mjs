import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { native } from './platform.mjs';

const uniqueFile = (candidates, label) => {
  const found = [...new Set(candidates.filter(file => fs.existsSync(file) && fs.statSync(file).isFile()).map(file => fs.realpathSync(file)))];
  if (found.length !== 1) throw new Error(`Expected one bundled ${label}; unsupported or ambiguous Desktop layout`);
  return found[0];
};
export function desktopLayout(app, platform = process.platform, operations = { native, execFileSync }) {
  if (platform === 'darwin') {
    const resources = path.join(app, 'Contents/Resources');
    const plist = key => execFileSync('/usr/libexec/PlistBuddy', ['-c', `Print :${key}`, path.join(app, 'Contents/Info.plist')], { encoding: 'utf8' }).trim();
    return { resources, cli: path.join(resources, 'codex-cli/CodexCLI.app/Contents/MacOS/codex'),
      node: path.join(resources, 'cua_node/bin/node'), desktopExecutable: path.join(app, 'Contents/MacOS', plist('CFBundleExecutable')),
      desktopVersion: plist('CFBundleShortVersionString') };
  }
  if (platform !== 'win32') throw new Error('Shared Desktop supports macOS and Windows');
  const resources = path.join(app, 'resources');
  const cli = uniqueFile(['codex.exe', 'codex-cli/codex.exe', 'codex-cli/bin/codex.exe'].map(p => path.join(resources, p)), 'CLI');
  const node = uniqueFile(['cua_node/node.exe', 'cua_node/bin/node.exe'].map(p => path.join(resources, p)), 'Node');
  const desktopExecutable = operations.native('windows-desktop-executable', { path: app });
  const powershell = path.join(process.env.SystemRoot ?? 'C:\\Windows', 'System32/WindowsPowerShell/v1.0/powershell.exe');
  const desktopVersion = operations.execFileSync(powershell, ['-NoProfile', '-NonInteractive', '-Command',
    '[System.Diagnostics.FileVersionInfo]::GetVersionInfo($env:LAZYMIND_VERSION_TARGET).ProductVersion'],
    { env: { ...process.env, LAZYMIND_VERSION_TARGET: desktopExecutable }, encoding: 'utf8', windowsHide: true, timeout: 10000 }).trim();
  return { resources, cli, node, desktopExecutable, desktopVersion };
}
