import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { assertUniqueReleaseVersion } from "./validate-release-identity.mjs";

test("another tag for the same package version is rejected before any asset upload", () => {
  for (const [candidate, existing] of [
    ["v1.2.3-alpha.2", "v1.2.3a2"], ["v1.2.3a2", "v1.2.3-alpha.2"],
    ["v1.2.3-beta.4", "v1.2.3b4"], ["v1.2.3rc1", "v1.2.3-rc.1"],
  ]) {
    for (const draft of [true, false]) {
      assert.throws(() => assertUniqueReleaseVersion(candidate, [
        { tag_name: existing, draft, target_commitish: "different-commit" },
      ]), /already belongs to/, `${candidate}, ${existing}, draft=${draft}`);
    }
  }
});

test("unrelated tags and a retry of the identical tag do not create alias conflicts", () => {
  assert.doesNotThrow(() => assertUniqueReleaseVersion("v1.2.3-alpha.2", [
    { tag_name: "server-2026" }, { tag_name: "v1.2.3-alpha.1" }, { tag_name: "v1.2.3-alpha.2" },
  ]));
  assert.throws(() => assertUniqueReleaseVersion("invalid", []), /Unsupported release tag/);
  assert.throws(() => assertUniqueReleaseVersion("v1.2.3", {}), /release list/);
});

test("release publication serializes across tag aliases and checks identity before creating or uploading", () => {
  const source = readFileSync(new URL("../../.github/workflows/desktop-release.yml", import.meta.url), "utf8");
  const release = source.slice(source.indexOf("\n  release:"));
  assert.match(release, /concurrency:\s*\n\s*group: desktop-release-publish\s*\n\s*cancel-in-progress: false/);
  const check = release.indexOf("node desktop/scripts/validate-release-identity.mjs");
  assert.ok(check > 0 && check < release.indexOf("gh release create"));
  assert.ok(check < release.indexOf("gh release upload"));
});

test("the actual CLI checks every API page and fails closed on API failure", (t) => {
  const root = mkdtempSync(path.join(os.tmpdir(), "lm release identity-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const mockGH = path.join(root, "gh-fixture.mjs");
  const calls = path.join(root, "calls.json");
  writeFileSync(mockGH, `
import assert from 'node:assert/strict';
import childProcess from 'node:child_process';
import { writeFileSync } from 'node:fs';
import { syncBuiltinESMExports } from 'node:module';
childProcess.execFileSync = (command, args, options) => {
  assert.equal(command, 'gh');
  assert.equal(options.encoding, 'utf8');
  writeFileSync(process.env.CALLS, JSON.stringify(args));
  if (process.env.MOCK_FAIL) throw new Error('fixture GitHub API failure');
  return process.env.MOCK_PAGES;
};
syncBuiltinESMExports();
`);
  const run = (pages, fail = "") => execFileSync(process.execPath, [
    "--import", pathToFileURL(mockGH).href,
    fileURLToPath(new URL("./validate-release-identity.mjs", import.meta.url)),
    "--tag", "v1.2.3-alpha.2", "--repository", "fixture/repository",
  ], { stdio: "pipe", env: { ...process.env,
    CALLS: calls, MOCK_PAGES: JSON.stringify(pages), MOCK_FAIL: fail } });
  assert.doesNotThrow(() => run([[{ tag_name: "v1.2.3-alpha.1" }], []]));
  const args = JSON.parse(readFileSync(calls, "utf8"));
  assert.ok(args.includes("--paginate") && args.includes("--slurp"));
  assert.ok(args.includes("repos/fixture/repository/releases?per_page=100"));
  assert.throws(() => run([[{ tag_name: "v1.2.2" }], [{ tag_name: "v1.2.3a2" }]]));
  assert.throws(() => run([], "1"));
  assert.throws(() => run({ message: "not a release list" }));
});
