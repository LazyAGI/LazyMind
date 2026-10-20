// Read-only inspection through the installed DSH resolver. Never initialize a profile
// or import its plugins: metadata lookup must not run user configuration.
const { createRequire } = require('node:module');
const { readFileSync, existsSync, accessSync, constants, realpathSync } = require('node:fs');
const { join, delimiter, resolve } = require('node:path');
const { pathToFileURL } = require('node:url');
const { spawnSync } = require('node:child_process');
const [resources, home] = process.argv.slice(1);
const runtime = join(resources, 'app.asar', 'dsh');
const req = createRequire(join(runtime, 'package.json'));
const minimum = '0.2.0-rc.2';
const result = { version: '', supported: false, profiles: [] };
const readJSON = path => JSON.parse(readFileSync(path, 'utf8'));
const canonical = path => { try { return realpathSync(path); } catch { return resolve(path); } };
(async () => {
  const semver = req('semver');
  const anchor = join(runtime, 'node_modules/@deepseek-ai/dsh/package.json');
  result.version = readJSON(anchor).version;
  if (!semver.valid(result.version)) throw new Error('DSH version is unreadable');
  result.supported = semver.gte(result.version, minimum);
  if (!result.supported) return;
  const { Context } = await import(pathToFileURL(req.resolve('@deepseek-ai/cordis')));
  const { createRuntimeResolution, PluginPackages } = await import(pathToFileURL(req.resolve('@deepseek-ai/dsh-app-boot')));
  const yaml = req('js-yaml');
  for (const name of ['desktop', 'web']) {
    const dir = join(home, 'profiles', name);
    const item = { name, mcpVersion: '', mcpPath: '' };
    result.profiles.push(item);
    let ctx;
    try {
      if (existsSync(join(dir, 'package.json'))) {
        const manifest = readJSON(join(dir, 'package.json'));
        if (!Array.isArray(manifest.dsh?.profile?.bundles) || !manifest.dsh.profile.bundles.includes('@deepseek-ai/dsh-web-app')) {
          throw new Error('Profile does not include the Web application bundle');
        }
      }
      const profile = { dir, layers: [], skippedBundles: [] };
      const resolution = await createRuntimeResolution({ installAnchor: anchor, home, profile });
      ctx = new Context();
      await ctx.plugin(PluginPackages, { resolution });
      const parent = pathToFileURL(join(dir, 'cordis.yml')).href;
      const pkg = ctx.get('pluginPackages').packageOf('@deepseek-ai/dsh-mcp-client', parent);
      if (!pkg) throw new Error('MCP client is missing');
      item.mcpVersion = pkg.version;
      item.mcpPath = pkg.dir;
      if (!semver.valid(pkg.version) || semver.lt(pkg.version, minimum)) throw new Error('Resolved MCP client predates the supported runtime');
      accessSync(createRequire(parent).resolve('@deepseek-ai/dsh-mcp-client'), constants.R_OK);
      const modulesPath = join(dir, 'node_modules/.modules.yaml');
      if (existsSync(modulesPath)) {
        const modules = yaml.load(readFileSync(modulesPath, 'utf8'));
        if (typeof modules?.storeDir !== 'string') throw new Error('Dependency store record is unreadable');
        const check = spawnSync(process.execPath, ['--expose-internals', resolveDSHPnpm(resources), 'store', 'path'], {
          cwd: dir, encoding: 'utf8', timeout: 5000,
          env: { ...process.env, ELECTRON_RUN_AS_NODE: '1', DSH_DESKTOP_NODE_EXECUTABLE: process.execPath,
            PATH: join(resources, 'runtime/bin') + delimiter + (process.env.PATH || '') },
        });
        if (check.status !== 0) throw new Error('Cannot determine the active dependency store: ' + (check.stdout || check.stderr || check.error || ''));
        const expected = check.stdout.trim();
        if (!expected || canonical(expected) !== canonical(modules.storeDir)) throw new Error('ERR_PNPM_UNEXPECTED_STORE: recorded and active dependency stores differ');
      }
    } catch (error) {
      item.path = dir;
      item.diagnostic = String(error);
    } finally {
      if (ctx) await ctx.fiber.dispose();
    }
  }
})().catch(error => { result.path = resources; result.diagnostic = String(error); }).finally(() => {
  process.stdout.write(JSON.stringify(result) + '\n');
});
