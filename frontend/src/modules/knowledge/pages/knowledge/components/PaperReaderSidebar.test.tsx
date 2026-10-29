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
  const navigate=vi.fn();render(<PaperReaderSidebar document={doc} activeTab="outline" onTabChange={vi.fn()} onNavigate={navigate} onCollapse={vi.fn()} references={<div>Mounted reference importer</div>}/>);
  fireEvent.click(screen.getByRole('button',{name:/3.2. The Model/}));expect(navigate).toHaveBeenCalledWith('section');
 });
 it('keeps references mounted with no count and lets users select vocabulary',()=>{
  const tab=vi.fn();render(<PaperReaderSidebar activeTab="outline" onTabChange={tab} onNavigate={vi.fn()} onCollapse={vi.fn()} references={<div data-testid="importer"/>} vocabulary={<div>Word collection</div>}/>);
  expect(screen.getByTestId('importer')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('tab',{name:'References'}));expect(tab).toHaveBeenCalledWith('references');
  fireEvent.click(screen.getByRole('tab',{name:'Vocabulary'}));expect(tab).toHaveBeenCalledWith('vocabulary');
 });
 it('shows a parsing hint without inventing an outline when source IR is unavailable',()=>{
  render(<PaperReaderSidebar activeTab="outline" onTabChange={vi.fn()} onNavigate={vi.fn()} onCollapse={vi.fn()} references={<div/>}/>);
  expect(screen.getByText('Parse the original PDF to see its outline.')).toBeInTheDocument();
 });
});


it('does not offer PDF parsing for non-PDF documents and keeps their chunks reachable',()=>{
 render(<PaperReaderSidebar isPdf={false} activeTab="outline" onTabChange={vi.fn()} onNavigate={vi.fn()} onCollapse={vi.fn()} references={<div/>} segments={<div>Existing document chunks</div>}/>);
 expect(screen.queryByText('Parse the original PDF to see its outline.')).not.toBeInTheDocument();
 expect(screen.getByText('An outline is not available for this document.')).toBeInTheDocument();
 fireEvent.click(screen.getByText('Document chunks'));
 expect(screen.getByText('Existing document chunks')).toBeVisible();
});


it('keeps vocabulary visible for PDFs when the feature is unavailable',()=>{
 render(<PaperReaderSidebar isPdf activeTab="vocabulary" onTabChange={vi.fn()} onNavigate={vi.fn()} onCollapse={vi.fn()} references={<div/>}/>);
 expect(screen.getByRole('tab',{name:'Vocabulary'})).toBeInTheDocument();
 expect(screen.getByText('Vocabulary is not enabled in this workspace. Contact your administrator to enable it.')).toBeVisible();
});
