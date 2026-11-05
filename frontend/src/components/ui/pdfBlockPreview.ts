/** Crop only pixels already rendered by PDF.js; no model or generated image is involved. */
export function cropPdfBlockPreview(page?:HTMLElement|null,bbox?:[number,number,number,number]):string|undefined {
 if(!page||!bbox||bbox.length!==4||bbox.some(value=>!Number.isFinite(value)||value<0||value>1)||bbox[0]>=bbox[2]||bbox[1]>=bbox[3])return;
 const source=page.querySelector<HTMLCanvasElement>('.react-pdf__Page__canvas')||page.querySelector<HTMLCanvasElement>('canvas');
 if(!source?.width||!source.height)return;
 const left=Math.floor(bbox[0]*source.width),top=Math.floor(bbox[1]*source.height);
 const right=Math.floor(bbox[2]*source.width),bottom=Math.floor(bbox[3]*source.height);
 const width=Math.max(1,right-left),height=Math.max(1,bottom-top);
 try {
  const output=window.document.createElement('canvas');output.width=width;output.height=height;
  const context=output.getContext('2d');if(!context)return;
  context.drawImage(source,left,top,width,height,0,0,width,height);
  return output.toDataURL('image/png');
 } catch {return;}
}
