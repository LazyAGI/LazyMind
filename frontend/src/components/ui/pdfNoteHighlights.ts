export const PDF_NOTE_COLORS = [
  {key:'red',label:'红色',swatch:'#d62c21'},
  {key:'orange',label:'橙色',swatch:'#b87408'},
  {key:'yellow',label:'黄色',swatch:'#d6b827'},
  {key:'green',label:'绿色',swatch:'#1bb83b'},
  {key:'blue',label:'蓝色',swatch:'#1645db'},
  {key:'indigo',label:'靛色',swatch:'#4e36ae'},
  {key:'purple',label:'紫色',swatch:'#862ac0'},
  {key:'rainbow',label:'彩色',swatch:'conic-gradient(#ed4337,#ecb52e,#37b85a,#2b88df,#892dbe,#ed4337)'},
] as const;

export type PdfNoteColor = typeof PDF_NOTE_COLORS[number]['key'];
export type PdfNoteHighlight = {id:string;page:number;color:PdfNoteColor;rects:Array<[number,number,number,number]>};
export type PdfHighlightRect = [number,number,number,number];

// A single SVG path paints overlapping subpaths once. Separate translucent
// elements would darken the same text each time their rectangles intersect.
export function pdfHighlightPath(rects:readonly PdfHighlightRect[]):string {
  return rects.map(([left,top,right,bottom])=>`M${left} ${top}H${right}V${bottom}H${left}Z`).join(' ');
}

export function pdfNoteHighlightPaths(highlights:readonly PdfNoteHighlight[],page:number):Array<{color:PdfNoteColor;path:string}> {
  const latestColor=new Map<string,{color:PdfNoteColor;rect:PdfHighlightRect}>();
  for(const highlight of highlights){
    if(highlight.page!==page)continue;
    for(const rect of highlight.rects)latestColor.set(rect.join(','),{color:highlight.color,rect});
  }
  const byColor=new Map<PdfNoteColor,Map<string,PdfHighlightRect>>();
  for(const [key,{color,rect}] of latestColor){
    let rects=byColor.get(color);
    if(!rects){rects=new Map();byColor.set(color,rects);}
    rects.set(key,rect);
  }
  return [...byColor].map(([color,rects])=>({color,path:pdfHighlightPath([...rects.values()])}));
}

const validColors = new Set<string>(PDF_NOTE_COLORS.map(item=>item.key));
const validRect = (value:unknown):value is [number,number,number,number] =>
  Array.isArray(value) && value.length===4 && value.every((n:unknown)=>typeof n==='number' && Number.isFinite(n) && n>=0 && n<=1) && value[0]<value[2] && value[1]<value[3];

export function readPdfNoteHighlights(key?:string):PdfNoteHighlight[] {
  if(!key)return [];
  try {
    const data:unknown=JSON.parse(sessionStorage.getItem(key)||'[]');
    if(!Array.isArray(data))return [];
    return data.filter((item):item is PdfNoteHighlight=>item && typeof item.id==='string' && item.id.length<512 && Number.isInteger(item.page) && item.page>0 && validColors.has(item.color) && Array.isArray(item.rects) && item.rects.length>0 && item.rects.length<=50 && item.rects.every(validRect)).slice(-200);
  } catch {return [];}
}

export function writePdfNoteHighlights(key:string|undefined,highlights:PdfNoteHighlight[]) {
  if(!key)return;
  try {sessionStorage.setItem(key,JSON.stringify(highlights.slice(-200)));} catch { /* Annotation remains visible this session when storage is unavailable. */ }
}

export function pdfNoteHighlightBackground(color:PdfNoteColor) {
  const swatch=PDF_NOTE_COLORS.find(item=>item.key===color)?.swatch || PDF_NOTE_COLORS[2].swatch;
  return color==='rainbow' ? 'linear-gradient(90deg,#ed433766,#ecb52e66,#37b85a66,#2b88df66,#892dbe66)' : `color-mix(in srgb, ${swatch} 32%, transparent)`;
}
