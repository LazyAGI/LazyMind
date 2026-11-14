import {act,renderHook,waitFor} from '@testing-library/react';
import {beforeEach,expect,it,vi} from 'vitest';
import {AUTH_LOGOUT_EVENT} from '@/components/auth';
import {usePaperReaderResource} from './paperReader';
const mocks=vi.hoisted(()=>({post:vi.fn()}));
vi.mock('@/components/request',()=>({axiosInstance:mocks,BASE_URL:''}));
const old={source_sha256:'source',summary:'Existing explanation'};
const useResource=(resource='blocks/figure/explanation',source='source')=>usePaperReaderResource<typeof old>({dataset:'d',document:'doc'},source,source,resource,'en',true);
beforeEach(()=>{mocks.post.mockReset();window.dispatchEvent(new Event(AUTH_LOGOUT_EVENT));});
it('keeps the same-source explanation during updates, failure and cancellation and replaces it on success',async()=>{
 mocks.post.mockResolvedValueOnce({data:{data:old}});
 const {result}=renderHook(()=>useResource());
 await waitFor(()=>expect(result.current.data).toEqual(old));
 let reject!:(reason:unknown)=>void;
 mocks.post.mockImplementationOnce(()=>new Promise((_,fail)=>{reject=fail;}));
 let loading!:Promise<void>;
 act(()=>{loading=result.current.load(true);});
 expect(result.current.status).toBe('loading');expect(result.current.data).toEqual(old);
 await act(async()=>{reject(new Error('Unavailable'));await loading;});
 expect(result.current.status).toBe('error');expect(result.current.data).toEqual(old);
 let finish!:(value:unknown)=>void;
 mocks.post.mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve;}));
 act(()=>{loading=result.current.load(true);});
 act(()=>result.current.cancel());
 expect(result.current.status).toBe('cancelled');expect(result.current.data).toEqual(old);
 await act(async()=>{finish({data:{data:{...old,summary:'Cancelled response'}}});await loading;});
 expect(result.current.data).toEqual(old);
 mocks.post.mockResolvedValueOnce({data:{data:{...old,summary:'Updated explanation'}}});
 await act(()=>result.current.load(true));
 expect(result.current.data?.summary).toBe('Updated explanation');
});
it('does not retain old content across resource/source changes or explicit source mismatch',async()=>{
 mocks.post.mockResolvedValueOnce({data:{data:old}});
 const {result,rerender}=renderHook(({resource,source})=>useResource(resource,source),{initialProps:{resource:'blocks/figure/explanation',source:'source'}});
 await waitFor(()=>expect(result.current.data).toEqual(old));
 mocks.post.mockImplementationOnce(()=>new Promise(()=>{}));
 rerender({resource:'blocks/other/explanation',source:'source'});
 expect(result.current.data).toBeUndefined();
 mocks.post.mockResolvedValueOnce({data:{data:{source_sha256:'new-source',summary:'New'}}});
 rerender({resource:'blocks/other/explanation',source:'new-source'});
 await waitFor(()=>expect(result.current.data?.summary).toBe('New'));
 mocks.post.mockResolvedValueOnce({data:{data:old}});
 await act(()=>result.current.load(true));
 expect(result.current.sourceMismatch).toBe(true);expect(result.current.data).toBeUndefined();
});

it('reuses a same-source explanation on reopening without another request, but explicit regeneration bypasses it',async()=>{
 mocks.post.mockResolvedValue({data:{data:old}});
 const first=renderHook(()=>useResource());
 await waitFor(()=>expect(first.result.current.data).toEqual(old));
 first.unmount();
 const second=renderHook(()=>useResource());
 await waitFor(()=>expect(second.result.current.data).toEqual(old));
 expect(mocks.post).toHaveBeenCalledTimes(1);
 await act(()=>second.result.current.load(true));
 expect(mocks.post).toHaveBeenCalledTimes(2);
});
it('clears saved browser explanations on logout',async()=>{
 mocks.post.mockResolvedValue({data:{data:old}});
 const first=renderHook(()=>useResource());
 await waitFor(()=>expect(first.result.current.data).toEqual(old));
 first.unmount();
 act(()=>window.dispatchEvent(new Event(AUTH_LOGOUT_EVENT)));
 const second=renderHook(()=>useResource());
 await waitFor(()=>expect(second.result.current.data).toEqual(old));
 expect(mocks.post).toHaveBeenCalledTimes(2);
});

it.each([{code:'ECONNABORTED'},{code:'ETIMEDOUT'},{response:{status:504}},{response:{status:502,data:{data:{error_code:'request_timeout'}}}}])('reports a reading timeout and retains the previous result: %j',async failure=>{
 mocks.post.mockResolvedValueOnce({data:{data:old}});
 const {result}=renderHook(()=>useResource());
 await waitFor(()=>expect(result.current.status).toBe('ready'));
 mocks.post.mockRejectedValueOnce(failure);
 await act(async()=>{await result.current.load(true);});
 expect(result.current.status).toBe('error');
 expect(result.current.error).toMatch(/2 minutes/);
 expect(result.current.error).not.toMatch(/configuration/);
 expect(result.current.data).toEqual(old);
});
