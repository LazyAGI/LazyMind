import type {PdfIRBlock} from '@/modules/knowledge/api/pdfIR';
import {paperVisualLabel,usePaperReaderLabels} from './paperReaderLabels';
export function PdfFigureLayer({blocks,allBlocks,onSelect}:{blocks:PdfIRBlock[];allBlocks:PdfIRBlock[];onSelect:(block:PdfIRBlock)=>void}){
 const {label}=usePaperReaderLabels();
 return <div className="pdf-figure-layer">{blocks.filter(block=>{const box=block.bbox;return box&&box.length===4&&box.every(value=>Number.isFinite(value)&&value>=0&&value<=1)&&box[0]<box[2]&&box[1]<box[3];}).map(block=><button key={block.id} style={{left:`${Math.min(94,block.bbox![2]*100)}%`,top:`${block.bbox![1]*100}%`}} aria-label={paperVisualLabel(block,allBlocks,label)} onClick={()=>onSelect(block)}>▧</button>)}</div>;
}
