import { n as openSocket } from "./transport-BdoFawgF.mjs";
import { i as loadConfig } from "./config-C_SuSEAC.mjs";

//#region desktop-runtime/health.mjs
const config = loadConfig();
const timer = setTimeout(() => {
	process.stderr.write("Backend readiness timed out\n");
	process.exit(1);
}, 7e3);
let socket;
try {
	socket = await openSocket(config);
	const identity = await new Promise((resolve, reject) => {
		socket.on("error", reject);
		socket.on("close", () => reject(/* @__PURE__ */ new Error("Backend closed during readiness check")));
		socket.on("message", (data) => {
			try {
				const message = JSON.parse(data.toString());
				if (message.id === "readiness" && !message.method) if (message.error) reject(/* @__PURE__ */ new Error("Backend initialization rejected"));
				else resolve(message.result);
			} catch (error) {
				reject(error);
			}
		});
		socket.send(JSON.stringify({
			id: "readiness",
			method: "initialize",
			params: {
				clientInfo: {
					name: "lazymind_desktop_readiness",
					version: "1.0.0"
				},
				capabilities: { experimentalApi: true }
			}
		}));
	});
	if (identity?.codexHome !== config.codexHome || !identity?.userAgent?.includes(config.cliVersion)) throw new Error("Backend version or Codex profile does not match");
	socket.send(JSON.stringify({ method: "initialized" }));
	process.stdout.write(JSON.stringify(identity) + "\n");
} catch (error) {
	process.stderr.write(error.message + "\n");
	process.exitCode = 1;
} finally {
	clearTimeout(timer);
	socket?.close();
	setTimeout(() => socket?.terminate(), 250).unref();
}

//#endregion
export {  };