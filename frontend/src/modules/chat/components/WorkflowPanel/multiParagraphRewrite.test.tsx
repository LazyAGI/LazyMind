import {act,fireEvent,render,screen,waitFor,within} from '@testing-library/react';
import {beforeEach,expect,it,vi} from 'vitest';
import i18n from '@/i18n';
import {ConfigProvider} from 'antd';
import {WriterIRDocumentEditor} from './WriterIRDocumentEditor';
import {ArtifactRewriteBatchPreview} from './ArtifactRewriteBatchPreview';
import {documentRewritePreview} from './documentRewritePreview';
import {selectedIRParagraphs} from './writerIRRewriteSelection';
import {markdownSelectionRange} from './writerMarkdownSource';
import {selectedMarkdownParagraph} from './artifactRewriteSelection';
const payload={representation:'markdown',results:[
 {target:{type:'block',block_type:'paragraph',target_start:0,target_end:5},preview:{old_text:'First',new_text:'Clear'},patch:{type:'string_replace_set',payload:{}}},
 {target:{type:'block',block_type:'paragraph',target_start:13,target_end:17},preview:{old_text:'Last',new_text:'Better'},patch:{type:'string_replace_set',payload:{}}},
],artifact:{content_type:'text',value:'Clear\n\nKeep\n\nBetter'},commit:{token:'00000000000000000000000000000001'}};
beforeEach(async()=>{await i18n.changeLanguage('zh-CN');Range.prototype.getBoundingClientRect=()=>({x:0,y:100,top:100,left:0,right:100,bottom:140,width:100,height:40,toJSON:()=>({})});});
it('keeps every paragraph diff and only commits on explicit confirmation',async()=>{
 const preview=documentRewritePreview(payload,3,7),apply=vi.fn(),cancel=vi.fn();
 render(<ConfigProvider theme={{token:{motion:false}}}><ArtifactRewriteBatchPreview preview={preview} onApply={apply} onCancel={cancel}/></ConfigProvider>);
 expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
 await waitFor(()=>expect(screen.getByText('Clear')).toBeVisible());expect(screen.getByText('Better')).toBeVisible();expect(apply).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole('button',{name:'全部拒绝'}));expect(cancel).toHaveBeenCalledTimes(1);expect(apply).not.toHaveBeenCalled();
 let finish!:()=>void;apply.mockImplementation(()=>new Promise<void>(resolve=>{finish=resolve;}));
 const button=screen.getByRole('button',{name:'全部接受'});fireEvent.click(button);fireEvent.click(button);expect(apply).toHaveBeenCalledTimes(1);
 await act(async()=>finish());
});
it('preserves the preview when applying fails',async()=>{
 const apply=vi.fn().mockRejectedValue(new Error('conflict'));
 render(<ConfigProvider theme={{token:{motion:false}}}><ArtifactRewriteBatchPreview preview={documentRewritePreview(payload,3,7)} onApply={apply} onCancel={vi.fn()}/></ConfigProvider>);
 fireEvent.click(screen.getByRole('button',{name:'全部接受'}));
 expect(await screen.findByRole('alert')).toHaveTextContent('正文可能已变化');await waitFor(()=>expect(screen.getByText('Better')).toBeVisible());
});
it('rejects one paragraph and applies only the remaining proposal', async () => {
 const apply=vi.fn().mockResolvedValue(undefined),complete=vi.fn(),cancel=vi.fn();
 render(<ArtifactRewriteBatchPreview preview={documentRewritePreview(payload,3,7)} onApply={apply} onComplete={complete} onCancel={cancel}/>);
 fireEvent.click(within(screen.getByRole('group',{name:'第 1 段'})).getByRole('button',{name:'拒绝'}));
 expect(screen.queryByText('Clear')).not.toBeInTheDocument();expect(screen.getByText('Better')).toBeVisible();
 expect(cancel).not.toHaveBeenCalled();expect(apply).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole('button',{name:'全部接受'}));
 await waitFor(()=>expect(complete).toHaveBeenCalledTimes(1));expect(apply).toHaveBeenCalledWith([1]);
});
it('accepts one paragraph without resolving the others, then rejects the rest', async () => {
 const apply=vi.fn().mockResolvedValue(undefined),complete=vi.fn(),cancel=vi.fn();
 render(<ArtifactRewriteBatchPreview preview={documentRewritePreview(payload,3,7)} onApply={apply} onComplete={complete} onCancel={cancel}/>);
 fireEvent.click(within(screen.getByRole('group',{name:'第 1 段'})).getByRole('button',{name:'接受'}));
 await waitFor(()=>expect(screen.queryByText('Clear')).not.toBeInTheDocument());
 expect(apply).toHaveBeenCalledWith([0]);expect(complete).not.toHaveBeenCalled();expect(screen.getByText('Better')).toBeVisible();
 fireEvent.click(screen.getByRole('button',{name:'全部拒绝'}));expect(cancel).toHaveBeenCalledTimes(1);expect(apply).toHaveBeenCalledTimes(1);
});
it('maps cross-paragraph DOM selections to distinct Unicode source positions',()=>{
 const root=document.createElement('div');root.className='mdxeditor-root-contenteditable';root.innerHTML='<p>😀 Same.</p><p>Keep.</p><p>😀 Same.</p>';document.body.append(root);
 const paragraphs=root.querySelectorAll('p'),range=document.createRange();range.setStart(paragraphs[0].firstChild!,3);range.setEnd(paragraphs[2].firstChild!,7);
 const selection=globalThis.getSelection()!;selection.removeAllRanges();selection.addRange(range);
 const picked=selectedMarkdownParagraph(root,true)!;expect(picked.supported).toBe(true);
 const source='😀 Same.\n\nKeep.\n\n😀 Same.';
 const ranges=picked.paragraphSelections!.map(item=>markdownSelectionRange(source,item));
 expect(ranges).toHaveLength(3);expect(ranges[0].start).toBe(2);expect(ranges[2].start).toBe(16);
 for(const item of ranges)expect(Array.from(source).slice(item.start,item.end).join('')).toBe(item.selected_text);
 root.remove();
});
it('captures a heading between selected paragraphs',()=>{
 const root=document.createElement('div');root.innerHTML='<p>First.</p><h2>Heading</h2><p>Last.</p>';document.body.append(root);
 const range=document.createRange();range.selectNodeContents(root);globalThis.getSelection()!.removeAllRanges();globalThis.getSelection()!.addRange(range);
 expect(selectedMarkdownParagraph(root,true)).toMatchObject({supported:true,text:'First.\n\nHeading\n\nLast.'});root.remove();
});
it('captures multiple IR paragraphs and rejects a read-only member',()=>{
 const root=document.createElement('div');root.innerHTML='<div data-node-id="one"><p data-writer-block-content>First.</p></div><div data-node-id="two"><p data-writer-block-content>Last.</p></div>';document.body.append(root);
 const range=document.createRange();range.selectNodeContents(root);globalThis.getSelection()!.removeAllRanges();globalThis.getSelection()!.addRange(range);
 const documentValue={document_id:'fixture',title:'Fixture',stage:'draft',blocks:[{node_id:'one',type:'paragraph',content:'First.'},{node_id:'two',type:'paragraph',content:'Last.',editable:true}]};
 expect(selectedIRParagraphs(root,documentValue)?.nodeSelections).toEqual([{node_id:'one',selected_text:'First.'},{node_id:'two',selected_text:'Last.'}]);
 documentValue.blocks[1].editable=false;expect(selectedIRParagraphs(root,documentValue)).toBeNull();root.remove();
});

it('exposes multi-paragraph polish from the real IR editor selection',async()=>{
 const onRewrite=vi.fn();
 const value={document_id:'fixture',title:'Fixture',stage:'draft',blocks:[{node_id:'one',type:'paragraph',content:'First.'},{node_id:'two',type:'paragraph',content:'Last.'}]};
 const {container}=render(<WriterIRDocumentEditor document={value} ariaLabel='IR editor' onChange={vi.fn()} onFocus={vi.fn()} onBlur={vi.fn()} allowMultipleParagraphs onRewriteSelection={onRewrite}/>);
 const paragraphs=container.querySelectorAll('[data-writer-block-content]');
 const range=document.createRange();range.setStart(paragraphs[0].firstChild!,1);range.setEnd(paragraphs[1].firstChild!,4);
 globalThis.getSelection()!.removeAllRanges();globalThis.getSelection()!.addRange(range);
 fireEvent(document,new Event('selectionchange'));
 fireEvent.click(await screen.findByRole('button',{name:String(i18n.t('chat.artifactRewrite.action'))}));
 expect(onRewrite).toHaveBeenCalledWith(expect.objectContaining({nodeSelections:[{node_id:'one',selected_text:'irst.'},{node_id:'two',selected_text:'Last'}]}));
});

it.each(['editing','reading'])('rejects IR selections crossing empty structures or the document title in %s',mode=>{
 for(const extra of ['<div data-node-id="divider"><hr></div>','<h1>Document title</h1>']) {
  const root=document.createElement('div');const attribute=mode==='editing'?'data-writer-block-content':'class="writer-ir__paragraph"';
  root.innerHTML=`<div data-node-id="one"><p ${attribute}>First.</p></div>${extra}<div data-node-id="two"><p ${attribute}>Last.</p></div>`;document.body.append(root);
  const range=document.createRange();range.selectNodeContents(root);globalThis.getSelection()!.removeAllRanges();globalThis.getSelection()!.addRange(range);
  const value={document_id:'fixture',title:'Fixture',stage:'draft',blocks:[{node_id:'one',type:'paragraph',content:'First.'},{node_id:'divider',type:'divider',content:''},{node_id:'two',type:'paragraph',content:'Last.'}]};
  expect(selectedIRParagraphs(root,value)).toBeNull();root.remove();
 }
});
it('includes heading text only when the selection actually intersects it', () => {
 const root=document.createElement('div');root.innerHTML='<h1>Title</h1><p>First</p><p>Last</p>';document.body.append(root);
 const heading=root.querySelector('h1')!.firstChild!,last=root.querySelectorAll('p')[1].firstChild!;
 const range=document.createRange();range.setStart(heading,5);range.setEnd(last,4);const selection=window.getSelection()!;selection.removeAllRanges();selection.addRange(range);
 expect(selectedMarkdownParagraph(root,true)).toMatchObject({supported:true,text:'First\n\nLast'});
 range.setStart(heading,4);expect(selectedMarkdownParagraph(root,true)).toMatchObject({supported:true,text:'e\n\nFirst\n\nLast'});selection.removeAllRanges();root.remove();
});

it('captures IR headings and list items without including unselected descendants', () => {
 const root=document.createElement('div');
 root.innerHTML='<div data-node-id="h"><h2 data-writer-block-content>Heading</h2><div data-node-id="p"><p data-writer-block-content>Body</p></div></div><div data-node-id="li"><p data-writer-block-content>Item</p><div data-node-id="child"><p data-writer-block-content>Child</p></div></div>';
 document.body.append(root);
 const range=document.createRange();range.setStart(root.querySelector('h2')!.firstChild!,0);range.setEnd(root.querySelector('[data-node-id="li"] > p')!.firstChild!,4);
 const selection=window.getSelection()!;selection.removeAllRanges();selection.addRange(range);
 const value={document_id:'fixture',blocks:[{node_id:'h',type:'heading',content:'Heading',children:[{node_id:'p',type:'paragraph',content:'Body'}]},{node_id:'li',type:'list_item',content:'Item',children:[{node_id:'child',type:'list_item',content:'Child'}]}]};
 expect(selectedIRParagraphs(root,value)?.nodeSelections).toEqual([{node_id:'h',selected_text:'Heading'},{node_id:'p',selected_text:'Body'},{node_id:'li',selected_text:'Item'}]);
 selection.removeAllRanges();root.remove();
});
