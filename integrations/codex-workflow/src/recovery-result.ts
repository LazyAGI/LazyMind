// A recovery child has no work left after its final validated receipt. Flush the
// receipt before exiting; unrelated HTTP/WebSocket handles must not hold the UI.
export async function finishRecovery(result: {accepted:boolean}|{ready:boolean}) {
  await new Promise<void>((resolve,reject)=>{
    process.stdout.write(JSON.stringify(result)+'\n',error=>error?reject(error):resolve())
  })
  process.exit(0)
}
