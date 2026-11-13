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

it('finds the initialized browser in a loaded Rspack runtime without executing factories', async () => {
  fixture('<script type="module" src="app://-/assets/782259.7a3e865ee5.js"></script><script type="module" src="app://-/assets/348410.fcfef000d0.js"></script>')
  const browserFactory = function () { return 'getWebview disposeHost getSnapshot' }
  const unrelatedGetter = vi.fn(() => { throw Error('must not read unrelated getters') })
  const browserGetter = vi.fn(() => manager)
  const namespace = Object.defineProperty({}, 'minified', {get: browserGetter, enumerable:true})
  const runtime = Object.assign(vi.fn(), {
    c: {unrelated: {exports:Object.defineProperty({}, 'value', {get:unrelatedGetter})}, browser:{exports:namespace}},
    m: {unrelated: () => {}, browser:browserFactory, unloaded:browserFactory},
  })
  const fetcher = vi.fn(); vi.stubGlobal('fetch', fetcher)
  const load = vi.fn(async (url:string) => url.includes('/348410.') ? {__webpack_require__:runtime} : {__webpack_modules__:{}})
  await prepareCodexBrowser(load)
  expect((window as any).__lazymindCodexBrowser).toBe(manager)
  expect(load).toHaveBeenCalledTimes(2)
  expect(browserGetter).toHaveBeenCalledOnce()
  expect(unrelatedGetter).not.toHaveBeenCalled()
  expect(runtime).not.toHaveBeenCalled()
  expect(fetcher).not.toHaveBeenCalled()
})

it('ignores remote numeric entries and uninitialized browser factories', async () => {
  fixture('<script type="module" src="https://example.com/assets/123.abc.js"></script><script type="module" src="app://-/assets/123.abc.js"></script>')
  const runtime = Object.assign(vi.fn(), {c:{},m:{browser:()=>'getWebview disposeHost getSnapshot'}})
  const load = vi.fn(async () => ({__webpack_require__:runtime}))
  await expect(prepareCodexBrowser(load)).rejects.toThrow('module is unavailable')
  expect(load).toHaveBeenCalledOnce()
  expect(load).toHaveBeenCalledWith('app://-/assets/123.abc.js')
  expect(runtime).not.toHaveBeenCalled()
})

it('keeps legacy discovery available when a numeric entry cannot be imported', async () => {
  fixture('<script type="module" src="app://-/assets/123.stale.js"></script><link href="app://-/assets/app-initial-old.js">')
  const load = vi.fn(async (url:string) => {
    if (url.includes('/123.')) throw Error('entry replaced')
    return {minified:manager}
  })
  await prepareCodexBrowser(load)
  expect((window as any).__lazymindCodexBrowser).toBe(manager)
  expect(load).toHaveBeenCalledTimes(2)
})

it('bounds cached factory source inspection and backs off unavailable discovery', async () => {
  fixture('<script type="module" src="app://-/assets/123.abc.js"></script>')
  const factory = Function('return "' + 'x'.repeat(1024 * 1024) + '"')
  const large = Object.fromEntries(Array.from({length:34},(_,i)=>['large'+i,factory]))
  const browser = () => 'getWebview disposeHost getSnapshot'
  const getter = vi.fn(() => manager)
  const runtime = Object.assign(vi.fn(), {
    c: {...Object.fromEntries(Object.keys(large).map(id=>[id,{exports:{}}])),browser:{exports:Object.defineProperty({},'value',{get:getter})}},
    m: {...large,browser},
  })
  const load = vi.fn(async () => ({__webpack_require__:runtime}))
  await expect(prepareCodexBrowser(load)).rejects.toThrow('module is unavailable')
  await expect(prepareCodexBrowser(load)).rejects.toThrow('cooling down')
  expect(getter).not.toHaveBeenCalled()
  expect(load).toHaveBeenCalledOnce()
})
