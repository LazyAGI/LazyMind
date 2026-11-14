import {act,render,waitFor} from '@testing-library/react';
import {MemoryRouter,Route,Routes} from 'react-router-dom';
import {afterEach,expect,it,vi} from 'vitest';
import type {ReactNode} from 'react';
import {axiosInstance} from '@/components/request';
import type {PaperGuideEvidence} from '@/api/generated/core-client';
import type {Dataset} from '@/api/generated/knowledge-client';
import {useDatasetPermissionStore} from '@/modules/knowledge/store/dataset_permission';
import Detail from './index';
const state=vi.hoisted(()=>({viewer:{} as Record<string,any>,guide:{} as Record<string,any>,outline:{} as Record<string,any>}));
vi.mock('react-pdf',()=>({pdfjs:{GlobalWorkerOptions:{}},Document:()=>null,Page:()=>null}));
vi.mock('@/modules/knowledge/components/FileViewer',async()=>({default:(await import('react')).forwardRef((props:Record<string,any>,_ref)=>{state.viewer=props;return <div/>;})}));
vi.mock('@/modules/knowledge/components/PaperReadingWorkspace',()=>({default:(props:Record<string,any>)=>{state.guide=props;return props.source as ReactNode;}}));
vi.mock('./components/PaperOutlineRail',()=>({PaperOutlineRail:(props:Record<string,any>)=>{state.outline=props;return null;}}));
vi.mock('@/modules/knowledge/components/PdfTemporaryChat',async()=>({default:(await import('react')).forwardRef(()=>null)}));
const adapter=axiosInstance.defaults.adapter;
afterEach(()=>{axiosInstance.defaults.adapter=adapter;useDatasetPermissionStore.getState().clearDataset();});
it('uses the guide locator even when the canonical parent still begins on page one',async()=>{
 useDatasetPermissionStore.getState().setCurrentDataset({dataset_id:'library',scene:'academic_papers'} as Dataset);
 axiosInstance.defaults.adapter=config=>config.url?.endsWith('/documents/paper')?Promise.resolve({config,status:200,statusText:'OK',headers:{},data:{display_name:'paper.pdf'}}):new Promise(()=>{});
 render(<MemoryRouter initialEntries={['/doc/library/paper']}><Routes><Route path="/doc/:knowledgeBaseId/:knowledgeId" element={<Detail/>}/></Routes></MemoryRouter>);
 await waitFor(()=>expect(state.viewer.fileName).toBe('paper.pdf'));
 act(()=>state.viewer.onPdfIRDocumentChange({version:1,source_sha256:'hash',parser:{name:'mineru',model:'vlm'},coordinate_space:'normalized_top_left',pages:[{index:0},{index:1}],blocks:[{id:'parent',type:'text',text:'First and second page text',page:0,bbox:[.1,.7,.9,.9]}]}));
 const evidence:PaperGuideEvidence={id:'ev',source_block_id:'parent',source_type:'text',text:'Second page text',locator:{kind:'page',page:1}};
 act(()=>state.guide.onNavigate(evidence));
 await waitFor(()=>expect(state.viewer.pdfNavigateToPosition).toMatchObject({page:2,progress:0,sourceLocator:true}));
 expect(state.viewer.pdfNavigateTo).toBeUndefined();expect(state.viewer.pdfNavigateToPosition.bbox).toBeUndefined();
 act(()=>state.outline.onNavigate('parent'));
 await waitFor(()=>expect(state.viewer.pdfNavigateTo).toMatchObject({id:'parent'}));
 act(()=>state.guide.onNavigate({...evidence,locator:{kind:'text_span',page:1,bbox:[.2,.3,.8,.5]}}));
 await waitFor(()=>expect(state.viewer.pdfNavigateToPosition).toMatchObject({page:2,bbox:[.2,.3,.8,.5]}));
 expect(state.viewer.pdfNavigateTo).toBeUndefined();
 // Returning to the same canonical parent with a page-only guide locator must
 // clear the earlier precise box, including repeated clicks on that page.
 for (let click = 0; click < 2; click++) {
  act(()=>state.guide.onNavigate({...evidence,locator:{kind:'page',page:0}}));
  await waitFor(()=>expect(state.viewer.pdfNavigateToPosition).toMatchObject({page:1,progress:0,sourceLocator:true}));
  expect(state.viewer.pdfNavigateToPosition.bbox).toBeUndefined();
  expect(state.viewer.pdfNavigateTo).toBeUndefined();
 }
});
