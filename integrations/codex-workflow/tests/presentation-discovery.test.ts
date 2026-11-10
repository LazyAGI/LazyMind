import {afterEach, expect, it, vi} from 'vitest'
import {JSDOM} from 'jsdom'
import {prepareCodexBrowser} from '../src/presentation-surface'

let dom: JSDOM
const manager = {pages:{getWebview(){},disposeHost(){}},getSnapshot(){}}
function fixture(html: string) {
  dom = new JSDOM(html, {url:'app://-/index.html'})
  vi.stubGlobal('window', dom.window)
  vi.stubGlobal('document', dom.window.document)
}
afterEach(() => {dom?.window.close();vi.unstubAllGlobals()})
it('uses the existing preloaded browser module without fetching wrappers', async () => {
  fixture('<link href="app://-/assets/app-initial-old.js">')
  const fetcher = vi.fn(); vi.stubGlobal('fetch', fetcher)
  const load = vi.fn(async (url:string) => url.includes('/app-initial-') ? {minified:manager} : {})
  await prepareCodexBrowser(load)
  expect(fetcher).not.toHaveBeenCalled()
  expect(load).toHaveBeenCalledWith('app://-/assets/app-initial-old.js')
})
it('finds the browser behind index and app-main wrappers without preload links', async () => {
  fixture('<script type="module" src="app://-/assets/index-new.js"></script>')
  const fetcher = vi.fn(async (url:string) => new Response(url.includes('/index-')
    ? 'import("./app-main-new.js")' : 'import{a}from"./app-initial-new.js"'))
  vi.stubGlobal('fetch', fetcher)
  const load = vi.fn(async (url:string) => url.includes('/app-initial-') ? {minified:manager} : {})
  await Promise.all([prepareCodexBrowser(load),prepareCodexBrowser(load)])
  expect(fetcher).toHaveBeenCalledTimes(2)
  expect(load).toHaveBeenCalledTimes(3)
  expect(load).toHaveBeenCalledWith('app://-/assets/app-initial-new.js')
  await prepareCodexBrowser(load)
  expect(fetcher).toHaveBeenCalledTimes(2)
})
it('does not traverse unrelated modules, remote scripts, or cycles and backs off failures', async () => {
  fixture('<script type="module" src="https://example.com/assets/index-bad.js"></script><script type="module" src="app://-/assets/index-local.js"></script>')
  const fetcher = vi.fn(async () => new Response('import("./app-main-local.js");import("./unrelated.js");import("https://example.com/app-initial-bad.js")'))
  vi.stubGlobal('fetch', fetcher)
  const load = vi.fn(async (_url:string) => ({}))
  await expect(prepareCodexBrowser(load)).rejects.toThrow('module is unavailable')
  await expect(prepareCodexBrowser(load)).rejects.toThrow('cooling down')
  expect(fetcher).toHaveBeenCalledTimes(2)
  expect(load.mock.calls.map(([url]) => url)).toEqual([
    'app://-/assets/index-local.js', 'app://-/assets/app-main-local.js',
  ])
})
it('bounds legacy wrapper source size before discovering further modules', async () => {
  fixture('<script type="module" src="app://-/assets/index-new.js"></script>')
  vi.stubGlobal('fetch', vi.fn(async () => new Response('x'.repeat(65537))))
  const load = vi.fn(async () => ({}))
  await expect(prepareCodexBrowser(load)).rejects.toThrow('exceeds discovery limit')
  expect(load).toHaveBeenCalledOnce()
})
