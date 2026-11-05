import {afterEach,expect,it,vi} from 'vitest';
import {cropPdfBlockPreview} from './pdfBlockPreview';

afterEach(()=>vi.restoreAllMocks());

it('crops a figure from the actual rendered PDF canvas using its source coordinates',()=>{
 const page=document.createElement('div');
 const canvas=document.createElement('canvas');canvas.width=1000;canvas.height=1500;page.append(canvas);
 const drawImage=vi.fn();
 vi.spyOn(HTMLCanvasElement.prototype,'getContext').mockReturnValue({drawImage} as unknown as CanvasRenderingContext2D);
 vi.spyOn(HTMLCanvasElement.prototype,'toDataURL').mockReturnValue('data:image/png;base64,original');
 expect(cropPdfBlockPreview(page,[.2,.1,.6,.3])).toBe('data:image/png;base64,original');
 expect(drawImage).toHaveBeenCalledWith(canvas,200,150,400,300,0,0,400,300);
});

it('does not invent a preview when geometry or a PDF canvas is unavailable',()=>{
 expect(cropPdfBlockPreview(document.createElement('div'),[.2,.1,.6,.3])).toBeUndefined();
 const page=document.createElement('div');page.append(document.createElement('canvas'));
 expect(cropPdfBlockPreview(page,[.8,.2,.2,.3])).toBeUndefined();
});
