import assert from "node:assert/strict";
import test from "node:test";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const {
  canOpenExternally,
  installExternalNavigationHandler,
  isOAuthPopup,
  isSameOrigin,
} = require("../electron/src/external-navigation.js");

test("recognizes same-origin LazyMind links", () => {
  assert.equal(
    isSameOrigin(
      "http://127.0.0.1:8090/lib/knowledge/list",
      "http://127.0.0.1:8090/agent/chat/home",
    ),
    true,
  );
  assert.equal(
    isSameOrigin("https://open.feishu.cn/app", "http://127.0.0.1:8090/agent/chat/home"),
    false,
  );
});

test("only sends supported external protocols to the operating system", () => {
  assert.equal(canOpenExternally("https://open.feishu.cn/app"), true);
  assert.equal(canOpenExternally("mailto:support@example.com"), true);
  assert.equal(canOpenExternally("javascript:alert(1)"), false);
  assert.equal(canOpenExternally("not a URL"), false);
});

test("recognizes the named, sized OAuth windows used by the frontend", () => {
  assert.equal(isOAuthPopup({ frameName: "Feishu OAuth", features: "width=560,height=760" }), true);
  assert.equal(isOAuthPopup({ frameName: "_blank", features: "" }), false);
});

test("opens ordinary external blank links outside Electron", async () => {
  const opened = [];
  let handler;
  const webContents = {
    getURL: () => "http://127.0.0.1:8090/agent/chat/home",
    setWindowOpenHandler: (value) => { handler = value; },
    on: () => {},
  };

  installExternalNavigationHandler(webContents, async (url) => opened.push(url));
  assert.deepEqual(handler({ url: "https://open.feishu.cn/app", frameName: "_blank" }), {
    action: "deny",
  });
  await Promise.resolve();
  assert.deepEqual(opened, ["https://open.feishu.cn/app"]);
});

test("keeps same-origin links and OAuth popups inside Electron", () => {
  let handler;
  const webContents = {
    getURL: () => "http://127.0.0.1:8090/agent/chat/home",
    setWindowOpenHandler: (value) => { handler = value; },
    on: () => {},
  };

  installExternalNavigationHandler(webContents, async () => {});
  assert.deepEqual(handler({
    url: "http://127.0.0.1:8090/lib/knowledge/list",
    frameName: "_blank",
  }), { action: "allow" });
  assert.deepEqual(handler({
    url: "https://accounts.google.com/o/oauth2/v2/auth",
    frameName: "Google OAuth",
    features: "width=560,height=760,left=100,top=100",
  }), { action: "allow" });
});
