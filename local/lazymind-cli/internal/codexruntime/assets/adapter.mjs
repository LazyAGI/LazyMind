import { t as forwardDesktop } from "./transport-BdoFawgF.mjs";
import { a as logger, c as delegateCLI, h as processIdentity, i as loadConfig, l as desktopParent, m as privateDirectory, n as configVariable, p as native, r as kinds, t as atomicJSON } from "./config-C_SuSEAC.mjs";
import { n as listenBridge } from "./bridge-transport-BB9eOkVL.mjs";
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { spawn } from "node:child_process";

//#region desktop-runtime/adapter.mjs
const config = loadConfig();
const args = process.argv.slice(2);
const env = {
	...process.env,
	CODEX_CLI_PATH: config.cli,
	CODEX_HOME: config.codexHome,
	...config.helper ? { LAZYMIND_RUNTIME_HELPER: config.helper } : {}
};
const desktop = desktopParent(config);
if (!!!desktop || !args.includes("app-server") || args.some((arg) => [
	"generate-json-schema",
	"generate-ts",
	"daemon",
	"proxy"
].includes(arg))) {
	delete env[configVariable];
	await delegateCLI(config.cli, args, env);
}
const log = logger(config, "adapter");
const root = config.runtimeDir;
const bridgeDir = path.join(root, `bridge-${process.pid}`);
const registration = path.join(root, `bridge-${process.pid}.json`);
const connection = path.join(root, `desktop-${process.pid}.json`);
const children = /* @__PURE__ */ new Set();
const servers = [];
const sockets = /* @__PURE__ */ new Set();
const originalParent = desktop.pid;
const originalParentIdentity = processIdentity(originalParent);
let stopping = false;
let closeTransport;
let parentWatch;
function stop(code = 0) {
	if (stopping) return;
	stopping = true;
	clearInterval(parentWatch);
	closeTransport?.();
	for (const socket of sockets) socket.destroy();
	for (const child of children) child.kill("SIGTERM");
	for (const { server, socketPath } of servers) {
		server.close();
		try {
			if (socketPath) fs.unlinkSync(socketPath);
		} catch {}
	}
	try {
		fs.unlinkSync(connection);
	} catch {}
	try {
		fs.unlinkSync(registration);
	} catch {}
	try {
		fs.rmdirSync(bridgeDir);
	} catch {}
	log("stopped", { code });
	setTimeout(() => process.exit(code), 500);
}
process.once("SIGTERM", () => stop());
process.once("SIGINT", () => stop());
process.stdin.once("end", () => stop());
process.stdin.once("error", () => stop(1));
process.stdout.once("error", () => stop(1));
try {
	log("starting", { desktop: originalParent });
	const backend = native("ensure", { path: config.configFile }, config.helper);
	log("backend_ready", { backend: backend.process.pid });
	if (env.CODEX_APP_TOOLS_PIPE_PATH) {
		privateDirectory(bridgeDir);
		const endpoints = {};
		for (const kind of kinds) {
			const bridge = await listenBridge(bridgeDir, kind, (socket) => {
				sockets.add(socket);
				const plugin = config.plugins[kind];
				const childEnv = {
					...env,
					...plugin.env,
					CODEX_CLI_PATH: config.cli
				};
				for (const name of [
					"CODEX_APP_TOOLS_PIPE_PATH",
					"CODEX_TASK_WORKSPACE_VERIFYING_IDENTITY",
					"BROWSER_USE_ACCESS_VERIFYING_IDENTITY"
				]) {
					delete childEnv[name];
					if (env[name] !== void 0) childEnv[name] = env[name];
				}
				const child = spawn(kind === "codex_app" ? config.node : plugin.command, kind === "codex_app" ? [path.join(plugin.cwd, "server.mjs")] : plugin.args, {
					cwd: plugin.cwd ?? config.codexHome,
					env: childEnv,
					stdio: [
						"pipe",
						"pipe",
						"pipe"
					]
				});
				children.add(child);
				log("plugin_started", {
					kind,
					child: child.pid
				});
				socket.pipe(child.stdin);
				child.stdout.pipe(socket);
				child.stderr.resume();
				child.once("error", (error) => {
					log("plugin_error", {
						kind,
						code: error.code
					});
					socket.destroy();
				});
				child.stdin.on("error", () => socket.destroy());
				child.once("exit", (code, signal) => {
					children.delete(child);
					socket.destroy();
					log("plugin_exited", {
						kind,
						code,
						signal
					});
				});
				socket.on("error", () => {});
				socket.once("close", () => {
					sockets.delete(socket);
					child.stdin.end();
					setTimeout(() => {
						if (children.has(child)) child.kill("SIGTERM");
					}, 1e3).unref();
				});
			});
			const { server } = bridge;
			servers.push(bridge);
			if (bridge.endpoint) endpoints[kind] = bridge.endpoint;
			server.on("error", (error) => {
				log("listener_error", { code: error.code });
				stop(1);
			});
		}
		const group = crypto.createHash("sha256").update(JSON.stringify([
			originalParent,
			originalParentIdentity,
			env.CODEX_APP_TOOLS_PIPE_PATH,
			env.CODEX_TASK_WORKSPACE_VERIFYING_IDENTITY,
			env.BROWSER_USE_ACCESS_VERIFYING_IDENTITY
		])).digest("hex");
		atomicJSON(registration, {
			pid: process.pid,
			ppid: originalParent,
			identity: processIdentity(process.pid),
			parentIdentity: originalParentIdentity,
			dir: bridgeDir,
			group,
			endpoints,
			created: Date.now()
		});
	}
	closeTransport = await forwardDesktop(config, process.stdin, process.stdout, (error) => {
		process.stderr.write(`Shared backend: ${error.message}\n`);
		stop(1);
	});
	if (stopping) closeTransport();
	else {
		atomicJSON(connection, {
			process: native("process", { pid: process.pid }, config.helper),
			desktop: native("process", { pid: originalParent }, config.helper),
			backend: backend.process
		});
		parentWatch = setInterval(() => {
			if (processIdentity(originalParent) !== originalParentIdentity) stop();
		}, 1e3);
	}
} catch (error) {
	log("startup_failed", { code: error.code ?? "startup" });
	process.stderr.write(`Shared backend startup failed; no private fallback: ${error.message}\n`);
	stop(1);
}

//#endregion
export {  };