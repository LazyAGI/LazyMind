import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

function inside(root, file) {
  const relative = path.relative(fs.realpathSync(root), fs.realpathSync(file));
  return relative !== '' && !path.isAbsolute(relative) && relative !== '..' && !relative.startsWith(`..${path.sep}`);
}

// Accept only the known desktop wrapper. Never evaluate an arbitrary shell
// command or infer a Node executable from PATH.
export function resolvePluginManifest(kind, plugin, file, node, options = {}) {
  const platform = options.platform ?? process.platform;
  const cwd = plugin?.cwd ?? path.dirname(file);
  if (!plugin || !Array.isArray(plugin.args) || !plugin.args.every(arg => typeof arg === 'string') || typeof plugin.command !== 'string' || !path.isAbsolute(cwd)) {
    throw new Error(`Expected the installed, resolved ${kind} manifest`);
  }
  const args = plugin.args;
  let resolved = { ...plugin, cwd };
  if (platform === 'win32' && kind === 'codex_app' && /^(?:.*[\\/])?cmd\.exe$/i.test(plugin.command)) {
    const wrapper = './scripts/launch_codex_app_tools_mcp.cmd';
    const command = ['call', wrapper, './server.mjs'];
    const tail = args.slice(3);
    const known = JSON.stringify(tail) === JSON.stringify(command) ||
      (tail.length === 1 && tail[0] === command.join(' '));
    if (args.slice(0, 3).map(arg => arg.toLowerCase()).join(' ') !== '/d /s /c' || !known) {
      throw new Error('Unsupported codex_app cmd.exe wrapper');
    }
    if (!inside(cwd, path.join(cwd, wrapper)) || !inside(cwd, path.join(cwd, 'server.mjs'))) {
      throw new Error('codex_app wrapper escapes its plugin directory');
    }
    resolved = { ...resolved, command: node, args: [path.join(cwd, 'server.mjs')] };
  }
  if (!path.isAbsolute(resolved.command)) throw new Error(`Expected the installed, resolved ${kind} manifest`);
  fs.accessSync(resolved.command, fs.constants.X_OK);
  if (kind === 'codex_app') {
    fs.accessSync(path.join(cwd, 'server.mjs'), fs.constants.R_OK);
  } else {
    const command = fs.realpathSync(resolved.command);
    const entry = args[0];
    if (!entry || !path.isAbsolute(entry)) throw new Error('CUA entrypoint is not resolved');
    fs.accessSync(entry, fs.constants.R_OK);
    if (command !== fs.realpathSync(node)) {
      const roots = options.localAppDataRoots ?? [process.env.LOCALAPPDATA, path.join(os.homedir(), 'AppData', 'Local')].filter(Boolean);
      const runtime = path.dirname(path.dirname(command));
      const managed = platform === 'win32' && path.basename(command).toLowerCase() === 'node.exe' &&
        path.basename(path.dirname(command)).toLowerCase() === 'bin' && roots.some(root => {
          const base = path.join(root, 'OpenAI', 'Codex', 'runtimes', 'cua_node');
          if (!fs.existsSync(base)) return false;
          const relative = path.relative(fs.realpathSync(base), runtime);
          return relative !== '' && relative !== '..' && !relative.includes(path.sep) && !path.isAbsolute(relative) && inside(runtime, entry);
        });
      if (!managed) throw new Error('CUA must use bundled Node or one complete Codex-managed runtime');
    }
  }
  const env = { ...resolved.env };
  for (const key of ['CODEX_CLI_PATH', 'CODEX_APP_TOOLS_PIPE_PATH', 'CODEX_TASK_WORKSPACE_VERIFYING_IDENTITY', 'BROWSER_USE_ACCESS_VERIFYING_IDENTITY']) delete env[key];
  return { ...resolved, env };
}
