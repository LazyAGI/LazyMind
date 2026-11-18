import assert from 'node:assert/strict';
import test from 'node:test';
import fs from 'node:fs';
import vm from 'node:vm';
import { createRequire } from 'node:module';
import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';
const require = createRequire(import.meta.url);
const { runRecoveryCommand } = require('../electron/src/agent-recovery');
const { createUpdateRuntimeClient } = require('../electron/src/update-runtime-client');
const source = fs.readFileSync(new URL('../electron/src/main.js', import.meta.url), 'utf8');
const tick = () => new Promise(resolve => setImmediate(resolve));
const gate = source.slice(source.indexOf('let desktopUpdateAdmission ='), source.indexOf('function stopDesktopUpdateChild('));
const recovery = source.slice(source.indexOf('const agentRecovery = createAgentRecovery('), source.indexOf('let recoverySessionIdentity;'));
const update = source.slice(source.indexOf('const updateRuntime = createUpdateRuntimeClient('), source.indexOf('const desktopUpdater = createDesktopUpdater('));
function context() {
 const children = [], calls = [];
 const c = { module:{exports:{}}, createUpdateRuntimeClient, runRecoveryCommand,
  BrowserWindow:{},ipcMain:{handle(){}},screen:{},app:{getLocale:()=> 'en'},
  createAgentRecovery: options => options, runConnectorJSON: async()=>({}),
  sidecarEnv:()=>({}),agentConnectorPath:'fixture',appendStartupLog(){},
  spawn:()=>{const p=Object.assign(new EventEmitter(),{stdout:new PassThrough(),stderr:new PassThrough(),kill(){}});children.push(p);return p;},
  runtimeStopping:false,isQuitting:false,runtimeRestartPromise:null,inputRecorder:{isActive:()=>false},nativeRecording:null,
  agentHostRestartTimer:null,agentHostStableTimer:null,clearTimeout,managedBrowser:{clear:async()=>{}},
  agentHostProcess:null,runtimeProcess:null,
  stopDesktopUpdateChild:async()=>{},
  runSidecar:async(_,args)=>{calls.push(args[1]);return JSON.stringify(args[1]==='stop'?{status:'stopped'}:{paused:true,epoch:Number(args[3]),active:0});},
 };
 vm.runInNewContext(gate+recovery+update+'\nmodule.exports={recovery:agentRecovery,runtime:updateRuntime,get work(){return desktopUpdateWork},set admitted(v){desktopUpdateAdmission=v}}',c);
 return {c, api:c.module.exports, children,calls};
}
test('actual main reconnect counts work and rejects a new reconnect while update admission is paused',async t=>{
 const f=context(); t.after(()=>{for(const child of f.children){child.stdout.write('{"stage":"done"}\n');child.emit('close',0);}}); const work=f.api.recovery.reconnect('fixture',()=>{},new AbortController().signal);
 await tick(); assert.equal(f.api.work,1);
 await f.api.runtime.pause(7);
 await assert.rejects(f.api.runtime.stop(7),{code:"tasks_active"});
 await assert.rejects(f.api.recovery.reconnect('second',()=>{},new AbortController().signal),{code:'update_preparing'});
 assert.equal(f.children.length,1);
 f.children[0].stdout.write('{"stage":"done"}\n');f.children[0].emit('close',0);await work;assert.equal(f.api.work,0);
});
test('actual update shutdown waits for recovery ownership and prevents later recovery starts',async()=>{
 const f=context();let finish;f.api.recovery.stop=()=>new Promise(resolve=>{finish=resolve});
 await f.api.runtime.pause(7);const stopping=f.api.runtime.stop(7);await tick();
 assert.ok(!f.calls.includes('stop'));assert.equal(f.api.recovery.canStart(),false);
 finish();await stopping;assert.ok(f.calls.includes('stop'));
});
test('recovery JSON commands keep ownership until the child closes even after an abort callback',async()=>{
 const code=source.slice(source.indexOf('function runConnectorJSON('),source.indexOf('const agentRecovery ='));
 let callback,settled=false;const child=Object.assign(new EventEmitter(),{stdin:{end(){}}});
 const c={module:{exports:{}},agentConnectorPath:'fixture',sidecarEnv:()=>({}),isWindows:false,
  execFile:(_path,_args,_options,cb)=>{callback=cb;return child;}};
 vm.runInNewContext(code+'\nmodule.exports=runConnectorJSON;',c);
 const p=c.module.exports([],1000,undefined,{waitForClose:true}).then(()=>{settled=true},()=>{settled=true});
 callback(new Error('aborted'),'','');await tick();assert.equal(settled,false);
 child.emit('close');await p;assert.equal(settled,true);
});
