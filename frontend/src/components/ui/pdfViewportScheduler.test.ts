import {afterEach, expect, it, vi} from 'vitest';
import {createPdfViewportScheduler} from './pdfViewportScheduler';
afterEach(()=>vi.unstubAllGlobals());
function clock() {
  let id=0;
  const frames=new Map<number,FrameRequestCallback>();
  vi.stubGlobal('requestAnimationFrame',(fn:FrameRequestCallback)=>{frames.set(++id,fn);return id;});
  vi.stubGlobal('cancelAnimationFrame',(key:number)=>frames.delete(key));
  return {frames,advance:(time:number)=>{const pending=[...frames.values()];frames.clear();pending.forEach(fn=>fn(time));}};
}
it('updates throughout continuous resizing, coalescing events instead of waiting for release',()=>{
  const time=clock();const update=vi.fn();const scheduler=createPdfViewportScheduler(update);
  for(let t=0;t<=96;t+=8){scheduler.schedule();scheduler.schedule();expect(time.frames.size).toBe(1);time.advance(t);}
  expect(update).toHaveBeenCalledTimes(4);
  expect(time.frames.size).toBe(0);
});
it('reads the final size even when no further pointer events arrive',()=>{
  const time=clock();let width=400;const sizes:number[]=[];
  const scheduler=createPdfViewportScheduler(()=>sizes.push(width));
  scheduler.schedule();time.advance(0);
  width=430;scheduler.schedule();time.advance(16);
  width=475;scheduler.schedule();time.advance(32);
  expect(sizes).toEqual([400,475]);
});
it('cancels pending geometry work on unmount',()=>{
  const time=clock();const update=vi.fn();const scheduler=createPdfViewportScheduler(update);
  scheduler.schedule();scheduler.cancel();time.advance(32);
  expect(update).not.toHaveBeenCalled();expect(time.frames.size).toBe(0);
});
