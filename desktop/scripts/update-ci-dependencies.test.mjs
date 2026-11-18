import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

for (const workflow of ["ci", "macos-installer", "windows-installer"]) {
  test(`${workflow} prepares runtime manager modules before offline desktop tests`, () => {
    let source = readFileSync(new URL(`../../.github/workflows/${workflow}.yml`, import.meta.url), "utf8");
    if (workflow === "ci") source = source.split("\n  test-desktop-shell:")[1].split("\n  test-auth-service:")[0];
    const setup = source.indexOf("uses: actions/setup-go@");
    const prepare = source.indexOf("- name: Prepare update-helper test dependencies");
    const tests = source.indexOf(workflow === "ci" ? "- name: Test Desktop shell" : "- name: Run Desktop tests");
    assert.ok(setup >= 0 && prepare > setup && tests > prepare, "Go setup and dependency preparation must precede offline helper compilation");
    const step = source.slice(prepare, source.indexOf("\n      - ", prepare + 1));
    assert.match(step, /working-directory: local\/local-runtime-manager\s*\n\s*run: go list -mod=readonly -deps \.\/cmd\/update-helper > \/dev\/null/);
    assert.doesNotMatch(step, /\n\s*if:|continue-on-error:|GOPROXY:\s*["']?off/);
    if (workflow === "ci") {
      const go = source.slice(setup, source.indexOf("\n      - ", setup + 1));
      assert.doesNotMatch(go, /\n\s*if:/, "every desktop matrix platform needs Go for the offline fixture");
    }
  });
}
