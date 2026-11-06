import type {FormulaExplanation,FormulaEvidence} from '@/modules/knowledge/api/formulaExplanation';
import type {VisualExplanation} from '@/modules/knowledge/api/paperReader';

export interface PdfPinGeometry {x:number;y:number;width:number;height:number}
interface PdfPinBase {id:string;title:string;geometry:PdfPinGeometry;minimized:boolean}
export type PdfRegionPinRecord=PdfPinBase&{kind:'region';page:number;bbox:[number,number,number,number];image:string};
export type PdfStudyPinRecord=PdfPinBase&{kind:'formula'|'table'|'image';blockId:string;page?:number;view?:'visual';explanation?:FormulaExplanation|VisualExplanation;sourcePreview?:string};
export type PdfPinRecord=PdfRegionPinRecord|PdfStudyPinRecord;

const DATABASE='lazymind-pdf-pins-v1';
const STORE='boards';
const MAX_PINS=6;
const MAX_IMAGE_BYTES=6*1024*1024;
const MAX_SNAPSHOT_BYTES=512*1024;
const pendingWrites=new Map<string,Promise<boolean>>();

const finite=(value:unknown):value is number=>typeof value==='number'&&Number.isFinite(value);
const positivePage=(value:unknown):value is number=>Number.isInteger(value)&&Number(value)>0&&Number(value)<=100_000;
const zeroBasedPage=(value:unknown):value is number=>Number.isInteger(value)&&Number(value)>=0&&Number(value)<100_000;
const boundedText=(value:unknown,limit:number):value is string=>typeof value==='string'&&value.trim().length>0&&value.length<=limit;
const stringWithin=(value:unknown,limit:number):value is string=>typeof value==='string'&&value.length<=limit;
const objectRecord=(value:unknown):value is Record<string,unknown>=>!!value&&typeof value==='object'&&!Array.isArray(value);

function stringList(value:unknown,maxItems:number,maxLength:number):string[]|undefined{
 if(!Array.isArray(value)||value.length>maxItems||!value.every(item=>stringWithin(item,maxLength)))return;
 return value;
}

function normalizeEvidence(value:unknown):FormulaEvidence|undefined{
 if(!objectRecord(value)||!boundedText(value.id,512)||!stringWithin(value.text,16_000)||!zeroBasedPage(value.page))return;
 const bbox=value.bbox;
 if(bbox!==undefined&&(!Array.isArray(bbox)||bbox.length!==4||!bbox.every(coordinate=>finite(coordinate)&&coordinate>=0&&coordinate<=1)||bbox[0]>=bbox[2]||bbox[1]>=bbox[3]))return;
 return {id:value.id,text:value.text,page:value.page,...(bbox===undefined?{}:{bbox:[bbox[0],bbox[1],bbox[2],bbox[3]]})};
}

function evidenceList(value:unknown):FormulaEvidence[]|undefined{
 if(!Array.isArray(value)||value.length>96)return;
 const evidence=value.map(normalizeEvidence);
 return evidence.every(Boolean)?evidence as FormulaEvidence[]:undefined;
}

function fitsSnapshot(value:FormulaExplanation|VisualExplanation):boolean{
 try{return new TextEncoder().encode(JSON.stringify(value)).byteLength<=MAX_SNAPSHOT_BYTES;}
 catch{return false;}
}

function normalizeFormulaExplanation(value:unknown):FormulaExplanation|undefined{
 if(!objectRecord(value)||!boundedText(value.summary,32_000)||value.reading!==undefined&&!stringWithin(value.reading,64_000)||value.display_latex!==undefined&&!stringWithin(value.display_latex,64_000)||value.repair_note!==undefined&&!stringWithin(value.repair_note,16_000)||value.model!==undefined&&!stringWithin(value.model,256))return;
 if(!Array.isArray(value.symbols)||value.symbols.length>128||!Array.isArray(value.steps)||value.steps.length>96)return;
 const symbols=value.symbols.map(item=>{
  if(!objectRecord(item)||!boundedText(item.symbol,2048)||!stringWithin(item.meaning,16_000)||typeof item.inferred!=='boolean')return;
  const ids=stringList(item.evidence_ids,96,512);
  return ids?{symbol:item.symbol,meaning:item.meaning,evidence_ids:ids,inferred:item.inferred}:undefined;
 });
 const steps=value.steps.map(item=>{
  if(!objectRecord(item)||!stringWithin(item.text,16_000)||typeof item.inferred!=='boolean')return;
  const ids=stringList(item.evidence_ids,96,512);
  return ids?{text:item.text,evidence_ids:ids,inferred:item.inferred}:undefined;
 });
 const evidence=evidenceList(value.evidence),warnings=stringList(value.warnings,96,16_000);
 if(symbols.some(item=>!item)||steps.some(item=>!item)||!evidence||!warnings)return;
 const normalized:FormulaExplanation={summary:value.summary,symbols:symbols as FormulaExplanation['symbols'],steps:steps as FormulaExplanation['steps'],evidence,warnings,
  ...(value.reading===undefined?{}:{reading:value.reading}),...(value.display_latex===undefined?{}:{display_latex:value.display_latex}),...(value.repair_note===undefined?{}:{repair_note:value.repair_note}),...(value.model===undefined?{}:{model:value.model})};
 return fitsSnapshot(normalized)?normalized:undefined;
}

function normalizeVisualExplanation(value:unknown):VisualExplanation|undefined{
 if(!objectRecord(value)||!boundedText(value.source_sha256,256)||!boundedText(value.summary,32_000)||value.model!==undefined&&!stringWithin(value.model,256)||!Array.isArray(value.observations)||value.observations.length>96)return;
 const observations=value.observations.map(item=>{
  if(!objectRecord(item)||!stringWithin(item.text,16_000)||typeof item.inferred!=='boolean')return;
  const ids=stringList(item.evidence_ids,96,512);
  return ids?{text:item.text,evidence_ids:ids,inferred:item.inferred}:undefined;
 });
 const evidence=evidenceList(value.evidence),warnings=stringList(value.warnings,96,16_000);
 if(observations.some(item=>!item)||!evidence||!warnings)return;
 const normalized:VisualExplanation={source_sha256:value.source_sha256,summary:value.summary,observations:observations as VisualExplanation['observations'],evidence,warnings,...(value.model===undefined?{}:{model:value.model})};
 return fitsSnapshot(normalized)?normalized:undefined;
}

function validImage(value:unknown):value is string{
 if(typeof value!=='string'||value.length<28||value.length>MAX_IMAGE_BYTES*4/3+64)return false;
 const prefix=/^data:image\/(?:png|jpeg|webp);base64,/i.exec(value)?.[0];
 if(!prefix)return false;
 const encoded=value.slice(prefix.length);
 if(!encoded||encoded.length%4!==0||!/^[A-Za-z0-9+/]+={0,2}$/.test(encoded))return false;
 const padding=encoded.endsWith('==')?2:encoded.endsWith('=')?1:0;
 return encoded.length/4*3-padding<=MAX_IMAGE_BYTES;
}

function normalizeRecord(value:unknown):PdfPinRecord|undefined{
 if(!value||typeof value!=='object'||Array.isArray(value))return;
 const record=value as Record<string,unknown>;
 const {id,title,geometry,minimized,kind}=record;
 if(!boundedText(id,256)||!boundedText(title,240)||typeof minimized!=='boolean'||!geometry||typeof geometry!=='object'||Array.isArray(geometry))return;
 const box=geometry as Record<string,unknown>;
 if(!finite(box.x)||!finite(box.y)||!finite(box.width)||!finite(box.height)||box.x<0||box.y<0||box.x>100_000||box.y>100_000||box.width<=0||box.height<=0||box.width>10_000||box.height>10_000)return;
 const base={id,title,geometry:{x:box.x,y:box.y,width:box.width,height:box.height},minimized};
 if(kind==='region'){
  const bbox=record.bbox;
  if(!positivePage(record.page)||!Array.isArray(bbox)||bbox.length!==4||!bbox.every((coordinate:unknown)=>finite(coordinate)&&coordinate>=0&&coordinate<=1)||bbox[0]>=bbox[2]||bbox[1]>=bbox[3]||!validImage(record.image))return;
  return {...base,kind,page:record.page,bbox:[bbox[0],bbox[1],bbox[2],bbox[3]],image:record.image};
 }
 if(kind==='formula'||kind==='table'||kind==='image'){
  if(!boundedText(record.blockId,512)||record.page!==undefined&&!positivePage(record.page))return;
  if(record.view!==undefined&&(kind!=='table'||record.view!=='visual'))return;
  if(record.sourcePreview!==undefined&&(kind==='formula'||kind==='table'&&record.view!=='visual'||!validImage(record.sourcePreview)))return;
  if(record.explanation!==undefined&&kind==='table'&&record.view!=='visual')return;
  const explanation=record.explanation===undefined?undefined:kind==='formula'?normalizeFormulaExplanation(record.explanation):normalizeVisualExplanation(record.explanation);
  if(record.explanation!==undefined&&!explanation)return;
  return {...base,kind,blockId:record.blockId,...(record.page===undefined?{}:{page:record.page}),...(record.view===undefined?{}:{view:'visual' as const}),...(explanation===undefined?{}:{explanation}),...(record.sourcePreview===undefined?{}:{sourcePreview:record.sourcePreview})};
 }
}

export function isPersistablePdfPin(record:PdfPinRecord):boolean{
 return !!normalizeRecord(record);
}

function normalizeBoard(value:unknown):PdfPinRecord[]{
 if(!Array.isArray(value))return [];
 const seen=new Set<string>();
 const reversed:PdfPinRecord[]=[];
 for(let index=value.length-1;index>=0&&reversed.length<MAX_PINS;index--){
  const record=normalizeRecord(value[index]);
  if(record&&!seen.has(record.id)){seen.add(record.id);reversed.push(record);}
 }
 return reversed.reverse();
}

function openDatabase():Promise<IDBDatabase>{
 return new Promise((resolve,reject)=>{
  if(typeof indexedDB==='undefined'){reject(new Error('IndexedDB unavailable'));return;}
  let request:IDBOpenDBRequest;
  try{request=indexedDB.open(DATABASE,1);}catch(error){reject(error);return;}
  request.onupgradeneeded=()=>{
   try{if(!request.result.objectStoreNames.contains(STORE))request.result.createObjectStore(STORE);}catch(error){reject(error);}
  };
  request.onsuccess=()=>resolve(request.result);
  request.onerror=()=>reject(request.error);
  request.onblocked=()=>reject(new Error('IndexedDB blocked'));
 });
}

async function readStored(key:string):Promise<PdfPinRecord[]>{
 let database:IDBDatabase|undefined;
 try{
  database=await openDatabase();
  const transaction=database.transaction(STORE,'readonly');
  const request=transaction.objectStore(STORE).get(key);
  const value=await new Promise<unknown>((resolve,reject)=>{
   request.onsuccess=()=>resolve(request.result);
   request.onerror=()=>reject(request.error);
  });
  return normalizeBoard(value);
 }catch{return [];}finally{database?.close();}
}

async function writeStored(key:string,records:PdfPinRecord[]):Promise<boolean>{
 let database:IDBDatabase|undefined;
 try{
  database=await openDatabase();
  await new Promise<void>((resolve,reject)=>{
   const transaction=database!.transaction(STORE,'readwrite');
   transaction.oncomplete=()=>resolve();
   transaction.onerror=()=>reject(transaction.error);
   transaction.onabort=()=>reject(transaction.error);
   const store=transaction.objectStore(STORE);
   if(records.length)store.put(records,key);else store.delete(key);
  });
  return true;
 }catch{return false;}
 finally{database?.close();}
}

export async function readPdfPinBoard(key?:string):Promise<PdfPinRecord[]>{
 if(!key)return [];
 await pendingWrites.get(key);
 return readStored(key);
}

export async function writePdfPinBoard(key:string|undefined,records:PdfPinRecord[]):Promise<boolean>{
 if(!key)return false;
 const valid=normalizeBoard(records);
 const previous=pendingWrites.get(key);
 const write=(previous||Promise.resolve()).then(()=>writeStored(key,valid));
 pendingWrites.set(key,write);
 void write.finally(()=>{if(pendingWrites.get(key)===write)pendingWrites.delete(key);});
 return write;
}
