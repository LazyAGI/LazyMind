import {expect,it} from 'vitest'
import {spawn} from 'node:child_process'
import {finishRecovery} from '../src/recovery-result'

it('flushes its terminal receipt and exits even if another handle keeps Node alive',async()=>{
 const child=spawn(process.execPath,['-e',`setInterval(()=>{},1000);(${finishRecovery.toString()})({accepted:true})`],{stdio:['ignore','pipe','pipe']})
 let stdout='',stderr=''
 child.stdout.on('data',chunk=>{stdout+=String(chunk)})
 child.stderr.on('data',chunk=>{stderr+=String(chunk)})
 const timer=setTimeout(()=>child.kill(),3000)
 try {
  const code=await new Promise<number|null>((resolve,reject)=>{child.once('close',resolve);child.once('error',reject)})
  expect(stderr).toBe('');expect(code).toBe(0);expect(stdout).toBe('{"accepted":true}\n')
 } finally {clearTimeout(timer);child.kill()}
})
