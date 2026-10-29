import {describe,it,expect} from 'vitest';
import {formulaNumberMap, referenceAtPoint, formulaReferenceForClick} from './pdfFormulaReferences';
const formula={id:'a',type:'formula',page:0,latex:'x=1 \\tag{3}'};
describe('equation references',()=>{
 it('maps explicit tags but does not guess ambiguous or absent numbers',()=>{
 expect(formulaNumberMap([formula]).get('3')?.id).toBe('a');
 expect(formulaNumberMap([formula,{...formula,id:'b'}]).has('3')).toBe(false);
 expect(formulaNumberMap([{...formula,latex:'x=1'}]).size).toBe(0);
 });
 it('matches references split across PDF text spans without modifying selection DOM',()=>{
 const page=document.createElement('div'); page.innerHTML='<div class="react-pdf__Page__textContent"><span>See Eq.</span><span> (3) for details</span></div>';
 const original=page.innerHTML;
 const getRects=Range.prototype.getClientRects;
 Range.prototype.getClientRects=()=>[{left:10,right:50,top:10,bottom:30}] as unknown as DOMRectList;
 expect(referenceAtPoint(page,formulaNumberMap([formula]),20,20)?.id).toBe('a');
 expect(referenceAtPoint(page,formulaNumberMap([formula]),80,20)).toBeUndefined();
 expect(page.innerHTML).toBe(original);
 Range.prototype.getClientRects=getRects;
 });
});


describe('native PDF link compatibility',()=>{
 it('recognizes internal annotation overlays but preserves external links, dragging and selection',()=>{
 const page=document.createElement('div');
 page.innerHTML='<div class="react-pdf__Page__textContent"><span>Eq. (3)</span></div><section data-internal-link><a href="#eq3">link</a></section><a href="https://example.com">external</a>';
 const original=Range.prototype.getClientRects;
 Range.prototype.getClientRects=()=>[{left:10,right:50,top:10,bottom:30}] as unknown as DOMRectList;
 const internal=page.querySelector('section a')!;
 const external=page.querySelector('a[href^="https"]')!;
 const formulas=formulaNumberMap([formula]);
 expect(formulaReferenceForClick(page,formulas,internal,{x:20,y:20},20,20,'')?.id).toBe('a');
 expect(formulaReferenceForClick(page,formulas,external,{x:20,y:20},20,20,'')).toBeUndefined();
 expect(formulaReferenceForClick(page,formulas,internal,{x:0,y:20},20,20,'')).toBeUndefined();
 expect(formulaReferenceForClick(page,formulas,internal,{x:20,y:20},20,20,'selected')).toBeUndefined();
 expect(formulaReferenceForClick(page,formulas,internal,{x:80,y:20},80,20,'')).toBeUndefined();
 Range.prototype.getClientRects=original;
 });
});
