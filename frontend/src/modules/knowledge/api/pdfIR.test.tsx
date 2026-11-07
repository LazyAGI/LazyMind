import {act, renderHook, waitFor} from '@testing-library/react';
import {afterEach,describe,it,expect,vi} from 'vitest';
import {usePdfIR, usablePdfIR} from './pdfIR';
const mocks=vi.hoisted(()=>({get:vi.fn(),post:vi.fn()}));
vi.mock('@/components/request',()=>({axiosInstance:mocks,BASE_URL:''}));
afterEach(()=>vi.useRealTimers());
describe('standalone PDF IR',()=>{
 it('rejects wrong source and unknown versions',()=>{
 const document={version:1,source_sha256:'abc',coordinate_space:'normalized_top_left',pages:[],blocks:[]};
 expect(usablePdfIR({status:'ready',source_sha256:'abc',document} as never,'abc')).toEqual(document);
 expect(usablePdfIR({status:'ready',document} as never,'other')).toBeUndefined();
 expect(usablePdfIR({status:'ready',document:{...document,version:2}} as never,'abc')).toBeUndefined();
 });
 it('aborts old requests and never applies late selection responses',async()=>{
 let resolveOld!:(value:unknown)=>void;
 mocks.get.mockImplementationOnce(()=>new Promise(resolve=>{resolveOld=resolve})).mockResolvedValue({data:{data:{status:'not_requested'}}});
 const view=renderHook(({document})=>usePdfIR({dataset:'d',document}),{initialProps:{document:'old'}});
 view.rerender({document:'new'});
 await waitFor(()=>expect(view.result.current.state.status).toBe('not_requested'));
 expect(mocks.get.mock.calls[0][1].signal.aborted).toBe(true);
 await act(async()=>resolveOld({data:{data:{status:'ready'}}}));
 expect(view.result.current.state.status).toBe('not_requested');
 view.unmount();
 });
 it('retains unavailable start diagnostics without replacing them with GET state',async()=>{
 mocks.get.mockReset().mockResolvedValue({data:{data:{status:'not_requested'}}});
 mocks.post.mockResolvedValue({data:{data:{status:'unavailable',error_message:'Configure MinerU'}}});
 const view=renderHook(()=>usePdfIR({dataset:'d',document:'missing'}));
 await waitFor(()=>expect(mocks.get).toHaveBeenCalledTimes(1));
 await act(async()=>view.result.current.start());
 expect(view.result.current.state.status).toBe('unavailable');
 expect(mocks.get).toHaveBeenCalledTimes(1);
 view.unmount();
 });

 it('ignores an initial GET that finishes after a start response',async()=>{
 let resolveOld!:(value:unknown)=>void;
 mocks.get.mockReset().mockImplementation(()=>new Promise(resolve=>{resolveOld=resolve}));
 mocks.post.mockResolvedValue({data:{data:{status:'unavailable',error_message:'Configure MinerU'}}});
 const view=renderHook(()=>usePdfIR({dataset:'d',document:'race'}));
 await act(async()=>view.result.current.start());
 await act(async()=>resolveOld({data:{data:{status:'not_requested'}}}));
 expect(view.result.current.state.status).toBe('unavailable');
 view.unmount();
 });

 it('polls only active jobs and cancels timers on unmount without starting parsing',async()=>{
 vi.useFakeTimers();
 mocks.get.mockReset().mockResolvedValue({data:{data:{status:'queued'}}});
 mocks.post.mockReset();
 const view=renderHook(()=>usePdfIR({dataset:'d',document:'poll'}));
 await act(async()=>{});
 expect(view.result.current.state.status).toBe('queued');
 await act(async()=>vi.advanceTimersByTime(2000));
 expect(mocks.get).toHaveBeenCalledTimes(2);
 view.unmount();
 await act(async()=>vi.advanceTimersByTime(10000));
 expect(mocks.get).toHaveBeenCalledTimes(2);
 expect(mocks.post).not.toHaveBeenCalled();
 });

});

it('ignores an external refresh callback captured for a previous document', async () => {
 mocks.get.mockReset().mockResolvedValue({data:{data:{status:'not_requested'}}});
 mocks.post.mockReset();
 const view=renderHook(({document})=>usePdfIR({dataset:'d',document}),{initialProps:{document:'old'}});
 await act(async()=>{});
 const refreshOld=view.result.current.refresh;
 view.rerender({document:'new'});
 await act(async()=>{});
 const count=mocks.get.mock.calls.length;
 await act(async()=>refreshOld());
 expect(mocks.get).toHaveBeenCalledTimes(count);
 await act(async()=>view.result.current.refresh());
 expect(mocks.get).toHaveBeenCalledTimes(count+1);
 expect(mocks.post).not.toHaveBeenCalled();
 view.unmount();
});

 it('distinguishes pending observation from an unparsed document', async () => {
 let resolve!:(value:unknown)=>void;
 mocks.get.mockReset().mockImplementation(()=>new Promise(done=>{resolve=done}));
 const view=renderHook(()=>usePdfIR({dataset:'d',document:'pending'}));
 expect(view.result.current.loading).toBe(true);
 await act(async()=>resolve({data:{data:{status:'not_requested'}}}));
 expect(view.result.current.loading).toBe(false);
 expect(view.result.current.state.status).toBe('not_requested');
 view.unmount();
 });
