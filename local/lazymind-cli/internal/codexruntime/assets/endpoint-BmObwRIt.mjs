import { s as checkPrivate } from "./config-C_SuSEAC.mjs";
import fs from "node:fs";
import path from "node:path";

//#region desktop-runtime/endpoint.mjs
function connectionTarget(endpoint, tokenFile) {
	if (endpoint.startsWith("ws+unix://")) {
		const match = /^ws\+unix:\/\/localhost(\/[^?#]*):\/rpc$/.exec(endpoint);
		if (!match) throw new Error("Invalid local Unix endpoint");
		const socketPath = decodeURIComponent(match[1]);
		if (!path.isAbsolute(socketPath) || /[\x00-\x1f:]/.test(socketPath) || tokenFile) throw new Error("Invalid Unix endpoint");
		return {
			socketPath,
			rpcPath: "/rpc"
		};
	}
	if (!/^ws:\/\/127\.0\.0\.1:([1-9]\d{0,4})$/.test(endpoint) || Number(endpoint.split(":").at(-1)) > 65535 || typeof tokenFile !== "string" || !path.isAbsolute(tokenFile)) throw new Error("Use a local loopback endpoint with a private token file");
	return {
		url: endpoint,
		tokenFile
	};
}
function authorization(tokenFile) {
	if (!checkPrivate(tokenFile).isFile()) throw new Error("Token must be a private file");
	const token = fs.readFileSync(tokenFile, "utf8").trim();
	if (!/^[a-f0-9]{64}$/.test(token)) throw new Error("Invalid capability token");
	return { Authorization: `Bearer ${token}` };
}
function runtimeEndpoint(config) {
	return config.endpoint ?? `ws+unix://localhost${path.join(config.runtimeDir, "daemon.sock").split("/").map(encodeURIComponent).join("/")}:/rpc`;
}

//#endregion
export { connectionTarget as n, runtimeEndpoint as r, authorization as t };