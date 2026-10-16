import fs from "node:fs";
import path from "node:path";
import { execFileSync, spawn } from "node:child_process";
import net from "node:net";

//#region desktop-runtime/platform.mjs
function native(operation, request, helper = process.env.LAZYMIND_RUNTIME_HELPER) {
	if (!helper || !path.isAbsolute(helper)) throw new Error("Native runtime helper is required; reconnect LazyMind");
	return JSON.parse(execFileSync(helper, [
		"internal",
		"codex-runtime",
		operation
	], {
		input: JSON.stringify(request),
		encoding: "utf8",
		timeout: operation === "ensure" ? 55e3 : 1e4,
		windowsHide: true,
		stdio: [
			"pipe",
			"pipe",
			"pipe"
		]
	}));
}
function checkPrivate(file) {
	const info = fs.lstatSync(file);
	if (info.isSymbolicLink()) throw new Error("Expected a private owned path");
	if (process.platform === "win32") native("check-private", { path: file });
	else if (info.uid !== process.getuid() || info.mode & 63) throw new Error("Expected a private owned path");
	return info;
}
function makePrivate(file) {
	if (process.platform === "win32") native("make-private", { path: file });
	else fs.chmodSync(file, fs.statSync(file).isDirectory() ? 448 : 384);
}
function privateDirectory(dir) {
	const exists = fs.existsSync(dir);
	fs.mkdirSync(dir, {
		recursive: true,
		mode: 448
	});
	if (!exists) makePrivate(dir);
	if (!checkPrivate(dir).isDirectory()) throw new Error("Expected a private directory");
}
function processInfo(pid) {
	if (!Number.isSafeInteger(pid) || pid < 1) return null;
	if (process.platform === "win32") return native("process", { pid });
	try {
		const executable = execFileSync("/bin/ps", [
			"-p",
			String(pid),
			"-o",
			"comm="
		], { encoding: "utf8" }).trim();
		return {
			pid,
			ppid: Number(execFileSync("/bin/ps", [
				"-p",
				String(pid),
				"-o",
				"ppid="
			], { encoding: "utf8" }).trim()),
			executable
		};
	} catch {
		return null;
	}
}
function processIdentity(pid) {
	if (!Number.isSafeInteger(pid) || pid < 1) return null;
	if (process.platform === "win32") return processInfo(pid)?.identity ?? null;
	try {
		return execFileSync("/bin/ps", [
			"-p",
			String(pid),
			"-o",
			"lstart=,comm="
		], {
			encoding: "utf8",
			timeout: 2e3,
			stdio: [
				"ignore",
				"pipe",
				"ignore"
			]
		}).trim() || null;
	} catch {
		return null;
	}
}
function samePath(a, b) {
	return typeof a === "string" && typeof b === "string" && (process.platform === "win32" ? path.resolve(a).toLowerCase() === path.resolve(b).toLowerCase() : a === b);
}
function desktopParent(config) {
	let parent = processInfo(process.ppid);
	if (process.platform === "win32" && samePath(parent?.executable, config.launcher)) parent = processInfo(parent.ppid);
	return samePath(parent?.executable, config.desktopExecutable) ? parent : null;
}
function desktopRunning(config) {
	if (process.platform === "win32") return native("running", { path: config.desktopExecutable });
	return execFileSync("/bin/ps", ["-axo", "comm="], {
		encoding: "utf8",
		timeout: 5e3
	}).split("\n").some((line) => line.trim() === config.desktopExecutable);
}
async function delegateCLI(cli, args, env) {
	if (process.platform !== "win32") process.execve(cli, [cli, ...args], env);
	const child = spawn(cli, args, {
		env,
		stdio: "inherit",
		windowsHide: true
	});
	const forward = () => child.kill();
	process.once("SIGTERM", forward);
	process.once("SIGINT", forward);
	const code = await new Promise((resolve, reject) => {
		child.once("error", reject);
		child.once("exit", (code$1) => resolve(code$1 ?? 1));
	});
	process.exit(code);
}
function runtimeLocation(output, key) {
	return process.platform === "win32" ? path.join(output, "state") : `/private/tmp/lm-codex-${process.getuid()}/${key}`;
}
function launcherName(platform = process.platform) {
	return platform === "win32" ? "launch-adapter.exe" : "launch-adapter";
}
async function allocateLoopbackEndpoint() {
	const server = net.createServer();
	await new Promise((resolve, reject) => {
		server.once("error", reject);
		server.listen(0, "127.0.0.1", resolve);
	});
	const port = server.address().port;
	await new Promise((resolve) => server.close(resolve));
	return `ws://127.0.0.1:${port}`;
}

//#endregion
//#region desktop-runtime/config.mjs
const configVariable = "LAZYMIND_CODEX_DESKTOP_CONFIG";
const kinds = ["codex_app", "cua_repl"];
function loadConfig(file = process.env[configVariable]) {
	if (!file || !path.isAbsolute(file)) throw new Error(`${configVariable} must name an absolute config file`);
	if (!checkPrivate(file).isFile()) throw new Error("Runtime config must be a private owned file");
	const config = JSON.parse(fs.readFileSync(file, "utf8"));
	if (![1, 2].includes(config.version)) throw new Error("Unsupported Desktop runtime config");
	for (const key of [
		"runtimeDir",
		"bundleDir",
		"cli",
		"node",
		"desktopExecutable",
		"codexHome"
	]) if (typeof config[key] !== "string" || !path.isAbsolute(config[key])) throw new Error(`Invalid ${key}`);
	if (config.version === 2 && (!path.isAbsolute(config.helper ?? "") || !path.isAbsolute(config.launcher ?? ""))) throw new Error("Invalid native runtime entrypoint");
	for (const kind of kinds) if (!config.plugins?.[kind]?.command || !Array.isArray(config.plugins[kind].args)) throw new Error(`Missing original ${kind} configuration`);
	if (process.platform === "darwin" && Buffer.byteLength(path.join(config.runtimeDir, "bridge-2147483647", "codex_app.sock")) >= 104) throw new Error("Runtime socket path is too long");
	privateDirectory(config.runtimeDir);
	return {
		...config,
		configFile: file
	};
}
function atomicJSON(file, value) {
	const temporary = `${file}.${process.pid}.tmp`;
	fs.writeFileSync(temporary, JSON.stringify(value, null, 2) + "\n", { mode: 384 });
	makePrivate(temporary);
	fs.renameSync(temporary, file);
}
function logger(config, name) {
	const file = path.join(config.runtimeDir, `${name}.jsonl`);
	return (event, fields = {}) => {
		try {
			if (fs.existsSync(file) && fs.statSync(file).size > 2 * 1024 * 1024) fs.renameSync(file, `${file}.1`);
			fs.appendFileSync(file, JSON.stringify({
				time: (/* @__PURE__ */ new Date()).toISOString(),
				pid: process.pid,
				event,
				...fields
			}) + "\n", { mode: 384 });
		} catch {}
	};
}

//#endregion
export { logger as a, delegateCLI as c, launcherName as d, makePrivate as f, runtimeLocation as g, processIdentity as h, loadConfig as i, desktopParent as l, privateDirectory as m, configVariable as n, allocateLoopbackEndpoint as o, native as p, kinds as r, checkPrivate as s, atomicJSON as t, desktopRunning as u };