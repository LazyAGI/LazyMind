import type {PdfIRBlock,PdfIRDocument,PdfIRTarget} from '@/modules/knowledge/api/pdfIR';
import {paperSourceKey,readablePaperText,usePaperReaderResource,type VisualExplanation} from '@/modules/knowledge/api/paperReader';
import {FormulaMathText} from './FormulaMathText';
import {paperVisualLabel,usePaperReaderLabels} from './paperReaderLabels';
interface Props {target:PdfIRTarget;document:PdfIRDocument;block:PdfIRBlock;onNavigate:(id:string)=>void;onInspectTable:(block:PdfIRBlock)=>void;onClose:()=>void;onReturn:()=>void;canReturn:boolean}
export function PdfVisualStudy({target,document,block,onNavigate,onInspectTable,onClose,onReturn,canReturn}:Props){
 const {label,language}=usePaperReaderLabels();
 const request=usePaperReaderResource<VisualExplanation>(target,paperSourceKey(target,document),document.source_sha256,`blocks/${encodeURIComponent(block.id)}/explanation`,language,true);
 const sourceIds=new Set(document.blocks.map(item=>item.id));
 const caption=block.table?.captions || block.image?.captions || (block.text?[block.text]:[]);
 const cited=new Set(request.data?.observations.flatMap(item=>item.evidence_ids) || []);
 const evidence=(request.data?.evidence || []).filter(item=>sourceIds.has(item.id)&&cited.has(item.id));
 return <aside className="pdf-visual-study" aria-label={label('visualTitle')}>
  <header><div><span>{paperVisualLabel(block,document.blocks,label)}</span><h2>{label('visualTitle')}</h2></div><button aria-label={label('dismiss')} onClick={onClose}>×</button></header>
  <div className="pdf-visual-source"><p>{caption.length?caption.map(readablePaperText).join('\n'):label('noCaption')}</p><div><button onClick={()=>onNavigate(block.id)}>{label('originalRegion')}</button>{block.type==='table'&&<button onClick={()=>onInspectTable(block)}>{label('openTable')}</button>}{canReturn&&<button onClick={onReturn}>{label('back')}</button>}</div></div>
  <div className="pdf-visual-body"><p className="pdf-bilingual-note">{label('visualNote')}</p>
   {request.status==='loading'?<p role="status">{label('loading')} <button onClick={request.cancel}>{label('cancel')}</button></p>:<button onClick={()=>void request.load(true)}>{request.status==='ready'?label('regenerate'):label('retry')}</button>}
   {request.status==='error'&&<p role="alert">{request.sourceMismatch?label('sourceChanged'):(request.error || label('error'))}</p>}
   {request.data&&<><p className="pdf-visual-summary"><FormulaMathText text={request.data.summary}/></p><h3>{label('observations')}</h3><ol>{request.data.observations.map((item,index)=><li key={index}><p><FormulaMathText text={item.text}/></p><div className="pdf-guide-evidence">{Array.from(new Set(item.evidence_ids)).map(id=>request.data?.evidence.find(source=>source.id===id&&sourceIds.has(id))).filter(Boolean).map(source=><button key={source!.id} title={source!.text} onClick={()=>onNavigate(source!.id)}>{label('sourcePage',{page:source!.page+1})}</button>)}</div></li>)}</ol>{!!evidence.length&&<section><h3>{label('evidence')}</h3>{evidence.map(item=><blockquote key={item.id}><button onClick={()=>onNavigate(item.id)}>{label('sourcePage',{page:item.page+1})}</button><p>{item.text}</p></blockquote>)}</section>}{!!request.data.warnings?.length&&<details><summary>{label('limitations')}</summary>{request.data.warnings.map((warning,index)=><p key={index}>{warning}</p>)}</details>}</>}
  </div>
 </aside>;
}
