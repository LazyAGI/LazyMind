import assert from "node:assert/strict";
import { createRequire } from "node:module";
import net from "node:net";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const require = createRequire(import.meta.url);
const scriptsDir = path.dirname(fileURLToPath(import.meta.url));
const relayModule = path.join(scriptsDir, "..", "electron", "src", "cloud-oauth-callback-relay.js");

function listen(server, host = "127.0.0.1", port = 0) {
  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(port, host, () => {
      server.off("error", reject);
      resolve(server.address());
    });
  });
}

function close(server) {
  return new Promise((resolve, reject) => {
    server.close((error) => error ? reject(error) : resolve());
  });
}

test("localhost OAuth callback relay forwards bytes only through loopback", async () => {
  const upstream = net.createServer((socket) => socket.pipe(socket));
  const upstreamAddress = await listen(upstream);
  const { startCloudOAuthCallbackRelay } = require(relayModule);
  const relay = await startCloudOAuthCallbackRelay({
    baseURL: `https://127.0.0.1:${upstreamAddress.port}`,
    oauthCallbackMode: "localhost-relay",
    oauthCallbackPort: 0,
  });
  try {
    assert.equal(relay.address().address, "127.0.0.1");
    const reply = await new Promise((resolve, reject) => {
      const client = net.connect(relay.address().port, "127.0.0.1", () => client.write("oauth-callback"));
      client.setEncoding("utf8");
      client.once("data", (value) => {
        client.end();
        resolve(value);
      });
      client.once("error", reject);
    });
    assert.equal(reply, "oauth-callback");
  } finally {
    await close(relay);
    await close(upstream);
  }
});

test("direct OAuth callback mode does not open a listener", async () => {
  const { startCloudOAuthCallbackRelay } = require(relayModule);
  assert.equal(await startCloudOAuthCallbackRelay({
    baseURL: "https://cloud.example.com",
    oauthCallbackMode: "direct",
    oauthCallbackPort: 8443,
  }), null);
});
