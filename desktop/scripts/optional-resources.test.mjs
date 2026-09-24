import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, existsSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { stagePdfFont } from './stage-pdf-font.mjs';
import { createHash } from 'node:crypto';

test('desktop font staging emits verified resource and removes only the staged TTF',()=>{
 const root=mkdtempSync(path.join(tmpdir(),'pdf-font-stage-'));
 try {
  const runtime=path.join(root,'runtime'), fonts=path.join(runtime,'app/frontend/dist/fonts');
  mkdirSync(fonts,{recursive:true});
  writeFileSync(path.join(fonts,'NotoSansSC-wght.ttf'),'old');
  writeFileSync(path.join(fonts,'NotoSansSC-OFL.txt'),'license');
  const output=path.join(root,'upload');stagePdfFont(runtime,output);
  const descriptor=JSON.parse(readFileSync(path.join(runtime,'config/pdf-font.json'),'utf8'));
  const bytes=readFileSync(path.join(output,descriptor.filename));
  assert.equal(bytes.length,descriptor.sizeBytes);
  assert.equal(createHash('sha256').update(bytes).digest('hex'),descriptor.sha256);
  assert.equal(existsSync(path.join(fonts,'NotoSansSC-wght.ttf')),false);
  assert.equal(readFileSync(path.join(fonts,'NotoSansSC-OFL.txt'),'utf8'),'license');
  assert.ok(existsSync(new URL('../../frontend/public/fonts/NotoSansSC-wght.ttf',import.meta.url)));
  stagePdfFont(runtime,output);
 } finally {rmSync(root,{recursive:true,force:true});}
});

test('Mac component staging restores the runtime on mismatched Python architecture', {skip:process.platform!=='darwin'}, async()=>{
 const {createRequire}=await import('node:module');
 const require=createRequire(import.meta.url);
 const root=mkdtempSync(path.join(tmpdir(),'mac-component-failure-'));
 const oldStage=process.env.LAZYMIND_DESKTOP_RUNTIME_STAGE,oldMode=process.env.LAZYMIND_DESKTOP_SIGNING_MODE;
 try {
  const runtime=path.join(root,'LazyMind.app/Contents/Resources/runtime');
  const bin=path.join(runtime,'deps/python/algorithm/bin');mkdirSync(bin,{recursive:true});
  writeFileSync(path.join(bin,'python'),`#!/bin/sh\necho ${process.arch==='arm64'?'x86_64':'arm64'}\n`,{mode:0o755});
  process.env.LAZYMIND_DESKTOP_RUNTIME_STAGE=runtime;process.env.LAZYMIND_DESKTOP_SIGNING_MODE='none';
  const config=require('../electron/electron-builder.config.cjs');
  await assert.rejects(config.afterPack({electronPlatformName:'darwin',arch:process.arch==='arm64'?3:1,appOutDir:root}),/does not match native build/);
  assert.ok(existsSync(path.join(bin,'python')));
  assert.equal(existsSync(path.join(root,'.lazymind-runtime-for-signing')),false);
 } finally {
  if(oldStage===undefined) delete process.env.LAZYMIND_DESKTOP_RUNTIME_STAGE;else process.env.LAZYMIND_DESKTOP_RUNTIME_STAGE=oldStage;
  if(oldMode===undefined) delete process.env.LAZYMIND_DESKTOP_SIGNING_MODE;else process.env.LAZYMIND_DESKTOP_SIGNING_MODE=oldMode;
  rmSync(root,{recursive:true,force:true});
 }
});
