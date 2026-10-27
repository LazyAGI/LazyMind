import {expect,it,vi} from 'vitest';
import {libraryFixture,paperFixture} from './__tests__/fixtures';
import {loadPaperLibraryForExport} from './exportLibrary';
const api=vi.hoisted(()=>({get:vi.fn()}));
vi.mock('@/components/request',()=>({BASE_URL:'',axiosInstance:api}));
it('exports known, zero and unknown counts without changing bibliography',async()=>{
 const data=libraryFixture({papers:[paperFixture({id:'cited',metadata:{citationCount:42}}),paperFixture({id:'uncited',metadata:{citationCount:0}}),paperFixture({id:'unknown'})]});
 api.get.mockResolvedValue({data:{data,can_write:true}});
 const exported=await loadPaperLibraryForExport('papers');
 expect(exported.papers.map(p=>p.metadata.citationCount)).toEqual([42,0,undefined]);
});
