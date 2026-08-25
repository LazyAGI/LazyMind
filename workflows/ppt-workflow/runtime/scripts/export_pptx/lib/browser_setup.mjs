import { existsSync, lstatSync, readlinkSync, rmSync, symlinkSync, unlinkSync } from 'node:fs';
import { delimiter, isAbsolute, resolve } from 'node:path';
import { createRequire } from 'node:module';
import { execFileSync, execSync } from 'node:child_process';

const require = createRequire(import.meta.url);
const hiddenProcessOptions = { windowsHide: true };
let hiddenHooksInstalled = false;
const patchedModules = new WeakSet();

/** Force child processes created by Playwright internals to stay backgrounded on Windows. */
export function installHiddenProcessHooks() {
  if (hiddenHooksInstalled || process.platform !== 'win32') {
    return;
  }

  hiddenHooksInstalled = true;
  patchChildProcessModule(require('node:child_process'));
  patchChildProcessModule(require('child_process'));
}

function patchChildProcessModule(childProcess) {
  if (patchedModules.has(childProcess)) {
    return;
  }

  patchedModules.add(childProcess);
  const originalSpawn = childProcess.spawn.bind(childProcess);
  childProcess.spawn = (command, argsOrOptions, options) => {
    if (Array.isArray(argsOrOptions)) {
      return originalSpawn(command, argsOrOptions, { ...options, windowsHide: true });
    }
    if (argsOrOptions && typeof argsOrOptions === 'object') {
      return originalSpawn(command, { ...argsOrOptions, windowsHide: true });
    }
    return originalSpawn(command, { windowsHide: true });
  };
}

/** Standard Chromium launch options for background rendering/export. */
export function hiddenChromiumLaunchOptions(options = {}) {
  return {
    headless: true,
    ...options,
    args: [
      ...(options.args || []),
      '--disable-breakpad',
      '--disable-crash-reporter',
    ],
  };
}

/**
 * Ensure the local Node and Playwright dependencies needed by HTML export are
 * available. Missing browser support is surfaced as a normal skipped export by
 * callers instead of crashing the PPT pipeline.
 *
 * Deps (node_modules / linux-sysroot / browsers) may live in a separate install
 * dir pointed at by LAZYMIND_PPT_EXPORT_DEPS (the downloaded ZIP). Exporter
 * source stays in the repo; local runtime symlinks node_modules next to the
 * source so ESM imports resolve, and this helper also reads sysroot from DEPS.
 */
export function ensureDependencies(baseDir) {
  installHiddenProcessHooks();
  const depsDir = resolveDepsDir(baseDir);
  installBundledLinuxLibraryPath(depsDir);
  ensureExporterNodeModulesLink(baseDir);
  const nodeModules = resolve(depsDir, 'node_modules');
  const pptxgenMarker = resolve(nodeModules, 'pptxgenjs');
  const playwrightMarker = resolve(nodeModules, 'playwright');

  if (!existsSync(pptxgenMarker) || !existsSync(playwrightMarker)) {
    console.error('[setup] 首次运行，正在安装 npm 依赖...');
    try {
      execSync('npm install --omit=dev', { ...hiddenProcessOptions, cwd: depsDir, stdio: 'inherit' });
    } catch (e) {
      throw new Error(`npm install failed: ${e.message}. Headless browser environment unavailable.`);
    }
  }

  // Desktop guarantees Electron-as-Node, but not a system `node` or `npx` on
  // PATH. Resolve the bundled browser through the local Playwright module.
  try {
    const playwright = require(resolve(nodeModules, 'playwright'));
    const executable = playwright.chromium.executablePath();
    if (executable && existsSync(executable)) return;
  } catch {
    // Fall through to the local Playwright CLI below.
  }

  console.error('[setup] 正在安装 Playwright Chromium（仅首次）...');
  try {
    const playwrightCli = resolve(nodeModules, 'playwright', 'cli.js');
    execFileSync(process.execPath, [playwrightCli, 'install', 'chromium'], {
      ...hiddenProcessOptions,
      cwd: depsDir,
      env: process.env,
      stdio: 'inherit',
    });
  } catch (e) {
    throw new Error(`Chromium installation failed: ${e.message}. Cannot install headless browser in this environment.`);
  }
}

/** Prefer the installed dependency ZIP dir when local/desktop sets it. */
function resolveDepsDir(baseDir) {
  const fromEnv = String(process.env.LAZYMIND_PPT_EXPORT_DEPS || '').trim();
  if (fromEnv && existsSync(fromEnv)) return resolve(fromEnv);
  return resolve(baseDir);
}

/**
 * Point <exporter>/node_modules at LAZYMIND_PPT_EXPORT_DEPS/node_modules.
 * Needed because ESM resolves packages from the importer's directory tree, and
 * the deps ZIP is installed outside the repo. Never commit this link.
 */
export function ensureExporterNodeModulesLink(baseDir) {
  const depsDir = resolveDepsDir(baseDir);
  const target = resolve(depsDir, 'node_modules');
  const link = resolve(baseDir, 'node_modules');
  if (!existsSync(target)) {
    return;
  }
  try {
    let needsLink = true;
    try {
      const st = lstatSync(link);
      if (st.isSymbolicLink()) {
        let current = readlinkSync(link);
        if (!isAbsolute(current)) {
          current = resolve(baseDir, current);
        }
        if (resolve(current) === resolve(target)) {
          needsLink = false;
        } else {
          unlinkSync(link);
        }
      } else if (st.isDirectory()) {
        // Real local npm install — leave alone.
        needsLink = false;
      } else {
        unlinkSync(link);
      }
    } catch (err) {
      if (err && err.code !== 'ENOENT') {
        try { rmSync(link, { recursive: true, force: true }); } catch { /* ignore */ }
      }
    }
    if (needsLink) {
      // 'junction' works on Windows without admin; ignored as type on Unix.
      symlinkSync(target, link, 'junction');
    }
  } catch (err) {
    console.error(`[setup] failed to link exporter node_modules → ${target}: ${err.message}`);
  }
}

/** Use Chromium runtime libraries bundled for minimal Ubuntu/WSL installs. */
export function installBundledLinuxLibraryPath(baseDir) {
  if (process.platform !== 'linux') return;
  const depsDir = resolveDepsDir(baseDir);
  const candidates = [
    resolve(depsDir, 'linux-sysroot', 'usr', 'lib', 'x86_64-linux-gnu'),
    resolve(depsDir, 'linux-sysroot', 'lib', 'x86_64-linux-gnu'),
  ].filter(existsSync);
  if (candidates.length === 0) return;
  const current = String(process.env.LD_LIBRARY_PATH || '').trim();
  process.env.LD_LIBRARY_PATH = [...candidates, ...(current ? [current] : [])].join(delimiter);
}
