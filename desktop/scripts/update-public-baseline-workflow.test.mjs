import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import { createRequire } from "node:module";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import vm from "node:vm";

const electron = createRequire(new URL("../electron/package.json", import.meta.url));
const yaml = createRequire(electron.resolve("electron-builder/package.json"))("js-yaml");
const { jobs } = yaml.load(fs.readFileSync(new URL("../../.github/workflows/desktop-update-publish.yml", import.meta.url), "utf8"));

// These publication gates use boolean/string comparisons and Actions status
// functions. Evaluate those exact expressions with the caller's actual inputs.
function enabled(condition, sourceRun, verification = "success") {
  if (condition === undefined) return true;
  return vm.runInNewContext(condition.replace(/^\$\{\{\s*|\s*\}\}$/g, ""), {
    inputs: { use_current_run: true, unsigned_update_test: false, source_build_run_id: sourceRun },
    needs: { verify: { result: verification } }, cancelled: () => false, failure: () => verification === "failure",
  });
}

function expand(value, context) {
  return String(value).replace(/\$\{\{\s*([\w.]+)\s*\}\}/g, (_, name) => {
    const bound = name.split(".").reduce((object, field) => object?.[field], context);
    assert.notEqual(bound, undefined, `Unbound workflow value ${name}`);
    return String(bound);
  });
}

// External build/native tools emit distinguishable evidence for each platform.
// The workflow owns transfer, plan arguments, digest comparisons and their order.
const fixtureTools = `
go() { :; }
uv() {
  while [[ "$1" != python ]]; do shift; done
  shift; local tool="$1" output="" plan="" evidence="" history="" current="" platform="" verify=false
  shift
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --verify-source-only) verify=true; shift; continue ;;
      --output|--output-dir) output="$2" ;;
      --current) current="$2" ;;
      --plan) plan="$2" ;;
      --history) history="$2" ;;
      --evidence) evidence="$2" ;;
      --platform) platform="$2" ;;
      --repo|--baseline|--validator) ;;
      *) return 90 ;;
    esac
    shift 2
  done
  mkdir -p "$output"
  if [[ "$tool" == */verify-update-ci-pairs.py ]]; then
    [[ -f "$plan/expected-policy-sha256" ]] || return 91
    mkdir -p "$output/records/$platform" "$output/evidence/$platform"
    if [[ "$PUBLIC_STATE" != absent ]]; then
      echo "$platform source" > "$output/records/$platform/source"
      echo "$platform passed" > "$output/evidence/$platform/report"
    fi
    cp "$plan/expected-policy-sha256" "$output/$platform-policy-sha256"
  elif [[ "$tool" == */prepare-update-ci-plan.py ]]; then
    if [[ "$verify" != true && "$PUBLIC_STATE" != absent ]]; then
      for platform in macos windows; do
        [[ "$(cat "$history/$platform/source")" == "$platform source" ]] || return 92
        [[ "$(cat "$evidence/$platform/report")" == "$platform passed" ]] || return 93
      done
    fi
    printf '%s\\n' "$PUBLIC_STATE" > "$output/expected-policy-sha256"
    if [[ "$verify" != true ]]; then touch "$RUNNER_TEMP/plan-produced"; fi
  else
    return 94
  fi
}`;

function runShell(step, context, cwd, publicState) {
  const env = { PATH: process.env.PATH, RUNNER_TEMP: context.runner.temp, PUBLIC_STATE: publicState };
  for (const [name, value] of Object.entries(step.env || {})) env[name] = expand(value, context);
  return spawnSync("bash", ["-euc", fixtureTools + "\n" + step.run], { cwd, env, encoding: "utf8" });
}

for (const scenario of ["signed public baseline", "new channel (404)", "stale macos evidence", "stale windows evidence"]) {
  test(`empty source run transfers native evidence through publication: ${scenario}`, { skip: process.platform === "win32" }, t => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "lm-baseline-flow-"));
    t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
    const state = scenario.includes("404") ? "absent" : "1".repeat(64);
    const ctx = { inputs: { use_current_run: true, unsigned_update_test: false, source_build_run_id: "" },
      github: { run_id: "45142962799" }, vars: { LAZYMIND_UPDATE_MODELSCOPE_REPO: "fixture/updates" } };
    const archives = new Map();
    for (const platform of ["macos", "windows"]) {
      const worker = path.join(dir, platform);
      fs.mkdirSync(path.join(worker, "local/local-runtime-manager"), { recursive: true });
      const context = { ...ctx, runner: { temp: worker }, matrix: { platform } };
      for (const step of jobs.verify.steps) {
        if (!enabled(step.if, "")) continue;
        if (step.run?.includes("prepare-update-ci-plan.py")) {
          const r = runShell(step, context, worker, scenario === `stale ${platform} evidence` ? "2".repeat(64) : state);
          assert.equal(r.status, 0, r.stderr);
        }
        if (step.uses?.startsWith("actions/upload-artifact@")) {
          archives.set(expand(step.with.name, context), path.join(worker, expand(step.with.path, context)));
        }
      }
    }
    const work = path.join(dir, "publisher");
    fs.mkdirSync(work);
    const context = { ...ctx, runner: { temp: work } };
    let planning;
    for (const step of jobs.publish.steps) {
      if (step.uses?.startsWith("actions/download-artifact@") && step.with?.name?.includes("desktop-format-evidence")) {
        if (!enabled(step.if, "")) continue;
        const name = expand(step.with.name, context);
        assert.ok(archives.has(name), `publication requested another run's artifact: ${name}`);
        fs.cpSync(archives.get(name), path.join(work, expand(step.with.path, context)), { recursive: true });
      }
      if (step.run?.includes("prepare-update-ci-plan.py") && enabled(step.if, "")) {
        planning = runShell(step, context, work, state);
        break;
      }
    }
    assert.ok(planning, "the current build must prepare a publication plan");
    if (scenario.startsWith("stale")) {
      assert.ok(fs.existsSync(path.join(work, "plan-produced")), "both native artifacts must reach the plan before checking its digest");
      assert.notEqual(planning.status, 0, "a public policy changed after native verification");
    } else {
      assert.equal(planning.status, 0, planning.stderr);
      assert.ok(fs.existsSync(path.join(work, "plan-produced")));
    }
  });
}

test("a current official build still verifies signed history when no GitHub run is available", () => {
  assert.ok(enabled(jobs.verify.if, ""), "when a public policy exists, the native job must verify its latest release");
});

test("automatic promotion never skips the native baseline gate for an existing channel", () => {
  for (const run of ["", "45142962628"]) {
    assert.ok(enabled(jobs.publish.if, run, "success"));
    assert.equal(enabled(jobs.publish.if, run, "failure"), false);
    assert.equal(enabled(jobs.publish.if, run, "skipped"), false, "skipped verification cannot authorize an existing policy");
  }
});

for (const conclusion of ["failure", "success"]) {
  const situation = conclusion === "failure" ? "failed source run" : "expired source artifact";
  test(`the ${situation} does not prevent verification of the public baseline`, { skip: process.platform === "win32" }, t => {
    const run = "45142962628", current = "45142962999";
    const directory = fs.mkdtempSync(path.join(os.tmpdir(), "lm-public-source-"));
    t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
    const step = jobs.verify.steps.find(item => item.run?.includes("gh run download"));
    if (!step || !enabled(step.if, run)) return; // An entirely public source needs no GH artifact.
    const gh = `gh() {
      [[ "$1" == run && "$3" == "$SOURCE_RUN" ]] || return 90
      case "$2" in
        view) printf '%s\\n' "$CONCLUSION" ;;
        download) return 1 ;; # GH has no usable build artifact.
        *) return 91 ;;
      esac
    }`;
    const result = spawnSync("bash", ["-euc", gh + "\n" + step.run], { cwd: directory, encoding: "utf8",
      env: { PATH: process.env.PATH, SOURCE_RUN: run, GITHUB_RUN_ID: current, PLATFORM: "macos", CONCLUSION: conclusion,
        RUNNER_TEMP: directory, GITHUB_STEP_SUMMARY: path.join(directory, "summary") } });
    assert.equal(result.status, 0, "source artifact lookup blocked the job before signed public history could be verified");
  });
}
