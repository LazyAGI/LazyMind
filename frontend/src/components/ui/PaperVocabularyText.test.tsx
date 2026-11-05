import {fireEvent,render,screen} from '@testing-library/react';
import {afterEach,expect,it,vi} from 'vitest';
import {PdfPaperOverview} from './PdfPaperOverview';
const document={version:1 as const,source_sha256:'source',coordinate_space:'normalized_top_left' as const,parser:{name:'mineru',model:'v'},pages:[{index:0}],blocks:[{id:'abstract',type:'abstract',page:0,text:'Abstract: A resilient approach improves accuracy.'}]};
function selectWord(element:HTMLElement,word:string){
 const walker=window.document.createTreeWalker(element,NodeFilter.SHOW_TEXT);
 let node:Node|null;
 while((node=walker.nextNode()))if(node.textContent?.includes(word))break;
 const range=window.document.createRange();
 const start=node!.textContent!.indexOf(word);
 range.setStart(node!,start);range.setEnd(node!,start+word.length);
 window.getSelection()!.removeAllRanges();window.getSelection()!.addRange(range);
 fireEvent.mouseUp(element);
}
afterEach(()=>window.getSelection()?.removeAllRanges());
it('collects a selected word from the overview abstract with its original context and page',()=>{
 const add=vi.fn();
 render(<PdfPaperOverview document={document} onOpenSource={vi.fn()} onAddVocabularySelection={add}/>);
 selectWord(screen.getByText('A resilient approach improves accuracy.'),'resilient');
 fireEvent.click(screen.getByRole('button',{name:/加入生词|Add vocabulary/}));
 expect(add).toHaveBeenCalledWith({text:'resilient',context:'A resilient approach improves accuracy.',page:1});
});
it('does not offer vocabulary when the feature is disabled',()=>{
 render(<PdfPaperOverview document={document} onOpenSource={vi.fn()}/>);
 selectWord(screen.getByText('A resilient approach improves accuracy.'),'resilient');
 expect(screen.queryByRole('button',{name:/加入生词|Add vocabulary/})).toBeNull();
});
it('does not invent page-one evidence for an abstract found only on a later page',()=>{
 render(<PdfPaperOverview document={{...document,pages:[{index:0},{index:2}],blocks:[{...document.blocks[0],page:2}]}} onOpenSource={vi.fn()} onAddVocabularySelection={vi.fn()}/>);
 expect(screen.queryByText('A resilient approach improves accuracy.')).toBeNull();
 expect(screen.queryByRole('button',{name:/加入生词|Add vocabulary/})).toBeNull();
});
