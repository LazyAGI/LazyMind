import type {PdfIRBlock,PdfIRDocument,PdfIRTarget} from '@/modules/knowledge/api/pdfIR';
import {PAPER_STAGES,paperSourceKey,usePaperReaderResource,type PaperGuide} from '@/modules/knowledge/api/paperReader';
import {FormulaMathText} from './FormulaMathText';
import {usePaperReaderLabels,paperVisualLabel} from './paperReaderLabels';
interface Props{target:PdfIRTarget;document:PdfIRDocument;active:boolean;onNavigate:(id:string)=>void;onStudy:(block:PdfIRBlock)=>void}
export function PdfPaperGuide({target,document,active,onNavigate,onStudy}:Props){
 const {label,language}=usePaperReaderLabels();
 const request=usePaperReaderResource<PaperGuide>(target,paperSourceKey(target,document),document.source_sha256,'guide',language,active);
 const loading=!request.status || request.status==='loading';
 const blocks=new Map(document.blocks.map(block=>[block.id,block]));
 return <div className="pdf-paper-guide">
  <header className="pdf-reader-section-header"><div><h2>{label('guideTitle')}</h2><p>{label('guideNote')}</p></div><button onClick={()=>void request.load(true)} disabled={loading}>{request.status==='error'?label('retry'):label('regenerate')}</button></header>
  {loading&&<div className="pdf-reader-status" role="status">{label('loading')} <button onClick={request.cancel}>{label('cancel')}</button></div>}
  {request.status==='error'&&<p className="pdf-reader-error" role="alert">{request.sourceMismatch?label('sourceChanged'):(request.error || label('error'))}</p>}
  <div className="pdf-guide-stages">{PAPER_STAGES.map((key,index)=>{
   const stage=request.data?.stages.find(item=>item.key===key);
   const evidence=Array.from(new Set(stage?.evidence_ids || [])).map(id=>request.data?.evidence.find(item=>item.id===id&&blocks.has(id))).filter(Boolean);
   const related=Array.from(new Set([...(stage?.related_block_ids || []),...(stage?.evidence_ids || [])])).map(id=>blocks.get(id)).filter((block):block is PdfIRBlock=>!!block&&['formula','table','image'].includes(block.type));
   return <section key={key} className="pdf-guide-stage"><div className="pdf-guide-number">{index+1}</div><div className="pdf-guide-content"><h3>{label(key==='insight'?'insightStage':key)}</h3>{(stage || loading || request.status==='ready')&&<p><FormulaMathText text={stage?.summary || label(loading?'loading':'noStage')}/></p>}<div className="pdf-guide-evidence">{evidence.map(item=><button key={item!.id} title={item!.text} onClick={()=>onNavigate(item!.id)}>{label('sourcePage',{page:item!.page+1})}</button>)}</div>{!!related.length&&<div className="pdf-guide-related">{related.map(block=><button key={block.id} onClick={()=>onStudy(block)}>{paperVisualLabel(block,document.blocks,label)}</button>)}</div>}</div></section>;
  })}</div>
  {!!request.data?.warnings?.length&&<details className="pdf-reader-notes"><summary>{label('limitations')}</summary>{request.data.warnings.map((warning,index)=><p key={index}>{warning}</p>)}</details>}
 </div>;
}
