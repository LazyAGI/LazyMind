import { CdpClient } from '../../workflow-agent-core/node/cdp'

export interface CodexTarget { id: string; type: string; url: string; webSocketDebuggerUrl?: string }

export function isCodexChatTarget(target: Pick<CodexTarget,'type'|'url'>) {
  if(target.type!=='page')return false
  try {
    const url=new URL(target.url)
    if(url.protocol!=='app:'||url.host!=='-')return false
    if(url.pathname==='/index.html') {
      const route=url.searchParams.get('initialRoute')
      return route===null || /^\/local\/[a-f0-9-]{36}$/i.test(route)
    }
    return url.pathname==='/detached-window.html' && (url.searchParams.get('initialRoute')??'/detached-window')==='/detached-window'
  } catch{return false}
}

export function selectCodexTarget<T extends {type:string;url:string;id?:string}>(targets: T[], id?: string):T {
  const pages=targets.filter(target=>isCodexChatTarget(target)&&(!id||target.id===id))
  if(pages.length!==1)throw Error(id?'Codex window is no longer available':'Expected one Codex window; select a specific window')
  return pages[0]
}

export async function codexTargets(port:number,signal:AbortSignal):Promise<CodexTarget[]> {
  const response=await fetch(`http://127.0.0.1:${port}/json/list`,{signal:AbortSignal.any([signal,AbortSignal.timeout(3000)]),redirect:'error'})
  if(!response.ok)throw Error('Codex CDP discovery failed')
  return (await response.json() as CodexTarget[]).filter(target=>isCodexChatTarget(target)&&typeof target.id==='string')
}

export function windowCdp(port:number,id:string) {return new CdpClient(port,targets=>selectCodexTarget(targets,id))}
