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
  assert.equal(
    canOpenExternally("cursor://anysphere.cursor-deeplink/mcp/install?name=lazymind&config=e30%3D"),
    true,
  );
  assert.equal(canOpenExternally("cursor://anysphere.cursor-deeplink/settings"), false);
  assert.equal(canOpenExternally("cursor://untrusted.example/mcp/install"), false);
  assert.equal(canOpenExternally("javascript:alert(1)"), false);
  assert.equal(canOpenExternally("not a URL"), false);
});

test("recognizes the named, sized OAuth windows used by the frontend", () => {
  assert.equal(isOAuthPopup({ frameName: "Feishu OAuth", features: "width=560,height=760" }), true);
  assert.equal(isOAuthPopup({ frameName: "_blank", features: "" }), false);
});

test("opens Obsidian notes without allowing write actions or callbacks", () => {
  const url = "obsidian://open?path=%2FUsers%2Ftest%2FVault%2Fnote.md";
  assert.equal(canOpenExternally(url), true);
  for (const invalid of ["obsidian://new?path=%2Ftmp%2Fnote.md", "obsidian://vlt_fixture/note.md", "obsidian://open?path=relative.md", `${url}&append=changed`, `${url}&x-success=https://example.test`, "obsidian://user@open?path=%2Ftmp%2Fnote.md"]) {
    assert.equal(canOpenExternally(invalid), false, invalid);
  }
  let handler;
  const opened = [];
  installExternalNavigationHandler({ getURL: () => "http://127.0.0.1:8090/agent/chat/home", setWindowOpenHandler: value => { handler = value; }, on: () => {} }, async target => { opened.push(target); });
  assert.deepEqual(handler({ url, frameName: "_blank" }), { action: "deny" });
  assert.deepEqual(opened, [url]);
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

test("gives same-origin windows the desktop preload without changing OAuth popups", () => {
  let handler;
  const sameOriginWindowOptions = {
    webPreferences: { preload: "/runtime/preload.js" },
  };
  const webContents = {
    getURL: () => "http://127.0.0.1:8090/agent/chat/home",
    setWindowOpenHandler: (value) => { handler = value; },
    on: () => {},
  };

  installExternalNavigationHandler(
    webContents,
    async () => {},
    () => {},
    sameOriginWindowOptions,
  );
  assert.deepEqual(handler({
    url: "http://127.0.0.1:8090/lib/knowledge/list",
    frameName: "_blank",
  }), {
    action: "allow",
    overrideBrowserWindowOptions: sameOriginWindowOptions,
  });
  assert.deepEqual(handler({
    url: "https://accounts.google.com/o/oauth2/v2/auth",
    frameName: "Google OAuth",
    features: "width=560,height=760,left=100,top=100",
  }), { action: "allow" });
});

test("only propagates desktop window options through same-origin children", () => {
  let didCreateWindow;
  const sameOriginWindowOptions = {
    webPreferences: { preload: "/runtime/preload.js" },
  };
  const parentWebContents = {
    getURL: () => "http://127.0.0.1:8090/agent/chat/home",
    setWindowOpenHandler: () => {},
    on: (event, listener) => {
      if (event === "did-create-window") didCreateWindow = listener;
    },
  };

  installExternalNavigationHandler(
    parentWebContents,
    async () => {},
    () => {},
    sameOriginWindowOptions,
  );

  let sameOriginHandler;
  didCreateWindow({
    webContents: {
      getURL: () => "http://127.0.0.1:8090/cloud-documents/docs/github-setup",
      setWindowOpenHandler: (value) => { sameOriginHandler = value; },
      on: () => {},
    },
  }, { url: "http://127.0.0.1:8090/cloud-documents/docs/github-setup" });
  assert.deepEqual(sameOriginHandler({
    url: "http://127.0.0.1:8090/cloud-documents",
    frameName: "_blank",
  }), {
    action: "allow",
    overrideBrowserWindowOptions: sameOriginWindowOptions,
  });

  let oauthHandler;
  didCreateWindow({
    webContents: {
      getURL: () => "https://accounts.google.com/o/oauth2/v2/auth",
      setWindowOpenHandler: (value) => { oauthHandler = value; },
      on: () => {},
    },
  }, { url: "https://accounts.google.com/o/oauth2/v2/auth" });
  assert.deepEqual(oauthHandler({
    url: "https://accounts.google.com/signin",
    frameName: "_blank",
  }), { action: "allow" });
});
