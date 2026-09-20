import {findWriterBlock,isWriterFormulaSpan,type WriterDocument} from './writerIR';
import {rangeTextWithin,selectionActionAnchor} from './artifactRewriteSelection';

export function selectedIRParagraphs(root: HTMLElement, document: WriterDocument) {
 const selection=globalThis.getSelection();if(!selection?.rangeCount || selection.isCollapsed)return null;
 const range=selection.getRangeAt(0);
 if(!root.contains(range.startContainer)||!root.contains(range.endContainer))return null;
 // Titles have no node ID; empty structures still occupy part of the range.
 for(const heading of root.querySelectorAll('h1,h2,h3,h4,h5,h6')) {if(!heading.closest('[data-node-id]')&&rangeTextWithin(range,heading))return null;}
 const nodes:Array<{node_id:string;selected_text:string}>=[];
 for(const element of root.querySelectorAll<HTMLElement>('[data-node-id]')) {
  const block=findWriterBlock(document.blocks,element.dataset.nodeId!);if(!block)continue;
  const content=element.querySelector<HTMLElement>(':scope > [data-writer-block-content], :scope > .writer-ir__paragraph, :scope > .writer-ir__heading, :scope > .writer-ir__fallback') ?? element;
  if(range.intersectsNode(content) && (!['paragraph','heading','list_item'].includes(block.type)||block.editable===false||block.spans?.some(isWriterFormulaSpan)))return null;
  const selected=rangeTextWithin(range,content);if(!selected)continue;
  if(!(block.content ?? '').includes(selected.selectedText))return null;
  nodes.push({node_id:block.node_id,selected_text:selected.selectedText});
 }
 if(nodes.length<2)return null;
 const anchor=selectionActionAnchor(range);if(!anchor)return null;
 return {nodeId:nodes[0].node_id,selectedText:nodes.map(n=>n.selected_text).join('\n\n'),nodeSelections:nodes,anchor};
}
