import {expect,it,vi} from 'vitest';
import {readNativePdfOutline} from './pdfOutline';

it('reads embedded bookmarks without PDF IR and resolves destinations to pages',async()=>{
 const getPageIndex=vi.fn(async(ref:unknown)=>{if(ref==='missing')throw new Error('Invalid bookmark');return ref==='page-five'?4:0;});
 const result=await readNativePdfOutline({numPages:8,getPageIndex,getDestination:async()=>['page-five'],getOutline:async()=>[
  {title:'Introduction',dest:[0],items:[{title:'Architecture',dest:'named-destination'}]},
  {title:'Broken destination',dest:['missing']},
 ]});
 expect(result.map(item=>[item.title,item.page,item.depth])).toEqual([['Introduction',1,0],['Architecture',5,1]]);
 expect(getPageIndex).toHaveBeenCalledWith('page-five');
});
