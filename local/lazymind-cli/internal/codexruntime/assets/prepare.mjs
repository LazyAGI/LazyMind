import { d as launcherName, f as makePrivate, g as runtimeLocation, i as loadConfig, m as privateDirectory, o as allocateLoopbackEndpoint, p as native, t as atomicJSON } from "./config-C_SuSEAC.mjs";
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { execFileSync } from "node:child_process";
import os from "node:os";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";

//#region desktop-runtime/desktop-layout.mjs
const uniqueFile = (candidates, label) => {
	const found = [...new Set(candidates.filter((file) => fs.existsSync(file) && fs.statSync(file).isFile()).map((file) => fs.realpathSync(file)))];
	if (found.length !== 1) throw new Error(`Expected one bundled ${label}; unsupported or ambiguous Desktop layout`);
	return found[0];
};
function desktopLayout(app, platform = process.platform, operations = {
	native,
	execFileSync
}) {
	if (platform === "darwin") {
		const resources$2 = path.join(app, "Contents/Resources");
		const plist = (key$1) => execFileSync("/usr/libexec/PlistBuddy", [
			"-c",
			`Print :${key$1}`,
			path.join(app, "Contents/Info.plist")
		], { encoding: "utf8" }).trim();
		return {
			resources: resources$2,
			cli: path.join(resources$2, "codex-cli/CodexCLI.app/Contents/MacOS/codex"),
			node: path.join(resources$2, "cua_node/bin/node"),
			desktopExecutable: path.join(app, "Contents/MacOS", plist("CFBundleExecutable")),
			desktopVersion: plist("CFBundleShortVersionString")
		};
	}
	if (platform !== "win32") throw new Error("Shared Desktop supports macOS and Windows");
	const resources$1 = path.join(app, "resources");
	const cli$1 = uniqueFile([
		"codex.exe",
		"codex-cli/codex.exe",
		"codex-cli/bin/codex.exe"
	].map((p) => path.join(resources$1, p)), "CLI");
	const node$1 = uniqueFile(["cua_node/node.exe", "cua_node/bin/node.exe"].map((p) => path.join(resources$1, p)), "Node");
	const desktopExecutable$1 = operations.native("windows-desktop-executable", { path: app });
	const powershell = path.join(process.env.SystemRoot ?? "C:\\Windows", "System32/WindowsPowerShell/v1.0/powershell.exe");
	return {
		resources: resources$1,
		cli: cli$1,
		node: node$1,
		desktopExecutable: desktopExecutable$1,
		desktopVersion: operations.execFileSync(powershell, [
			"-NoProfile",
			"-NonInteractive",
			"-Command",
			"[System.Diagnostics.FileVersionInfo]::GetVersionInfo($env:LAZYMIND_VERSION_TARGET).ProductVersion"
		], {
			env: {
				...process.env,
				LAZYMIND_VERSION_TARGET: desktopExecutable$1
			},
			encoding: "utf8",
			windowsHide: true,
			timeout: 1e4
		}).trim()
	};
}

//#endregion
//#region desktop-runtime/plugin-manifest.mjs
function inside(root, file) {
	const relative = path.relative(fs.realpathSync(root), fs.realpathSync(file));
	return relative !== "" && !path.isAbsolute(relative) && relative !== ".." && !relative.startsWith(`..${path.sep}`);
}
function resolvePluginManifest(kind, plugin, file, node$1, options = {}) {
	const platform = options.platform ?? process.platform;
	const cwd = plugin?.cwd ?? path.dirname(file);
	if (!plugin || !Array.isArray(plugin.args) || !plugin.args.every((arg) => typeof arg === "string") || typeof plugin.command !== "string" || !path.isAbsolute(cwd)) throw new Error(`Expected the installed, resolved ${kind} manifest`);
	const args = plugin.args;
	let resolved = {
		...plugin,
		cwd
	};
	if (platform === "win32" && kind === "codex_app" && /^(?:.*[\\/])?cmd\.exe$/i.test(plugin.command)) {
		const wrapper = "./scripts/launch_codex_app_tools_mcp.cmd";
		const command = [
			"call",
			wrapper,
			"./server.mjs"
		];
		const tail = args.slice(3);
		const known = JSON.stringify(tail) === JSON.stringify(command) || tail.length === 1 && tail[0] === command.join(" ");
		if (args.slice(0, 3).map((arg) => arg.toLowerCase()).join(" ") !== "/d /s /c" || !known) throw new Error("Unsupported codex_app cmd.exe wrapper");
		if (!inside(cwd, path.join(cwd, wrapper)) || !inside(cwd, path.join(cwd, "server.mjs"))) throw new Error("codex_app wrapper escapes its plugin directory");
		resolved = {
			...resolved,
			command: node$1,
			args: [path.join(cwd, "server.mjs")]
		};
	}
	if (!path.isAbsolute(resolved.command)) throw new Error(`Expected the installed, resolved ${kind} manifest`);
	fs.accessSync(resolved.command, fs.constants.X_OK);
	if (kind === "codex_app") fs.accessSync(path.join(cwd, "server.mjs"), fs.constants.R_OK);
	else {
		const command = fs.realpathSync(resolved.command);
		const entry = args[0];
		if (!entry || !path.isAbsolute(entry)) throw new Error("CUA entrypoint is not resolved");
		fs.accessSync(entry, fs.constants.R_OK);
		if (command !== fs.realpathSync(node$1)) {
			const roots = options.localAppDataRoots ?? [process.env.LOCALAPPDATA, path.join(os.homedir(), "AppData", "Local")].filter(Boolean);
			const runtime = path.dirname(path.dirname(command));
			if (!(platform === "win32" && path.basename(command).toLowerCase() === "node.exe" && path.basename(path.dirname(command)).toLowerCase() === "bin" && roots.some((root) => {
				const base = path.join(root, "OpenAI", "Codex", "runtimes", "cua_node");
				if (!fs.existsSync(base)) return false;
				const relative = path.relative(fs.realpathSync(base), runtime);
				return relative !== "" && relative !== ".." && !relative.includes(path.sep) && !path.isAbsolute(relative) && inside(runtime, entry);
			}))) throw new Error("CUA must use bundled Node or one complete Codex-managed runtime");
		}
	}
	const env = { ...resolved.env };
	for (const key$1 of [
		"CODEX_CLI_PATH",
		"CODEX_APP_TOOLS_PIPE_PATH",
		"CODEX_TASK_WORKSPACE_VERIFYING_IDENTITY",
		"BROWSER_USE_ACCESS_VERIFYING_IDENTITY"
	]) delete env[key$1];
	return {
		...resolved,
		env
	};
}

//#endregion
//#region desktop-runtime/prepare.mjs
const { values } = parseArgs({ options: {
	output: { type: "string" },
	app: {
		type: "string",
		default: "/Applications/ChatGPT.app"
	},
	"codex-home": {
		type: "string",
		default: process.env.CODEX_HOME ?? path.join(os.homedir(), ".codex")
	},
	"runtime-dir": { type: "string" },
	"desktop-plugin": { type: "string" },
	"browser-plugin": { type: "string" }
} });
if (!["darwin", "win32"].includes(process.platform)) throw new Error("Desktop shared runtime supports macOS and Windows");
if (!values.output) throw new Error("Use --output <new private installation directory>; prepare does not enable shared mode");
const output = path.resolve(values.output);
const configFile = path.join(output, "runtime.json");
if (fs.existsSync(output)) throw new Error("Output already exists; use a new directory to preserve any active installation");
const sourceBundle = path.dirname(fileURLToPath(import.meta.url));
for (const file of [
	"adapter.mjs",
	"mcp-proxy.mjs",
	"health.mjs",
	"manage.mjs"
]) fs.accessSync(path.join(sourceBundle, file), fs.constants.R_OK);
const { resources, cli, node, desktopExecutable, desktopVersion } = desktopLayout(fs.realpathSync(values.app));
const codexHome = fs.realpathSync(values["codex-home"]);
const helper = process.env.LAZYMIND_RUNTIME_HELPER;
if (!helper || !path.isAbsolute(helper)) throw new Error("Install through the LazyMind CLI to provide the native runtime helper");
const cliVersion = execFileSync(cli, ["--version"], {
	encoding: "utf8",
	timeout: 5e3
}).trim().replace(/^codex-cli\s+/, "");
if (cliVersion !== "0.162.0-alpha.17.2") throw new Error(`CLI ${cliVersion} has not been validated for this runtime`);
execFileSync(node, ["-e", "if (process.platform !== \"win32\" && typeof process.execve !== \"function\") process.exit(1)"], { timeout: 5e3 });
if (process.platform === "win32") {
	const help = execFileSync(cli, ["app-server", "--help"], {
		encoding: "utf8",
		windowsHide: true,
		timeout: 1e4
	});
	if (!help.includes("--ws-token-file") || !help.includes("capability-token")) throw new Error("Bundled CLI lacks authenticated WebSocket support");
}
const sourceManifests = {};
function originalPlugin(kind, pluginName, explicit) {
	let file = explicit && path.resolve(explicit);
	if (!file) {
		const cache = path.join(codexHome, "plugins/cache/openai-bundled", pluginName);
		const manifest = path.join(resources, "plugins/openai-bundled/plugins", pluginName, ".codex-plugin/plugin.json");
		const version = JSON.parse(fs.readFileSync(manifest, "utf8")).version;
		if (typeof version !== "string" || path.basename(version) !== version || version === "." || version === "..") throw new Error(`Invalid bundled ${pluginName} version`);
		file = path.join(cache, version, ".mcp.json");
		if (!fs.existsSync(file)) throw new Error(`Open Codex once to initialize ${pluginName}, then reconnect LazyMind`);
	}
	const plugin = JSON.parse(fs.readFileSync(file, "utf8")).mcpServers?.[kind];
	sourceManifests[kind] = file;
	return resolvePluginManifest(kind, plugin, file, node);
}
const plugins = {
	codex_app: originalPlugin("codex_app", "codex-app-tools", values["desktop-plugin"]),
	cua_repl: originalPlugin("cua_repl", "unified-computer-use", values["browser-plugin"])
};
const key = crypto.createHash("sha256").update(output + "\0" + codexHome).digest("hex").slice(0, 16);
const runtimeDir = values["runtime-dir"] ? path.resolve(values["runtime-dir"]) : runtimeLocation(output, key);
privateDirectory(path.dirname(runtimeDir));
privateDirectory(output);
const bundleDir = path.join(output, "runtime");
privateDirectory(bundleDir);
for (const file of fs.readdirSync(sourceBundle).filter((file$1) => file$1.endsWith(".mjs"))) fs.copyFileSync(path.join(sourceBundle, file), path.join(bundleDir, file));
const launcherFile = path.join(output, launcherName());
const installedHelper = path.join(output, process.platform === "win32" ? "runtime-helper.exe" : "runtime-helper");
fs.copyFileSync(helper, installedHelper);
makePrivate(installedHelper);
if (process.platform !== "win32") fs.chmodSync(installedHelper, 448);
process.env.LAZYMIND_RUNTIME_HELPER = installedHelper;
const helperDigest = crypto.createHash("sha256").update(fs.readFileSync(installedHelper)).digest("hex");
let endpoint, tokenFile;
if (process.platform === "win32") {
	privateDirectory(runtimeDir);
	endpoint = await allocateLoopbackEndpoint();
	tokenFile = path.join(runtimeDir, "capability-token");
	fs.writeFileSync(tokenFile, crypto.randomBytes(32).toString("hex"), {
		flag: "wx",
		mode: 384
	});
	makePrivate(tokenFile);
	fs.copyFileSync(helper, launcherFile);
	makePrivate(launcherFile);
} else {
	const quote = (text) => "'" + text.replaceAll("'", "'\\''") + "'";
	const launcher = `#!/bin/sh\nexport LAZYMIND_CODEX_DESKTOP_CONFIG=${quote(configFile)}\nexport LAZYMIND_RUNTIME_HELPER=${quote(installedHelper)}\nexec ${quote(node)} ${quote(path.join(bundleDir, "adapter.mjs"))} "$@"\n`;
	fs.writeFileSync(launcherFile, launcher, { mode: 448 });
}
atomicJSON(configFile, {
	version: 2,
	bundleDir,
	runtimeDir,
	cli,
	node,
	helper: installedHelper,
	launcher: launcherFile,
	codexHome,
	desktopExecutable,
	cliVersion,
	desktopVersion,
	plugins,
	sourceManifests,
	endpoint,
	tokenFile,
	helperDigest
});
loadConfig(configFile);
process.stdout.write(JSON.stringify({
	config: configFile,
	launcher: launcherFile,
	runtimeDir,
	next: [
		node,
		path.join(bundleDir, "manage.mjs"),
		"enable",
		"--config",
		configFile
	],
	enabled: false
}, null, 2) + "\n");

//#endregion
export {  };