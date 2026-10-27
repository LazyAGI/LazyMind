import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import test from "node:test";
const { guardDesktopStartup } = createRequire(import.meta.url)("../electron/src/update-startup.js");

test("packaged Electron must pass the fixed synchronous guard before creating identity or starting normal services", () => {
  const calls=[];
  const input={isPackaged:true,sidecarPath:"/fixture/runtime-manager",args:["startup-guard","--profile","desktop","--owner-token","fixture-owner"],env:{FIXTURE:"yes"},invoke:(...args)=>{calls.push(args);return '{"status":"allowed"}';}};
  assert.doesNotThrow(()=>guardDesktopStartup(input));
  assert.equal(calls.length,1);assert.equal(calls[0][0],input.sidecarPath);assert.deepEqual(calls[0][1],input.args);
  assert.equal(calls[0][2].shell,false);assert.ok(calls[0][2].timeout>0 && calls[0][2].timeout<=15000);
  assert.throws(()=>guardDesktopStartup({...input,invoke:()=>'{"status":"blocked"}'}),/update_startup_blocked/);
  assert.throws(()=>guardDesktopStartup({...input,invoke:()=>{throw new Error("PRIVATE path or credential");}}),e=>e.message==="update_startup_blocked");
  guardDesktopStartup({...input,isPackaged:false,invoke:()=>assert.fail("unpackaged renderer invoked packaged guard")});
  const source=readFileSync(new URL("../electron/src/main.js",import.meta.url),"utf8");
  const guard=source.indexOf("guardDesktopStartup({");const identity=source.indexOf("const desktopCredentialIdentity = loadOrCreateDesktopCredentialIdentity()");
  assert.ok(guard>=0 && guard<identity,"direct internal Electron must guard before identity writes");
  assert.match(source,/args: sidecarArgs\(["']startup-guard["']\)/);
});

test("both public launchers consult persistent recovery state before executing Electron",()=>{
  for(const platform of ["darwin","windows"]){
    const source=readFileSync(new URL(`../../local/local-runtime-manager/cmd/desktop-launcher/main_${platform}.go`,import.meta.url),"utf8");
    const guard=source.indexOf("checkPendingDesktopUpdate(launcherPath)");const execute=source.indexOf(platform==="darwin"?"syscall.Exec(":"command.Run()");
    assert.ok(guard>=0 && guard<execute,`${platform} launcher bypasses pending transaction`);
  }
});
