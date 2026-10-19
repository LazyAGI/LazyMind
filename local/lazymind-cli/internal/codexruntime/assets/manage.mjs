import { f as makePrivate, i as loadConfig, p as native, s as checkPrivate, t as atomicJSON, u as desktopRunning } from "./config-C_SuSEAC.mjs";
import { r as runtimeEndpoint } from "./endpoint-BmObwRIt.mjs";
import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";
import os from "node:os";
import { parseArgs } from "node:util";

//#region desktop-runtime/workflow-registration.mjs
function workflowRegistration(config$1) {
	return {
		version: 1,
		profile: config$1.codexHome,
		installation: config$1.configFile,
		app_server_url: runtimeEndpoint(config$1),
		...config$1.tokenFile ? { auth_token_file: config$1.tokenFile } : {}
	};
}
function registrationPath(config$1) {
	return path.join(config$1.codexHome, "lazymind-shared-server.json");
}
function checkOwner(config$1) {
	const file = registrationPath(config$1);
	let info;
	try {
		info = checkPrivate(file);
	} catch (error) {
		if (error.code === "ENOENT") return false;
		throw error;
	}
	if (!info.isFile()) throw new Error("Unsafe Workflow shared-server registration");
	const current$1 = JSON.parse(fs.readFileSync(file, "utf8"));
	if (current$1.installation !== config$1.configFile || current$1.profile !== config$1.codexHome) throw new Error("Another installation owns this profile shared-server registration");
	return true;
}
function registerWorkflow(config$1) {
	checkOwner(config$1);
	atomicJSON(registrationPath(config$1), workflowRegistration(config$1));
}
function unregisterWorkflow(config$1) {
	if (checkOwner(config$1)) fs.unlinkSync(registrationPath(config$1));
}

//#endregion
//#region desktop-runtime/restore-paths.mjs
function restoreLauncherText(text, launcher, cli) {
	const owned = JSON.stringify(launcher);
	const original = JSON.stringify(cli);
	return text.replace(/((?:"CODEX_CLI_PATH"\s*:|^\s*CODEX_CLI_PATH\s*=)\s*)("(?:[^"\\]|\\.)*")/gm, (match, prefix, value) => value === owned ? prefix + original : match);
}
function restoreGeneratedPaths(config$1, launcher) {
	const changed = [];
	for (const file of [path.join(config$1.codexHome, "config.toml"), ...Object.values(config$1.sourceManifests ?? {})]) {
		if (!fs.existsSync(file)) continue;
		const info = fs.lstatSync(file);
		if (!info.isFile() || (process.platform === "win32" ? !checkPrivate(file) : info.uid !== process.getuid())) throw new Error(`Refusing to modify an unowned generated config: ${file}`);
		const before = fs.readFileSync(file, "utf8");
		const after = restoreLauncherText(before, launcher, config$1.cli);
		if (after === before) continue;
		const temporary = `${file}.lazymind-${process.pid}.tmp`;
		fs.writeFileSync(temporary, after, {
			mode: info.mode & 511,
			flag: "wx"
		});
		if (process.platform === "win32") makePrivate(temporary);
		if (fs.readFileSync(file, "utf8") !== before) {
			fs.unlinkSync(temporary);
			throw new Error(`Config changed during restore: ${file}`);
		}
		fs.renameSync(temporary, file);
		changed.push(file);
	}
	return changed;
}

//#endregion
//#region desktop-runtime/activation.mjs
function disableActivation(config$1, stateFile$1, state$1, ops) {
	if (state$1) {
		if (ops.running(config$1)) throw new Error("Quit Codex normally before disconnecting");
		ops.validate(state$1);
		atomicJSON(stateFile$1, {
			...state$1,
			disabling: true
		});
		ops.stop?.();
		ops.set(state$1.before);
		ops.restore();
		ops.remove?.();
		fs.unlinkSync(stateFile$1);
	}
	ops.unregister(config$1);
}

//#endregion
//#region desktop-runtime/manage-windows.mjs
function manageWindows(action$1, config$1, ops = {}) {
	const callNative = ops.native ?? native;
	const register = ops.register ?? registerWorkflow;
	const unregister = ops.unregister ?? unregisterWorkflow;
	const restore = ops.restore ?? restoreGeneratedPaths;
	const running = ops.running ?? desktopRunning;
	const desired$1 = {
		CODEX_CLI_PATH: config$1.launcher,
		CODEX_APP_SERVER_FORCE_CLI: "1",
		CODEX_APP_SERVER_WS_URL: null,
		CODEX_APP_SERVER_USE_LOCAL_DAEMON: null
	};
	const stateFile$1 = path.join(path.dirname(config$1.configFile), "activation.json");
	const state$1 = fs.existsSync(stateFile$1) ? (checkPrivate(stateFile$1), JSON.parse(fs.readFileSync(stateFile$1, "utf8"))) : null;
	if (state$1 && state$1.config !== config$1.configFile) throw new Error("Activation belongs to another installation");
	const current$1 = () => callNative("environment-read", {}, config$1.helper);
	const set$1 = (snapshot) => callNative("environment-write", snapshot, config$1.helper);
	const desiredSnapshot = {
		values: desired$1,
		types: Object.fromEntries(Object.entries(desired$1).filter(([, v]) => v !== null).map(([key]) => [key, 1]))
	};
	const matchesKey = (a, b, key) => a.values[key] === b.values[key] && (a.values[key] === null || a.types[key] === b.types[key]);
	const matches$1 = (a, b) => Object.keys(desired$1).every((key) => matchesKey(a, b, key));
	if (action$1 === "plan") return console.log(JSON.stringify({
		environment: desired$1,
		before: current$1(),
		workflowRegistration: {
			path: registrationPath(config$1),
			value: workflowRegistration(config$1)
		}
	}));
	if (action$1 === "status") return console.log(JSON.stringify({
		enabled: !!state$1,
		environmentMatches: matches$1(current$1(), desiredSnapshot)
	}));
	if (action$1 === "enable") {
		if (state$1) {
			if (state$1.disabling) throw new Error("Finish disconnecting before enabling shared mode again");
			if (!matches$1(current$1(), desiredSnapshot)) throw new Error("Startup environment changed externally");
			register(config$1);
		} else {
			const before = current$1();
			atomicJSON(stateFile$1, {
				config: config$1.configFile,
				before
			});
			let registered = false;
			try {
				register(config$1);
				registered = true;
				set$1(desiredSnapshot);
			} catch (error) {
				set$1(before);
				if (registered) unregister(config$1);
				fs.unlinkSync(stateFile$1);
				throw error;
			}
		}
		console.log("Shared mode configured. Reopen Codex from its original icon; if Explorer retains the old environment, sign out and back in once.");
	} else if (action$1 === "disable") {
		disableActivation(config$1, stateFile$1, state$1, {
			running,
			set: set$1,
			unregister,
			restore: () => {
				const cli = ops.currentCLI ? callNative("validate-windows-cli", { path: ops.currentCLI }, process.env.LAZYMIND_RUNTIME_HELPER ?? config$1.helper) : config$1.cli;
				if (!fs.existsSync(cli)) throw new Error("Current Codex CLI is required to restore generated paths; startup environment has been restored");
				restore({
					...config$1,
					cli
				}, config$1.launcher);
			},
			validate: (state$2) => {
				const now = current$1();
				if (!(state$2.disabling ? Object.keys(desired$1).every((key) => matchesKey(now, desiredSnapshot, key) || matchesKey(now, state$2.before, key)) : matches$1(now, desiredSnapshot) || matches$1(now, state$2.before))) throw new Error("Startup environment changed externally; refusing overwrite");
			}
		});
		console.log("Original user environment restored; backend retained.");
	} else throw new Error("Unsupported Windows activation operation");
}

//#endregion
//#region desktop-runtime/manage.mjs
const { values, positionals } = parseArgs({
	allowPositionals: true,
	options: {
		config: { type: "string" },
		"current-cli": { type: "string" }
	}
});
const action = positionals[0];
if (![
	"plan",
	"enable",
	"disable",
	"status",
	"apply-login"
].includes(action) || positionals.length !== 1) throw new Error("Use plan|enable|disable|status --config <runtime.json>");
if (!["darwin", "win32"].includes(process.platform)) throw new Error("Unsupported platform");
const config = loadConfig(values.config);
if (process.platform === "win32") {
	manageWindows(action, config, { currentCLI: values["current-cli"] });
	process.exit(0);
}
const directory = path.dirname(config.configFile);
const stateFile = path.join(directory, "activation.json");
const agentFile = path.join(os.homedir(), "Library/LaunchAgents/com.lazymind.codex-desktop.plist");
const label = "com.lazymind.codex-desktop";
const domain = `gui/${process.getuid()}`;
const desired = {
	CODEX_CLI_PATH: path.join(directory, "launch-adapter"),
	CODEX_APP_SERVER_FORCE_CLI: "1",
	CODEX_APP_SERVER_WS_URL: null,
	CODEX_APP_SERVER_USE_LOCAL_DAEMON: null
};
const run = (args) => execFileSync("/bin/launchctl", args, {
	encoding: "utf8",
	timeout: 1e4,
	stdio: [
		"ignore",
		"pipe",
		"pipe"
	]
});
const current = () => Object.fromEntries(Object.keys(desired).map((key) => {
	try {
		return [key, run(["getenv", key]).replace(/\n$/, "") || null];
	} catch (error) {
		if (error.status === 1) return [key, null];
		throw error;
	}
}));
const set = (values$1) => {
	for (const [key, value] of Object.entries(values$1)) run(value === null ? ["unsetenv", key] : [
		"setenv",
		key,
		value
	]);
};
const xml = (value) => value.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;").replaceAll("\"", "&quot;");
const plist = `<?xml version="1.0" encoding="UTF-8"?>\n<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">\n<plist version="1.0"><dict><key>Label</key><string>${label}</string><key>ProgramArguments</key><array>${[
	config.node,
	path.join(config.bundleDir, "manage.mjs"),
	"apply-login",
	"--config",
	config.configFile
].map((value) => `<string>${xml(value)}</string>`).join("")}</array><key>RunAtLoad</key><true/><key>KeepAlive</key><false/></dict></plist>\n`;
const matches = (a, b) => Object.keys(desired).every((key) => a[key] === b[key]);
const state = fs.existsSync(stateFile) ? JSON.parse(fs.readFileSync(stateFile, "utf8")) : null;
if (state && state.config !== config.configFile) throw new Error("Activation belongs to another installation");
if (action === "plan") process.stdout.write(JSON.stringify({
	environment: desired,
	before: current(),
	launchAgent: agentFile,
	plist,
	workflowRegistration: {
		path: registrationPath(config),
		value: workflowRegistration(config)
	},
	note: "Preview only; no GUI configuration changed and no App or daemon started."
}, null, 2) + "\n");
else if (action === "status") process.stdout.write(JSON.stringify({
	enabled: !!state,
	environmentMatches: matches(current(), desired),
	runtimeDir: config.runtimeDir,
	backend: fs.existsSync(path.join(config.runtimeDir, "backend.json")) ? JSON.parse(fs.readFileSync(path.join(config.runtimeDir, "backend.json"), "utf8")).process : null,
	note: "Backend is the last recorded identity, not a liveness check. No processes started."
}, null, 2) + "\n");
else if (action === "apply-login") {
	if (!state || state.disabling || !fs.existsSync(agentFile) || fs.readFileSync(agentFile, "utf8") !== plist) throw new Error("No matching activation");
	set(desired);
} else if (action === "enable") {
	fs.accessSync(desired.CODEX_CLI_PATH, fs.constants.X_OK);
	if (state) {
		if (state.disabling) throw new Error("Finish disconnecting before enabling shared mode again");
		if (!fs.existsSync(agentFile) || fs.readFileSync(agentFile, "utf8") !== plist || !matches(current(), desired)) throw new Error("Activation changed externally; inspect before enabling again");
		registerWorkflow(config);
	} else {
		if (fs.existsSync(agentFile)) throw new Error("LaunchAgent already belongs to another installation");
		try {
			run(["print", `${domain}/${label}`]);
			throw new Error("LaunchAgent label is already loaded");
		} catch (error) {
			if (!error.status) throw error;
		}
		const before = current();
		atomicJSON(stateFile, {
			config: config.configFile,
			before
		});
		let registered = false;
		try {
			registerWorkflow(config);
			registered = true;
			fs.mkdirSync(path.dirname(agentFile), { recursive: true });
			fs.writeFileSync(agentFile, plist, {
				mode: 384,
				flag: "wx"
			});
			set(desired);
			run([
				"bootstrap",
				domain,
				agentFile
			]);
		} catch (error) {
			try {
				run(["bootout", `${domain}/${label}`]);
			} catch {}
			set(before);
			if (registered) unregisterWorkflow(config);
			if (fs.existsSync(agentFile) && fs.readFileSync(agentFile, "utf8") === plist) fs.unlinkSync(agentFile);
			fs.unlinkSync(stateFile);
			throw error;
		}
	}
	process.stdout.write("Shared mode configured. Quit Desktop normally, then open its original icon. No App or backend was started by this command.\n");
} else if (action === "disable") {
	disableActivation(config, stateFile, state, {
		running: desktopRunning,
		set,
		unregister: unregisterWorkflow,
		validate: (state$1) => {
			if (fs.existsSync(agentFile) ? fs.readFileSync(agentFile, "utf8") !== plist : !state$1.disabling) throw new Error("LaunchAgent changed externally; refusing removal");
			const now = current();
			if (!(state$1.disabling ? Object.keys(desired).every((key) => now[key] === desired[key] || now[key] === state$1.before[key]) : matches(now, desired) || matches(now, state$1.before))) throw new Error("GUI environment changed externally; refusing to overwrite it");
		},
		stop: () => {
			try {
				run(["bootout", `${domain}/${label}`]);
			} catch (error) {
				if (error.status !== 3 && error.status !== 113) throw error;
			}
		},
		restore: () => restoreGeneratedPaths(config, desired.CODEX_CLI_PATH),
		remove: () => {
			if (fs.existsSync(agentFile)) fs.unlinkSync(agentFile);
		}
	});
	process.stdout.write("Original GUI environment restored; owned generated CLI paths reset. Reopen Desktop normally. The shared backend is left running to preserve any ongoing work.\n");
}

//#endregion
export {  };