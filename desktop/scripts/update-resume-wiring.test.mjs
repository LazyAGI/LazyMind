import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

test("the Electron updater uses a store scoped to its existing application data directory", () => {
  const main = readFileSync(new URL("../electron/src/main.js", import.meta.url), "utf8");
  assert.match(main, /downloadIntent: createDownloadIntentStore\(path.join\(app.getPath\("userData"\), "update-download-intent.json"\)\)/);
});
