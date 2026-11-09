import {fireEvent,render,screen} from '@testing-library/react';
import {beforeEach,expect,it,vi} from 'vitest';
import type {PdfIRDocument} from '@/modules/knowledge/api/pdfIR';
import {PdfVisualStudy} from './PdfVisualStudy';
import {PdfStudyDialog} from './PdfStudyDialog';

const state=vi.hoisted(()=>({status:'ready',cancel:vi.fn()}));
beforeEach(()=>{state.status='ready';state.cancel.mockReset();});
vi.mock('@/modules/knowledge/api/paperReader',async original=>({...await original<typeof import('@/modules/knowledge/api/paperReader')>(),usePaperReaderResource:()=>({status:state.status,data:{source_sha256:'hash',summary:'The input is transformed in two steps.',observations:[{text:'The second step checks the result.',evidence_ids:[],inferred:true}],evidence:[],warnings:[]},load:vi.fn(),cancel:state.cancel})}));

it('shows the source figure crop beside its generated explanation in the original-page popup',()=>{
 const block={id:'image-1',type:'image',page:0,bbox:[.1,.2,.6,.7] as [number,number,number,number],image:{captions:['Figure 1. Architecture'],footnotes:[],number:'1',related_blocks:[]}};
 const document:PdfIRDocument={version:1,source_sha256:'hash',projection_version:2,parser:{name:'mineru',model:'vlm'},coordinate_space:'normalized_top_left',pages:[{index:0}],blocks:[block]};
 const navigate=vi.fn();
 render(<PdfStudyDialog onClose={vi.fn()}><PdfVisualStudy target={{dataset:'d',document:'doc'}} document={document} block={block} sourcePreview="data:image/png;base64,original" onNavigate={navigate} onInspectTable={vi.fn()} onClose={vi.fn()} onReturn={vi.fn()} canReturn={false}/></PdfStudyDialog>);
 expect(screen.getByRole('img',{name:'图片 1'})).toHaveAttribute('src','data:image/png;base64,original');
 expect(screen.getByText('The input is transformed in two steps.')).toBeInTheDocument();
 const zoom=screen.getByRole('button',{name:'放大图表'});fireEvent.click(zoom);expect(zoom).toHaveAttribute('aria-pressed','true');
 fireEvent.click(screen.getByRole('button',{name:/Original region|定位原图区域/}));
 expect(navigate).toHaveBeenCalledWith('image-1');
});

it('offers cancellable progress while keeping previous explanations readable',()=>{
 state.status='loading';
 const block={id:'v',type:'image',page:0};
 const document:PdfIRDocument={version:1,source_sha256:'hash',parser:{name:'mineru',model:'vlm'},coordinate_space:'normalized_top_left',pages:[],blocks:[block]};
 render(<PdfVisualStudy target={{dataset:'d',document:'doc'}} document={document} block={block} onNavigate={vi.fn()} onInspectTable={vi.fn()} onClose={vi.fn()} onReturn={vi.fn()} canReturn={false}/>);
 expect(screen.getByRole('progressbar')).not.toHaveAttribute('value');
 expect(screen.getByText('The input is transformed in two steps.')).toBeInTheDocument();
 fireEvent.click(screen.getByRole('button',{name:/取消|Cancel/}));
 expect(state.cancel).toHaveBeenCalledOnce();
});

it('keeps interactive table cells and their header context beside the explanation',()=>{
 const block={id:'table',type:'table',page:0,table:{number:'1',captions:['Results'],footnotes:['Accuracy in percent'],related_blocks:[],rows:[{cells:[{text:'Method',header:true,row_span:1,col_span:1},{text:'Accuracy',header:true,row_span:1,col_span:1}]},{cells:[{text:'Baseline',row_span:1,col_span:1},{text:'82.4',row_span:1,col_span:1}]}]}};
 const document:PdfIRDocument={version:1,source_sha256:'hash',parser:{name:'mineru',model:'vlm'},coordinate_space:'normalized_top_left',pages:[],blocks:[block]};
 render(<PdfStudyDialog onClose={vi.fn()}><PdfVisualStudy target={{dataset:'d',document:'doc'}} document={document} block={block} sourcePreview="data:image/png;base64,original" onNavigate={vi.fn()} onInspectTable={vi.fn()} onClose={vi.fn()} onReturn={vi.fn()} canReturn={false}/></PdfStudyDialog>);
 fireEvent.click(screen.getByRole('button',{name:'82.4'}));
 expect(screen.getByText('The input is transformed in two steps.')).toBeInTheDocument();
 expect(screen.getByRole('table')).toBeInTheDocument();
 expect(screen.getAllByText('Accuracy').length).toBeGreaterThan(1);
 fireEvent.click(screen.getByRole('button',{name:'原始表格'}));
 expect(screen.queryByRole('table')).not.toBeInTheDocument();
 expect(screen.getByRole('img')).toHaveAttribute('src','data:image/png;base64,original');
});

it('shows observed row and column context when extraction did not identify headers',()=>{
 const block={id:'table',type:'table',page:0,table:{number:'2',captions:[],footnotes:[],related_blocks:[],rows:[{cells:[{text:'Model',row_span:1,col_span:1},{text:'BLEU',row_span:1,col_span:1}]},{cells:[{text:'Transformer (big)',row_span:1,col_span:1},{text:'28.4',row_span:1,col_span:1}]}]}};
 const document:PdfIRDocument={version:1,source_sha256:'hash',parser:{name:'mineru',model:'vlm'},coordinate_space:'normalized_top_left',pages:[],blocks:[block]};
 render(<PdfVisualStudy target={{dataset:'d',document:'doc'}} document={document} block={block} onNavigate={vi.fn()} onInspectTable={vi.fn()} onClose={vi.fn()} onReturn={vi.fn()} canReturn={false}/>);
 fireEvent.click(screen.getByRole('button',{name:'28.4'}));
 expect(screen.getByText('同行标签')).toBeInTheDocument();
 expect(screen.getByText('列上方文字')).toBeInTheDocument();
 expect(screen.getAllByText('Transformer (big)')).toHaveLength(2);
 expect(screen.getAllByText('BLEU')).toHaveLength(2);
});
