import {fireEvent,render,screen} from '@testing-library/react';
import {expect,it,vi} from 'vitest';
import type {PdfIRDocument} from '@/modules/knowledge/api/pdfIR';
import {PdfVisualStudy} from './PdfVisualStudy';
import {PdfStudyDialog} from './PdfStudyDialog';

vi.mock('@/modules/knowledge/api/paperReader',async original=>({...await original<typeof import('@/modules/knowledge/api/paperReader')>(),usePaperReaderResource:()=>({status:'ready',data:{source_sha256:'hash',summary:'The input is transformed in two steps.',observations:[{text:'The second step checks the result.',evidence_ids:[],inferred:true}],evidence:[],warnings:[]},load:vi.fn(),cancel:vi.fn()})}));

it('shows the source figure crop beside its generated explanation in the original-page popup',()=>{
 const block={id:'image-1',type:'image',page:0,bbox:[.1,.2,.6,.7] as [number,number,number,number],image:{captions:['Figure 1. Architecture'],footnotes:[],number:'1',related_blocks:[]}};
 const document:PdfIRDocument={version:1,source_sha256:'hash',projection_version:2,parser:{name:'mineru',model:'vlm'},coordinate_space:'normalized_top_left',pages:[{index:0}],blocks:[block]};
 const navigate=vi.fn();
 render(<PdfStudyDialog onClose={vi.fn()}><PdfVisualStudy target={{dataset:'d',document:'doc'}} document={document} block={block} sourcePreview="data:image/png;base64,original" onNavigate={navigate} onInspectTable={vi.fn()} onClose={vi.fn()} onReturn={vi.fn()} canReturn={false}/></PdfStudyDialog>);
 expect(screen.getByRole('img',{name:'图片 1'})).toHaveAttribute('src','data:image/png;base64,original');
 expect(screen.getByText('The input is transformed in two steps.')).toBeInTheDocument();
 fireEvent.click(screen.getByRole('button',{name:/Original region|定位原图区域/}));
 expect(navigate).toHaveBeenCalledWith('image-1');
});
