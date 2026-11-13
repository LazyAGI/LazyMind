import {afterEach,beforeEach,expect,it,vi} from 'vitest';
import type {FormulaExplanation} from '@/modules/knowledge/api/formulaExplanation';
import type {VisualExplanation} from '@/modules/knowledge/api/paperReader';
import {isPersistablePdfPin,readPdfPinBoard,writePdfPinBoard,type PdfPinRecord} from './pdfPinStore';

const region:PdfPinRecord={
 id:'region-1',kind:'region',title:'第 2 页选区',
 geometry:{x:42,y:51,width:320,height:180},minimized:true,
 page:2,bbox:[.1,.2,.6,.5],image:'data:image/png;base64,ZmFrZQ==',
};
const formula:PdfPinRecord={
 id:'formula-1',kind:'formula',title:'Attention',
 geometry:{x:100,y:80,width:520,height:420},minimized:false,blockId:'formula-block-1',page:4,
};
const formulaSnapshot:FormulaExplanation={summary:'Scaled dot-product attention weights keys by a query.',reading:'The denominator normalizes dot products.',symbols:[{symbol:'Q',meaning:'Query matrix',evidence_ids:['equation-1'],inferred:false}],steps:[{text:'Multiply queries and keys.',evidence_ids:['equation-1'],inferred:false}],evidence:[{id:'equation-1',text:'Attention(Q,K,V)',page:4,bbox:[.1,.2,.8,.5]}],warnings:[],model:'test-model'};
const visualSnapshot:VisualExplanation={source_sha256:'paper-hash',summary:'The diagram shows the encoder and decoder.',observations:[{text:'Encoder is on the left.',evidence_ids:['figure-1'],inferred:false}],evidence:[{id:'figure-1',text:'Figure 1',page:4}],warnings:[],model:'test-model'};

function installIndexedDb(writeDelays:number[]=[]){
 const boards=new Map<string,unknown>();
 const stores=new Set<string>();
 let writeIndex=0;
 const request=(produce:()=>unknown,onDone?:()=>void,delay=0)=>{
  const value:{result?:unknown;onsuccess?:()=>void;onerror?:()=>void}={};
  const finish=()=>{value.result=produce();value.onsuccess?.();onDone?.();};
  if(delay)setTimeout(finish,delay);else queueMicrotask(finish);
  return value;
 };
 const database={
  objectStoreNames:{contains:(name:string)=>stores.has(name)},
  createObjectStore:(name:string)=>{stores.add(name);},
  transaction:()=>{
   const tx:{oncomplete?:()=>void;onerror?:()=>void;onabort?:()=>void;objectStore?:()=>unknown}={};
   const complete=()=>queueMicrotask(()=>tx.oncomplete?.());
   tx.objectStore=()=>({
    get:(key:string)=>request(()=>boards.get(key)),
    put:(value:unknown,key:string)=>request(()=>{boards.set(key,structuredClone(value));return key;},complete,writeDelays[writeIndex++]||0),
    delete:(key:string)=>request(()=>{boards.delete(key);return undefined;},complete),
   });
   return tx;
  },
  close:vi.fn(),
 };
 vi.stubGlobal('indexedDB',{open:()=>{
  const opened:{result?:typeof database;onsuccess?:()=>void;onerror?:()=>void;onblocked?:()=>void;onupgradeneeded?:()=>void}={};
  queueMicrotask(()=>{opened.result=database;if(!stores.has('boards'))opened.onupgradeneeded?.();opened.onsuccess?.();});
  return opened;
 }});
 return boards;
}

beforeEach(()=>vi.unstubAllGlobals());
afterEach(()=>vi.unstubAllGlobals());

it('restores the same six-slot board only for its scoped paper key',async()=>{
 installIndexedDb();
 await expect(writePdfPinBoard('user-a/paper-a/hash-a',[region,formula])).resolves.toBe(true);
 expect(await readPdfPinBoard('user-a/paper-a/hash-a')).toEqual([region,formula]);
 expect(await readPdfPinBoard('user-a/paper-b/hash-a')).toEqual([]);
 await writePdfPinBoard('user-a/paper-a/hash-a',[]);
 expect(await readPdfPinBoard('user-a/paper-a/hash-a')).toEqual([]);
});

it('keeps the latest six valid records and rejects unsafe or oversized images',async()=>{
 const boards=installIndexedDb();
 const many=Array.from({length:8},(_,index):PdfPinRecord=>({...formula,id:`formula-${index}`}));
 const unsafe={...region,id:'unsafe',image:'data:image/svg+xml;base64,PHN2Zz4='} as PdfPinRecord;
 const oversized={...region,id:'oversized',image:`data:image/png;base64,${'A'.repeat(8_388_612)}`} as PdfPinRecord;
 await writePdfPinBoard('paper',[unsafe,...many,oversized]);
 expect((await readPdfPinBoard('paper')).map(item=>item.id)).toEqual(['formula-2','formula-3','formula-4','formula-5','formula-6','formula-7']);
 boards.set('corrupt',[region,{...formula,id:'missing-block',blockId:''},{...region,id:'bad-box',bbox:[0,0,2,1]},{...region,id:'bad-data',image:'javascript:alert(1)'}]);
 expect(await readPdfPinBoard('corrupt')).toEqual([region]);
});

it('continues reading without storage when the key or IndexedDB is unavailable',async()=>{
 vi.stubGlobal('indexedDB',undefined);
 await expect(writePdfPinBoard('paper',[region])).resolves.toBe(false);
 await expect(writePdfPinBoard(undefined,[region])).resolves.toBe(false);
 await expect(readPdfPinBoard('paper')).resolves.toEqual([]);
 await expect(readPdfPinBoard(undefined)).resolves.toEqual([]);
});

it('serializes rapid writes and waits for a pending write before reading',async()=>{
 installIndexedDb([20,0]);
 const first=writePdfPinBoard('paper',[region]);
 const second=writePdfPinBoard('paper',[formula]);
 const latest=readPdfPinBoard('paper');
 await Promise.all([first,second]);
 expect(await latest).toEqual([formula]);
 expect(await readPdfPinBoard('paper')).toEqual([formula]);
});

it('persists validated formula and visual study snapshots and image previews',async()=>{
 installIndexedDb();
 const records:PdfPinRecord[]=[
  {...formula,explanation:{...formulaSnapshot,evidence:[{...formulaSnapshot.evidence[0],page:0}]}},
  {...formula,id:'image-1',kind:'image',blockId:'figure-1',explanation:visualSnapshot,sourcePreview:'data:image/png;base64,ZmFrZQ=='},
  {...formula,id:'table-1',kind:'table',blockId:'table-1',view:'visual',explanation:visualSnapshot,sourcePreview:'data:image/webp;base64,ZmFrZQ=='},
 ];
 expect(records.every(isPersistablePdfPin)).toBe(true);
 await writePdfPinBoard('paper/snapshots',records);
 expect(await readPdfPinBoard('paper/snapshots')).toEqual(records);
});

it('rejects malformed or oversized study snapshots and previews without losing legacy pins',async()=>{
 const boards=installIndexedDb();
 const malformed={...formula,id:'bad-symbol',explanation:{...formulaSnapshot,symbols:[{symbol:'Q',meaning:42,evidence_ids:[],inferred:false}]}} as unknown as PdfPinRecord;
 const huge={...formula,id:'huge',explanation:{...formulaSnapshot,summary:'x'.repeat(600_000)}} as PdfPinRecord;
 const unsafePreview={...formula,id:'unsafe-preview',kind:'image',sourcePreview:'data:image/svg+xml;base64,PHN2Zz4='} as PdfPinRecord;
 const wrongView={...formula,id:'wrong-view',kind:'table',sourcePreview:'data:image/png;base64,ZmFrZQ=='} as PdfPinRecord;
 const oversizedPreview={...formula,id:'oversized-preview',kind:'table',view:'visual',sourcePreview:`data:image/png;base64,${'A'.repeat(8_388_612)}`} as PdfPinRecord;
 const validTable={...formula,id:'table-visual',kind:'table',view:'visual',explanation:visualSnapshot} as PdfPinRecord;
 for(const invalid of [malformed,huge,unsafePreview,wrongView,oversizedPreview])expect(isPersistablePdfPin(invalid)).toBe(false);
 expect(isPersistablePdfPin(validTable)).toBe(true);
 boards.set('corrupt-studies',[formula,malformed,huge,unsafePreview,wrongView,oversizedPreview,validTable]);
 expect(await readPdfPinBoard('corrupt-studies')).toEqual([formula,validTable]);
});
