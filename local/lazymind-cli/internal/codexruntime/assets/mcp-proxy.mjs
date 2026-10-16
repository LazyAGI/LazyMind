import { a as logger, h as processIdentity, i as loadConfig, r as kinds, s as checkPrivate } from "./config-C_SuSEAC.mjs";
import { t as connectBridge } from "./bridge-transport-BB9eOkVL.mjs";
import fs from "node:fs";
import path from "node:path";
import readline from "node:readline";

//#region desktop-runtime/bridge-registry.mjs
function selectBridge(root) {
	const records = [];
	for (const name of fs.readdirSync(root).filter((name$1) => /^bridge-\d+\.json$/.test(name$1))) {
		let record;
		try {
			checkPrivate(path.join(root, name));
			record = JSON.parse(fs.readFileSync(path.join(root, name), "utf8"));
		} catch {
			continue;
		}
		const identity = processIdentity(record.pid);
		const parentIdentity = processIdentity(record.ppid);
		if (!identity || !parentIdentity || identity !== record.identity || parentIdentity !== record.parentIdentity) continue;
		if (record.dir !== path.join(root, `bridge-${record.pid}`) || !record.group) throw new Error("Invalid bridge registration");
		records.push(record);
	}
	if (!records.length) throw new Error("No live Desktop bridge; reopen Desktop");
	if (new Set(records.map((record) => record.group)).size !== 1) throw new Error("Ambiguous Desktop contexts; refusing arbitrary routing");
	return records.sort((a, b) => a.created - b.created || a.pid - b.pid)[0];
}

//#endregion
//#region desktop-runtime/mcp-proxy.mjs
const config = loadConfig();
const kind = process.argv[2];
if (!kinds.includes(kind)) throw new Error("Unknown Desktop bridge");
const log = logger(config, `proxy-${kind}`);
const output = (message) => process.stdout.write(JSON.stringify(message) + "\n");
let socket = null, initial = null, initialized = false, connecting = null, generation = 0;
let internal = null;
const pending = /* @__PURE__ */ new Map();
const fail = (message, text) => {
	if (message.id !== void 0 && message.method) output({
		jsonrpc: "2.0",
		id: message.id,
		error: {
			code: -32001,
			message: text
		}
	});
};
async function connect() {
	if (connecting) return connecting;
	if (socket && !socket.destroyed) return;
	connecting = (async () => {
		const bridge = selectBridge(config.runtimeDir);
		const current = connectBridge(bridge, kind);
		const epoch = ++generation;
		let buffer = "";
		current.setEncoding("utf8");
		current.on("data", (chunk) => {
			buffer += chunk;
			if (Buffer.byteLength(buffer) > 32 * 1024 * 1024) {
				current.destroy();
				return;
			}
			let end;
			while ((end = buffer.indexOf("\n")) >= 0) {
				const line = buffer.slice(0, end);
				buffer = buffer.slice(end + 1);
				let message;
				try {
					message = JSON.parse(line);
				} catch {
					current.destroy();
					return;
				}
				if (internal && message.id === internal.id && epoch === internal.epoch && !message.method) {
					const waiter = internal;
					internal = null;
					if (message.error) waiter.reject(/* @__PURE__ */ new Error("Bridge reinitialize failed"));
					else waiter.resolve();
					continue;
				}
				if (socket !== current) continue;
				if (message.id !== void 0 && !message.method) pending.delete(JSON.stringify(message.id));
				output(message);
			}
		});
		current.on("error", (error) => log("socket_error", {
			code: error.code,
			epoch
		}));
		current.on("close", () => {
			if (socket !== current) return;
			socket = null;
			if (internal) {
				internal.reject(/* @__PURE__ */ new Error("Bridge lost during initialize"));
				internal = null;
			}
			log("disconnected", {
				epoch,
				inFlight: pending.size
			});
			for (const request of pending.values()) fail(request, "Desktop bridge disconnected; execution outcome unknown; request was NOT replayed. Verify state before a new request.");
			pending.clear();
		});
		await new Promise((resolve, reject) => {
			const timer = setTimeout(() => {
				current.destroy();
				reject(/* @__PURE__ */ new Error("Bridge connect timeout"));
			}, 5e3);
			current.once("connect", () => {
				clearTimeout(timer);
				resolve();
			});
			current.once("error", (error) => {
				clearTimeout(timer);
				reject(error);
			});
		});
		socket = current;
		log("connected", {
			bridge: bridge.pid,
			desktop: bridge.ppid,
			epoch
		});
		if (initial && initialized) try {
			await new Promise((resolve, reject) => {
				const id = `bridge-reinitialize-${process.pid}-${epoch}`;
				const timer = setTimeout(() => {
					internal = null;
					reject(/* @__PURE__ */ new Error("Bridge initialize timeout"));
				}, 1e4);
				internal = {
					id,
					epoch,
					resolve: () => {
						clearTimeout(timer);
						resolve();
					},
					reject: (error) => {
						clearTimeout(timer);
						reject(error);
					}
				};
				current.write(JSON.stringify({
					...initial,
					id
				}) + "\n");
			});
			current.write(JSON.stringify({
				jsonrpc: "2.0",
				method: "notifications/initialized"
			}) + "\n");
		} catch (error) {
			current.destroy();
			throw error;
		}
	})();
	try {
		await connecting;
	} finally {
		connecting = null;
	}
}
let queue = Promise.resolve();
readline.createInterface({ input: process.stdin }).on("line", (line) => {
	let message;
	try {
		message = JSON.parse(line);
	} catch {
		return;
	}
	queue = queue.then(async () => {
		try {
			if ((!socket || socket.destroyed) && (!message.method || message.method === "notifications/cancelled")) return;
			await connect();
			if (message.method === "initialize") initial = message;
			if (message.method === "notifications/initialized") initialized = true;
			if (message.id !== void 0 && message.method) pending.set(JSON.stringify(message.id), message);
			socket.write(line + "\n");
		} catch (error) {
			fail(message, `Request not sent: ${error.message}`);
		}
	});
}).on("close", () => {
	socket?.destroy();
	process.exit(0);
});
process.on("SIGTERM", () => {
	socket?.destroy();
	process.exit(0);
});
process.stdout.on("error", () => {
	socket?.destroy();
	process.exit(1);
});

//#endregion
export {  };