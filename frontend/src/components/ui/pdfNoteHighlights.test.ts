import {beforeEach,expect,it} from 'vitest';
import {readPdfNoteHighlights,writePdfNoteHighlights} from './pdfNoteHighlights';

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
