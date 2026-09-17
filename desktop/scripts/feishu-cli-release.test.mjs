import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync, existsSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import test from "node:test";

const source = readFileSync(new URL("./build-darwin-arm64.sh", import.meta.url), "utf8");
const installer = source.slice(source.indexOf("install_feishu_cli() {"), source.indexOf("\nmake_internal_symlinks_relative()"));
const digest = (data) => createHash("sha256").update(data).digest("hex");

for (const badChecksum of [null, "archive", "license"]) {
  test(`macOS installer reads the manifest and enforces checksums (${badChecksum || "valid"})`, () => {
    const root = mkdtempSync(path.join(tmpdir(), "feishu-release-"));
    try {
      const tools = path.join(root, "tools"), payload = path.join(root, "payload"), runtime = path.join(root, "runtime");
      for (const dir of [tools, payload, path.join(runtime, "bin")]) mkdirSync(dir, { recursive: true });
      const binary = "#!/bin/sh\nprintf 'fixture CLI\\n'\n";
      writeFileSync(path.join(payload, "lark-cli"), binary, { mode: 0o755 });
      const archive = path.join(root, "fixture.tar.gz"), license = path.join(root, "LICENSE");
      assert.equal(spawnSync("tar", ["-czf", archive, "-C", payload, "lark-cli"]).status, 0);
      writeFileSync(license, "fixture license\n");
      const manifest = path.join(root, "release.json");
      writeFileSync(manifest, JSON.stringify({ version: "fixture-version", archive_sha256: { "darwin-arm64": badChecksum === "archive" ? "0".repeat(64) : digest(readFileSync(archive)) }, license_sha256: badChecksum === "license" ? "0".repeat(64) : digest(readFileSync(license)) }));
      writeFileSync(path.join(tools, "curl"), `#!/bin/sh
while [ "$#" -gt 0 ]; do
  case "$1" in --output) output="$2"; shift 2 ;; https:*) url="$1"; shift ;; *) shift ;; esac
done
case "$url" in */LICENSE) cp "$FIXTURE_LICENSE" "$output" ;; *.tar.gz) cp "$FIXTURE_ARCHIVE" "$output" ;; *) exit 1 ;; esac
`, { mode: 0o755 });
      const run = spawnSync("bash", ["-c", `set -euo pipefail\n${installer}\ninstall_feishu_cli`], {
        encoding: "utf8", env: { ...process.env, PATH: `${tools}:${process.env.PATH}`, BUILD_ROOT: root, RUNTIME_ROOT: runtime, FEISHU_CLI_RELEASE: manifest, FIXTURE_LICENSE: license, FIXTURE_ARCHIVE: archive },
      });
      if (badChecksum) {
        assert.notEqual(run.status, 0, run.stdout + run.stderr);
        if (badChecksum === "archive") assert.equal(existsSync(path.join(runtime, "bin/lark-cli")), false);
      } else {
        assert.equal(run.status, 0, run.stdout + run.stderr);
        assert.match(run.stdout, /fixture-version/);
        assert.equal(readFileSync(path.join(runtime, "bin/lark-cli"), "utf8"), binary);
        assert.equal(readFileSync(path.join(runtime, "bin/lark-cli.sha256"), "utf8").trim(), digest(binary));
      }
    } finally { rmSync(root, { recursive: true, force: true }); }
  });
}
