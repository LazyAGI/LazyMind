import {act,renderHook,waitFor} from '@testing-library/react';
import {beforeEach,expect,it,vi} from 'vitest';
import {readPdfPinBoard,writePdfPinBoard,type PdfPinRecord} from './pdfPinStore';
import {usePdfPinBoard} from './usePdfPinBoard';

vi.mock('./pdfPinStore',async original=>({...await original<typeof import('./pdfPinStore')>(),readPdfPinBoard:vi.fn(),writePdfPinBoard:vi.fn()}));

const pin=(id:string):PdfPinRecord=>({id,kind:'formula',title:`Formula ${id}`,blockId:id,geometry:{x:20,y:30,width:400,height:300},minimized:true});
beforeEach(()=>{
  vi.mocked(readPdfPinBoard).mockReset().mockResolvedValue([]);
  vi.mocked(writePdfPinBoard).mockReset().mockResolvedValue(true);
});

it('waits for saved pins before accepting changes, and ignores an old paper finishing late',async()=>{
  let finishOld:(pins:PdfPinRecord[])=>void=()=>{};
  vi.mocked(readPdfPinBoard).mockImplementation(key=>key==='paper-a'?new Promise(resolve=>{finishOld=resolve;}):Promise.resolve([pin('b')]));
  const view=renderHook(({key})=>usePdfPinBoard(key,'pdf'),{initialProps:{key:'paper-a'}});
  expect(view.result.current.ready).toBe(false);
  act(()=>expect(view.result.current.add(pin('early'))).toBe(false));
  expect(writePdfPinBoard).not.toHaveBeenCalled();
  view.rerender({key:'paper-b'});
  await waitFor(()=>expect(view.result.current.records.map(item=>item.id)).toEqual(['b']));
  await act(async()=>finishOld([pin('a')]));
  expect(view.result.current.records.map(item=>item.id)).toEqual(['b']);
});

it('keeps six saved items and refuses a seventh without overwriting the board',async()=>{
  vi.mocked(readPdfPinBoard).mockResolvedValue(Array.from({length:6},(_,index)=>pin(String(index))));
  const view=renderHook(()=>usePdfPinBoard('paper','pdf'));
  await waitFor(()=>expect(view.result.current.ready).toBe(true));
  act(()=>expect(view.result.current.add(pin('seventh'))).toBe(false));
  expect(view.result.current.records).toHaveLength(6);
  act(()=>expect(view.result.current.update('2',{minimized:false})).toBe(true));
  expect(view.result.current.records.find(item=>item.id==='2')?.minimized).toBe(false);
  expect(writePdfPinBoard).toHaveBeenCalledTimes(1);
});
