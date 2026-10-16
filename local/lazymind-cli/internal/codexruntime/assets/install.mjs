import { i as loadConfig, m as privateDirectory, s as checkPrivate, t as atomicJSON, u as desktopRunning } from "./config-C_SuSEAC.mjs";
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";

//#region desktop-runtime/installation.mjs
function currentInstallation(profile, ops, requireActive = true) {
	const registration = ops.registration(profile);
	if (!registration) return null;
	const config = ops.config(registration.installation);
	if (config.codexHome !== profile || registration.profile !== profile) throw new Error("Shared installation belongs to another Codex profile");
	if (requireActive && !ops.active(config)) throw new Error("Shared registration has no matching activation; restore it before reconnecting");
	return config;
}
function signature(config, build) {
	return JSON.stringify({
		build,
		cli: config.cli,
		node: config.node,
		platform: process.platform,
		helperDigest: config.helperDigest,
		cliVersion: config.cliVersion,
		desktopVersion: config.desktopVersion,
		desktopExecutable: config.desktopExecutable,
		codexHome: config.codexHome,
		plugins: config.plugins,
		sourceManifests: config.sourceManifests
	});
}
function prepareInstallation({ root, profile, app, build }, ops) {
	const previous = currentInstallation(profile, ops);
	const directory = path.join(root, "installations", crypto.randomUUID());
	let candidate;
	try {
		candidate = ops.prepare(directory, app, profile);
		const fingerprint = crypto.createHash("sha256").update(signature(candidate, build)).digest("hex");
		if (previous && ops.fingerprint(previous) === fingerprint) {
			ops.removePrepared(directory);
			return {
				config: previous.configFile,
				previous: previous.configFile
			};
		}
		if (previous && ops.running(previous)) throw new Error("请先正常退出 Codex，再点击连接完成共享模式升级；当前后台和配置保持不变。");
		ops.mark(candidate, fingerprint);
		return {
			config: candidate.configFile,
			previous: previous?.configFile ?? null
		};
	} catch (error) {
		ops.removePrepared(directory);
		throw error;
	}
}
function activateInstallation(plan, profile, ops) {
	const previous = currentInstallation(profile, ops);
	if ((previous?.configFile ?? null) !== plan.previous) throw new Error("Shared installation changed during setup; reconnect to retry");
	const candidate = ops.config(plan.config);
	if (candidate.codexHome !== profile) throw new Error("Prepared installation belongs to another profile");
	if (plan.config === plan.previous) {
		ops.enable(candidate);
		return;
	}
	if (previous && ops.running(previous)) throw new Error("请先正常退出 Codex，再重试共享模式升级。");
	if (previous) ops.disable(previous);
	try {
		ops.enable(candidate);
	} catch (error) {
		if (previous) try {
			ops.enable(previous);
		} catch (restore) {
			throw new Error(`共享模式升级失败，恢复旧配置也失败。请保留安装目录并重试连接。${error.message}; ${restore.message}`);
		}
		throw error;
	}
}
function disableInstallation(profile, ops) {
	const current = currentInstallation(profile, ops, false);
	if (!current) return;
	if (ops.running(current)) throw new Error("请先正常退出 Codex，再点击断开以恢复原启动方式；不会自动中断当前聊天。");
	ops.disable(current);
}

//#endregion
//#region desktop-runtime/install.mjs
try {
	const { positionals, values } = parseArgs({
		allowPositionals: true,
		options: {
			root: { type: "string" },
			profile: { type: "string" },
			app: { type: "string" },
			build: { type: "string" },
			"current-cli": { type: "string" },
			config: { type: "string" },
			previous: { type: "string" }
		}
	});
	if (!["darwin", "win32"].includes(process.platform)) throw new Error("Shared Desktop installation currently supports macOS and Windows");
	if (!values.profile || !path.isAbsolute(values.profile) || !values.root || !path.isAbsolute(values.root)) throw new Error("Absolute profile and installation root are required");
	const profile = fs.realpathSync(values.profile);
	const bundle = path.dirname(fileURLToPath(import.meta.url));
	const readPrivate = (file) => {
		if (!checkPrivate(file).isFile()) throw new Error(`Expected a private owned file: ${file}`);
		return JSON.parse(fs.readFileSync(file, "utf8"));
	};
	function run(entry, args) {
		try {
			return execFileSync(process.execPath, [path.join(bundle, entry), ...args], {
				encoding: "utf8",
				timeout: 45e3,
				stdio: [
					"ignore",
					"pipe",
					"pipe"
				],
				maxBuffer: 1024 * 1024
			});
		} catch (error) {
			const detail = String(error.stderr || error.message).trim();
			throw new Error(detail.match(/^Error: (.+)$/m)?.[1] ?? detail);
		}
	}
	const ops = {
		registration: (profile$1) => {
			const file = path.join(profile$1, "lazymind-shared-server.json");
			if (!fs.existsSync(file)) return null;
			const record = readPrivate(file);
			if (record.version !== 1 || record.profile !== profile$1 || !path.isAbsolute(record.installation ?? "")) throw new Error("Invalid shared registration");
			return record;
		},
		config: loadConfig,
		active: (config) => {
			const file = path.join(path.dirname(config.configFile), "activation.json");
			return fs.existsSync(file) && readPrivate(file).config === config.configFile;
		},
		running: desktopRunning,
		prepare: (directory, app, profile$1) => {
			privateDirectory(path.dirname(directory));
			run("prepare.mjs", [
				"--output",
				directory,
				"--app",
				app,
				"--codex-home",
				profile$1
			]);
			return loadConfig(path.join(directory, "runtime.json"));
		},
		fingerprint: (config) => {
			const marker = path.join(path.dirname(config.configFile), "installation.json");
			return fs.existsSync(marker) ? readPrivate(marker).fingerprint : null;
		},
		mark: (config, fingerprint) => atomicJSON(path.join(path.dirname(config.configFile), "installation.json"), {
			version: 1,
			fingerprint
		}),
		removePrepared: (directory) => fs.rmSync(directory, {
			recursive: true,
			force: true
		}),
		enable: (config) => run("manage.mjs", [
			"enable",
			"--config",
			config.configFile
		]),
		disable: (config) => run("manage.mjs", [
			"disable",
			"--config",
			config.configFile,
			...values["current-cli"] ? ["--current-cli", values["current-cli"]] : []
		])
	};
	if (positionals.length !== 1) throw new Error("Use prepare|activate|disable");
	let result = {};
	switch (positionals[0]) {
		case "prepare":
			if (!values.app || !path.isAbsolute(values.app) || !values.build) throw new Error("App and build are required");
			result = prepareInstallation({
				root: values.root,
				profile,
				app: values.app,
				build: values.build
			}, ops);
			break;
		case "activate":
			if (!values.config || !path.isAbsolute(values.config)) throw new Error("Prepared configuration is required");
			activateInstallation({
				config: values.config,
				previous: values.previous || null
			}, profile, ops);
			break;
		case "disable":
			disableInstallation(profile, ops);
			break;
		default: throw new Error("Use prepare|activate|disable");
	}
	process.stdout.write(JSON.stringify(result) + "\n");
} catch (error) {
	process.stderr.write(error.message + "\n");
	process.exitCode = 1;
}

//#endregion
export {  };