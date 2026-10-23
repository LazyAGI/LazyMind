import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import * as fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import test from "node:test";
import vm from "node:vm";

const repo = fileURLToPath(new URL("../../", import.meta.url));
const source = fs.readFileSync(path.join(repo, "desktop/scripts/build-darwin-arm64.sh"), "utf8");
const builder = fs.readFileSync(path.join(repo, "desktop/electron/electron-builder.config.cjs"), "utf8");
const digest = value => createHash("sha256").update(value).digest("hex");
const install = source.match(/install_github_cli\(\) \{\n[\s\S]*?\n\}/)?.[0];

function staging({ arch = "arm64", badChecksum = false, binaryArch, license = true } = {}) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "github-package-test-"));
  try {
    const tools = path.join(root, "tools");
    const runtime = path.join(root, "runtime");
    fs.mkdirSync(tools); fs.mkdirSync(path.join(runtime, "bin"), { recursive: true });
    fs.mkdirSync(path.join(root, "build"));
    const archive = path.join(root, "fixture.zip");
    const made = spawnSync("python3", ["-c", [
      "import sys,zipfile",
      "with zipfile.ZipFile(sys.argv[1], 'w') as z:",
      " z.writestr('gh_2.102.1_macOS_' + sys.argv[2] + '/bin/gh', 'fixture executable, not gh')",
      " if sys.argv[3] == 'yes': z.writestr('gh_2.102.1_macOS_' + sys.argv[2] + '/LICENSE', 'fixture MIT license')",
    ].join("\n"), archive, arch, license ? "yes" : "no"], { encoding: "utf8" });
    assert.equal(made.status, 0, made.stderr);
    const release = path.join(root, "release.json");
    fs.writeFileSync(release, JSON.stringify({ version: "2.102.1", archive_sha256: {
      [`darwin-${arch}`]: badChecksum ? "0".repeat(64) : digest(fs.readFileSync(archive)),
    } }));
    fs.writeFileSync(path.join(tools, "curl"), `#!/bin/bash
set -euo pipefail
# The real invocation has seven arguments. Refuse all other requests, never access the network.
[[ "$#" == 7 && "$1" == --fail && "$2" == --location && "$3" == --retry && "$4" == 3 && "$5" == "$FIXTURE_URL" && "$6" == --output ]]
cp "$FIXTURE_ARCHIVE" "$7"
`, { mode: 0o755 });
    fs.writeFileSync(path.join(tools, "file"), '#!/bin/sh\nprintf "Mach-O 64-bit executable %s\\n" "$FIXTURE_ARCH"\n', { mode: 0o755 });
    assert.ok(install, "the packaged build must stage GitHub CLI");
    const result = spawnSync("/bin/bash", ["-c", `set -euo pipefail\n${install}\ninstall_github_cli`], {
      env: { ...process.env, PATH: `${tools}:${process.env.PATH}`, GITHUB_CLI_RELEASE: release,
        BUILD_ROOT: path.join(root, "build"), RUNTIME_ROOT: runtime, GO_ARCH: arch,
        HOST_ARCH: arch === "arm64" ? "arm64" : "x86_64", FIXTURE_ARCH: binaryArch || (arch === "arm64" ? "arm64" : "x86_64"),
        FIXTURE_ARCHIVE: archive, FIXTURE_URL: `https://github.com/cli/cli/releases/download/v2.102.1/gh_2.102.1_macOS_${arch}.zip`,
      }, encoding: "utf8",
    });
    return { status: result.status, output: result.stderr, binary: fs.existsSync(path.join(runtime, "bin/gh")),
      sha: fs.existsSync(path.join(runtime, "bin/gh.sha256")) ? fs.readFileSync(path.join(runtime, "bin/gh.sha256"), "utf8").trim() : "",
      bytes: fs.existsSync(path.join(runtime, "bin/gh")) ? fs.readFileSync(path.join(runtime, "bin/gh")) : Buffer.alloc(0),
      license: fs.existsSync(path.join(runtime, "licenses/gh/LICENSE")),
      executable: fs.existsSync(path.join(runtime, "bin/gh")) && Boolean(fs.statSync(path.join(runtime, "bin/gh")).mode & 0o111),
    };
  } finally { fs.rmSync(root, { recursive: true, force: true }); }
}

test("release metadata retains the independently verified official version and both archive checksums", () => {
  assert.deepEqual(JSON.parse(fs.readFileSync(path.join(repo, "backend/auth-service/github-cli-release.json"))), {
    version: "2.102.1", archive_sha256: {
      "darwin-arm64": "08bfef98b50c553df122bb34ee8dcf415cae7d3b108891488964a0df07aa2aeb",
      "darwin-amd64": "91c5fbd493274b545b03ef32a89c2a21238393c0c0b1108210967199b3230d9d",
    },
  });
});
for (const arch of ["arm64", "amd64"]) {
  test(`the actual installer stages executable, matching digest and license for ${arch}`, () => {
    const result = staging({ arch });
    assert.equal(result.status, 0, result.output);
    assert.ok(result.binary && result.license && result.executable);
    assert.equal(result.sha, digest(result.bytes));
  });
}
test("wrong archive checksum stops before installing an executable", () => {
  const result = staging({ badChecksum: true });
  assert.notEqual(result.status, 0); assert.equal(result.binary, false);
});
test("wrong CPU architecture stops before installing an executable", () => {
  const result = staging({ binaryArch: "x86_64" });
  assert.notEqual(result.status, 0); assert.equal(result.binary, false);
});
test("missing bundled license fails the build", () => { assert.notEqual(staging({ license: false }).status, 0); });

for (const failure of [false, true]) {
  test(`Developer ID signing ${failure ? "failure blocks staging" : "refreshes digest after binary mutation"}`, async () => {
    const root = fs.mkdtempSync(path.join(os.tmpdir(), "github-signing-test-"));
    try {
      const binary = path.join(root, "LazyMind.app/Contents/Resources/runtime/bin/gh");
      fs.mkdirSync(path.dirname(binary), { recursive: true });
      fs.writeFileSync(binary, "fixture unsigned executable");
      fs.writeFileSync(`${binary}.sha256`, digest(fs.readFileSync(binary)));
      const fn = builder.match(/async function signAndStageEmbeddedRuntime\(context\) \{[\s\S]*?\n\}/)?.[0];
      assert.ok(fn);
      let staged = false;
      const context = {
        path, fs, createHash, process: { arch: "arm64" }, console: { log() {} }, macSigningMode: "developer-id",
        collectRuntimeMachOBinaries: () => [binary],
        developerIdSigningContext: async () => ({ identity: "fixture identity", entitlements: "fixture.plist" }),
        codesignWithRetry: async (_args, target) => {
          assert.equal(target, binary);
          if (failure) throw new Error("fixture signing rejected");
          fs.appendFileSync(target, " fixture signature");
        },
        splitPythonComponents: async () => {},
        stageEmbeddedRuntime: () => { staged = true; assert.equal(fs.readFileSync(`${binary}.sha256`, "utf8").trim(), digest(fs.readFileSync(binary))); },
      };
      vm.createContext(context);
      vm.runInContext(`${fn}\nthis.run = signAndStageEmbeddedRuntime;`, context);
      const run = context.run({ electronPlatformName: "darwin", arch: 3, appOutDir: root });
      if (failure) { await assert.rejects(run, /signing rejected/); assert.equal(staged, false); }
      else { await run; assert.equal(staged, true); }
    } finally { fs.rmSync(root, { recursive: true, force: true }); }
  });
}

test("final ad-hoc deep signing refreshes the digest before sealing the outer app", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "github-adhoc-test-"));
  try {
    const app = path.join(root, "LazyMind.app");
    const binary = path.join(app, "Contents/Resources/runtime/bin/gh");
    fs.mkdirSync(path.dirname(binary), { recursive: true });
    fs.writeFileSync(binary, "fixture executable");
    fs.writeFileSync(`${binary}.sha256`, digest(fs.readFileSync(binary)));
    const tools = path.join(root, "tools"); fs.mkdirSync(tools);
    fs.writeFileSync(path.join(tools, "codesign"), `#!/bin/bash
set -euo pipefail
if [[ "$*" == *--deep* ]]; then
  printf ' fixture signature' >> "$APP_PATH/Contents/Resources/runtime/bin/gh"
else
  expected="$(shasum -a 256 "$APP_PATH/Contents/Resources/runtime/bin/gh" | awk '{print $1}')"
  [[ "$(cat "$APP_PATH/Contents/Resources/runtime/bin/gh.sha256")" == "$expected" ]]
fi
`, { mode: 0o755 });
    const block = source.match(/    codesign --force --deep --sign - "\$\{APP_PATH\}"\n[\s\S]*?    codesign --force --sign - "\$\{APP_PATH\}"/)?.[0];
    assert.ok(block, "final ad-hoc sealing block must exist");
    const result = spawnSync("/bin/bash", ["-c", `set -euo pipefail\n${block}`], {
      env: { ...process.env, PATH: `${tools}:${process.env.PATH}`, APP_PATH: app }, encoding: "utf8",
    });
    assert.equal(result.status, 0, result.stderr);
    assert.equal(fs.readFileSync(`${binary}.sha256`, "utf8").trim(), digest(fs.readFileSync(binary)));
  } finally { fs.rmSync(root, { recursive: true, force: true }); }
});

test("the real macOS build invokes staging before the runtime manifest is produced", () => {
  assert.match(source, /GITHUB_CLI_RELEASE="\$\{ROOT\}\/backend\/auth-service\/github-cli-release\.json"/);
  const invocation = source.indexOf("\ninstall_github_cli\n");
  assert.ok(invocation > source.indexOf("install_github_cli()"));
  assert.ok(invocation < source.indexOf('node "${ROOT}/desktop/scripts/write-runtime-manifest.mjs"'));
});
