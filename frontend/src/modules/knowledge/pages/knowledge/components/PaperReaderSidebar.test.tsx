import {fireEvent,render,screen} from '@testing-library/react';
import {beforeAll,describe,expect,it,vi} from 'vitest';
import i18n from '@/i18n';
import type {PdfIRDocument} from '@/modules/knowledge/api/pdfIR';
import {PaperReaderSidebar,paperOutline} from './PaperReaderSidebar';
const doc={version:1,source_sha256:'hash',coordinate_space:'normalized_top_left',parser:{name:'mineru',model:'vlm'},pages:[],blocks:[{id:'abstract',type:'text',text:'Abstract',page:0},{id:'section',type:'text',text:'3.2. The Model',page:3},{id:'body',type:'text',text:'A paragraph describing the model.',page:3}]} as PdfIRDocument;
beforeAll(async()=>{await i18n.changeLanguage('en-US');});
describe('paper navigation sidebar',()=>{
 it('uses original section text and block IDs, never a body paragraph',()=>{
  expect(paperOutline(doc).map(block=>block.id)).toEqual(['abstract','section']);
  const navigate=vi.fn();render(<PaperReaderSidebar paperMode document={doc} activeTab="outline" onTabChange={vi.fn()} onNavigate={navigate} onCollapse={vi.fn()} references={<div>Mounted reference importer</div>}/>);
  fireEvent.click(screen.getByRole('button',{name:/3.2. The Model/}));expect(navigate).toHaveBeenCalledWith('section');
 });
 it('keeps references mounted with no count and lets users select vocabulary',()=>{
  const tab=vi.fn();render(<PaperReaderSidebar paperMode activeTab="outline" onTabChange={tab} onNavigate={vi.fn()} onCollapse={vi.fn()} references={<div data-testid="importer"/>} vocabulary={<div>Word collection</div>}/>);
  expect(screen.getByTestId('importer')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('tab',{name:'References'}));expect(tab).toHaveBeenCalledWith('references');
  fireEvent.click(screen.getByRole('tab',{name:'Vocabulary'}));expect(tab).toHaveBeenCalledWith('vocabulary');
 });
 it('shows a parsing hint without inventing an outline when source IR is unavailable',()=>{
  render(<PaperReaderSidebar paperMode activeTab="outline" onTabChange={vi.fn()} onNavigate={vi.fn()} onCollapse={vi.fn()} references={<div/>}/>);
  expect(screen.getByText('This PDF has no available outline.')).toBeInTheDocument();
 });
});


it('does not offer PDF parsing for non-PDF documents and keeps their chunks reachable',()=>{
 const change=vi.fn();
 render(<PaperReaderSidebar isPdf={false} activeTab="references" onTabChange={change} onNavigate={vi.fn()} onCollapse={vi.fn()} references={<div/>} segments={<div>Existing document chunks</div>}/>);
 expect(screen.queryByText('This PDF has no available outline.')).not.toBeInTheDocument();
 expect(screen.queryByRole('tab',{name:'Outline'})).toBeNull();
 fireEvent.click(screen.getByRole('tab',{name:'Document chunks'}));
 expect(change).toHaveBeenCalledWith('segments');
});


it('keeps vocabulary visible for PDFs when the feature is unavailable',()=>{
 render(<PaperReaderSidebar paperMode isPdf activeTab="vocabulary" onTabChange={vi.fn()} onNavigate={vi.fn()} onCollapse={vi.fn()} references={<div/>}/>);
 expect(screen.getByRole('tab',{name:'Vocabulary'})).toBeInTheDocument();
 expect(screen.getByText('Vocabulary is not enabled in this workspace. Contact your administrator to enable it.')).toBeVisible();
});

it('uses document navigation for ordinary knowledge files and keeps chunks accessible',()=>{
 const change=vi.fn();
 render(<PaperReaderSidebar paperMode={false} isPdf activeTab="references" onTabChange={change} onNavigate={vi.fn()} onCollapse={vi.fn()} references={<div>Reference importer</div>} segments={<div>Existing document chunks</div>}/>);
 expect(screen.getByRole('complementary',{name:'Document navigation'})).toBeInTheDocument();
 expect(screen.queryByRole('tab',{name:'Outline'})).toBeNull();
 expect(screen.getByRole('tab',{name:'References'})).toBeInTheDocument();
 fireEvent.click(screen.getByRole('tab',{name:'Document chunks'}));
 expect(change).toHaveBeenCalledWith('segments');
});

it('offers source-backed figures and tables and highlights the current section',()=>{
 const navigate=vi.fn();
 const source={...doc,blocks:[...doc.blocks,{id:'figure',type:'image',page:4,text:'Architecture',image:{number:'1',captions:['Architecture'],footnotes:[],related_blocks:[]}}]};
 const view=render(<PaperReaderSidebar paperMode document={source} currentPage={4} activeTab="outline" onTabChange={vi.fn()} onNavigate={navigate} onCollapse={vi.fn()} references={<div/>}/>);
 expect(screen.getByRole('button',{name:/3.2. The Model/})).toHaveAttribute('aria-current','location');
 view.rerender(<PaperReaderSidebar paperMode document={source} currentPage={4} activeTab="visuals" onTabChange={vi.fn()} onNavigate={navigate} onCollapse={vi.fn()} references={<div/>}/>);
 fireEvent.click(screen.getByRole('button',{name:/Figure 1/}));
 expect(navigate).toHaveBeenCalledWith('figure');
});
