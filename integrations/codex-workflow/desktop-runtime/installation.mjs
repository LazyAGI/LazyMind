import path from 'node:path';
import crypto from 'node:crypto';

// The CLI serializes these operations with an OS file lock. Runtime calls are
// injected so upgrade/rollback tests never change the real GUI environment.
export function currentInstallation(profile, ops, requireActive = true) {
  const registration = ops.registration(profile);
  if (!registration) return null;
  const config = ops.config(registration.installation);
  if (config.codexHome !== profile || registration.profile !== profile) throw new Error('Shared installation belongs to another Codex profile');
  if (requireActive && !ops.active(config)) throw new Error('Shared registration has no matching activation; restore it before reconnecting');
  return config;
}

function signature(config, build) {
  return JSON.stringify({ build, cli: config.cli, node: config.node, platform: process.platform, helperDigest: config.helperDigest,
    cliVersion: config.cliVersion, desktopVersion: config.desktopVersion,
    desktopExecutable: config.desktopExecutable, codexHome: config.codexHome,
    plugins: config.plugins, sourceManifests: config.sourceManifests });
}

export function prepareInstallation({ root, profile, app, build }, ops) {
  const previous = currentInstallation(profile, ops);
  const directory = path.join(root, 'installations', crypto.randomUUID());
  let candidate;
  try {
    candidate = ops.prepare(directory, app, profile);
    const fingerprint = crypto.createHash('sha256').update(signature(candidate, build)).digest('hex');
    if (previous && ops.fingerprint(previous) === fingerprint) {
      ops.removePrepared(directory);
      return { config: previous.configFile, previous: previous.configFile };
    }
    if (previous && ops.running(previous)) throw new Error('请先正常退出 Codex，再点击连接完成共享模式升级；当前后台和配置保持不变。');
    ops.mark(candidate, fingerprint);
    return { config: candidate.configFile, previous: previous?.configFile ?? null };
  } catch (error) {
    // This directory has not been activated or used by a backend.
    ops.removePrepared(directory);
    throw error;
  }
}

export function activateInstallation(plan, profile, ops) {
  const previous = currentInstallation(profile, ops);
  if ((previous?.configFile ?? null) !== plan.previous) throw new Error('Shared installation changed during setup; reconnect to retry');
  const candidate = ops.config(plan.config);
  if (candidate.codexHome !== profile) throw new Error('Prepared installation belongs to another profile');
  if (plan.config === plan.previous) { ops.enable(candidate); return; }
  if (previous && ops.running(previous)) throw new Error('请先正常退出 Codex，再重试共享模式升级。');
  if (previous) ops.disable(previous);
  try { ops.enable(candidate); }
  catch (error) {
    if (previous) {
      try { ops.enable(previous); }
      catch (restore) { throw new Error(`共享模式升级失败，恢复旧配置也失败。请保留安装目录并重试连接。${error.message}; ${restore.message}`); }
    }
    throw error;
  }
}

export function disableInstallation(profile, ops) {
  const current = currentInstallation(profile, ops, false);
  if (!current) return;
  if (ops.running(current)) throw new Error('请先正常退出 Codex，再点击断开以恢复原启动方式；不会自动中断当前聊天。');
  ops.disable(current); // Deliberately retain files and backend to preserve running tasks.
}
