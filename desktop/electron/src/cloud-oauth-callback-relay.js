const nodeNet = require("node:net");

async function startCloudOAuthCallbackRelay(configuration, net = nodeNet) {
  if (configuration?.oauthCallbackMode !== "localhost-relay") {
    return null;
  }
  const upstream = new URL(configuration.baseURL);
  if (upstream.protocol !== "https:") {
    throw new Error("Cloud OAuth callback relay requires an HTTPS upstream");
  }
  const upstreamPort = Number(upstream.port || 443);
  const listenPort = Number(configuration.oauthCallbackPort);
  if (!Number.isInteger(listenPort) || listenPort < 0 || listenPort > 65535) {
    throw new Error("Cloud OAuth callback relay port is invalid");
  }

  const server = net.createServer((downstream) => {
    const upstreamSocket = net.connect({ host: upstream.hostname, port: upstreamPort });
    downstream.on("error", () => upstreamSocket.destroy());
    upstreamSocket.on("error", () => downstream.destroy());
    downstream.pipe(upstreamSocket);
    upstreamSocket.pipe(downstream);
  });
  await new Promise((resolve, reject) => {
    const onError = (error) => {
      server.off("listening", onListening);
      reject(error);
    };
    const onListening = () => {
      server.off("error", onError);
      resolve();
    };
    server.once("error", onError);
    server.once("listening", onListening);
    server.listen(listenPort, "127.0.0.1");
  });
  return server;
}

module.exports = { startCloudOAuthCallbackRelay };
