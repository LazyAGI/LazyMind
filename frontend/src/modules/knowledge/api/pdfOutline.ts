export interface PdfNativeOutlineItem { id:string; title:string; page:number; depth:number }

interface OutlineNode { title:string; dest?:string|unknown[]|null; items?:OutlineNode[] }
interface OutlineSource {
 getOutline():Promise<OutlineNode[]|null>;
 getDestination(name:string):Promise<unknown[]|null>;
 getPageIndex(reference:unknown):Promise<number>;
 numPages:number;
}

/** PDF bookmarks are available as soon as PDF.js opens the original file. */
export async function readNativePdfOutline(source:OutlineSource):Promise<PdfNativeOutlineItem[]> {
 const roots=await source.getOutline();
 const result:PdfNativeOutlineItem[]=[];
 async function visit(nodes:OutlineNode[],depth:number){
  for(const node of nodes){
   const title=node.title?.trim();
   try {
    const target=typeof node.dest==='string'?await source.getDestination(node.dest):node.dest;
    const reference=Array.isArray(target)?target[0]:undefined;
    const index=typeof reference==='number'?reference:reference?await source.getPageIndex(reference):-1;
    if(title&&index>=0&&index<source.numPages)result.push({id:`bookmark-${result.length}`,title,page:index+1,depth:Math.min(depth,3)});
   } catch { /* An invalid bookmark does not hide the rest of the outline. */ }
   if(node.items?.length)await visit(node.items,depth+1);
  }
 }
 if(roots)await visit(roots,0);
 return result;
}
