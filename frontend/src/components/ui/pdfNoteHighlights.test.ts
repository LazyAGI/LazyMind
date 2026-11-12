import {beforeEach,expect,it} from 'vitest';
import {pdfNoteHighlightHitAreas,pdfNoteHighlightPaths,readPdfNoteHighlights,writePdfNoteHighlights} from './pdfNoteHighlights';

beforeEach(()=>sessionStorage.clear());

it('keeps normalized colored highlights for a PDF source across a reload',()=>{
 const key='paper:source-a';
 const highlights=[{id:'page-1-quote',page:1,color:'blue' as const,rects:[[.1,.2,.3,.23] as [number,number,number,number]]}];
 writePdfNoteHighlights(key,highlights);
 expect(readPdfNoteHighlights(key)).toEqual(highlights);
 expect(readPdfNoteHighlights('paper:source-b')).toEqual([]);
});

it('ignores malformed stored geometry and unknown colors',()=>{
 sessionStorage.setItem('bad',JSON.stringify([{id:'bad',page:1,color:'orange',rects:[[0,0,2,1]]},{id:'good',page:2,color:'yellow',rects:[[.1,.1,.2,.2]]}]));
 expect(readPdfNoteHighlights('bad')).toEqual([{id:'good',page:2,color:'yellow',rects:[[.1,.1,.2,.2]]}]);
});

it('uses the saved note color when local and server highlights cover the same text',()=>{
 const rect:[number,number,number,number]=[.1,.2,.3,.23];
 const paths=pdfNoteHighlightPaths([{id:'old-local',page:1,color:'yellow',rects:[rect]},{id:'saved-note',page:1,color:'blue',rects:[rect]}],1);
 expect(paths).toHaveLength(1);
 expect(paths[0].color).toBe('blue');
});

it('keeps each saved note id on its clickable highlight rectangles',()=>{
 const areas=pdfNoteHighlightHitAreas([
  {id:'note-a',page:2,color:'yellow',rects:[[.1,.2,.3,.24],[.1,.25,.4,.29]]},
  {id:'note-b',page:1,color:'blue',rects:[[.5,.6,.8,.64]]},
 ],2);
 expect(areas).toEqual([
  {id:'note-a',color:'yellow',rect:[.1,.2,.3,.24]},
  {id:'note-a',color:'yellow',rect:[.1,.25,.4,.29]},
 ]);
});
