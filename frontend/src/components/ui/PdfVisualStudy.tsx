import {useEffect,useState} from 'react';
import type {PdfIRBlock,PdfIRDocument,PdfIRTarget} from '@/modules/knowledge/api/pdfIR';
import {paperSourceKey,readablePaperText,usePaperReaderResource,type VisualExplanation} from '@/modules/knowledge/api/paperReader';
import {FormulaMathText} from './FormulaMathText';
import {paperVisualLabel,usePaperReaderLabels} from './paperReaderLabels';
import {PdfTableStudy} from './PdfTableStudy';
import './PdfVisualStudy.css';
interface Props {target:PdfIRTarget;document:PdfIRDocument;block:PdfIRBlock;sourcePreview?:string;initialExplanation?:VisualExplanation;autoExplain?:boolean;onExplanationChange?:(data:VisualExplanation)=>void;onNavigate:(id:string)=>void;onInspectTable:(block:PdfIRBlock)=>void;onClose:()=>void;onReturn:()=>void;canReturn:boolean}
export function PdfVisualStudy({target,document,block,sourcePreview,initialExplanation,autoExplain=true,onExplanationChange,onNavigate,onInspectTable,onClose,onReturn,canReturn}:Props){
 const {label,language}=usePaperReaderLabels();
 const text=(zh:string,en:string)=>language==='zh'?zh:en;
 const request=usePaperReaderResource<VisualExplanation>(target,paperSourceKey(target,document),document.source_sha256,`blocks/${encodeURIComponent(block.id)}/explanation`,language,autoExplain,initialExplanation,onExplanationChange);
 const [tableView,setTableView]=useState<'cells'|'original'>('cells');
 const [expanded,setExpanded]=useState(false),[elapsed,setElapsed]=useState(0);
 useEffect(()=>{setExpanded(false);},[block.id,document.source_sha256]);
 useEffect(()=>{
  if(request.status!=='loading')return;
  const start=Date.now();setElapsed(0);
  const timer=window.setInterval(()=>setElapsed(Math.floor((Date.now()-start)/1000)),1000);
  return()=>window.clearInterval(timer);
 },[request.status]);
 const sourceIds=new Set(document.blocks.map(item=>item.id));
 const caption=block.table?.captions || block.image?.captions || (block.text?[block.text]:[]);
 const cited=new Set(request.data?.observations.flatMap(item=>item.evidence_ids) || []);
 const evidence=(request.data?.evidence || []).filter(item=>sourceIds.has(item.id)&&cited.has(item.id));
 const title=paperVisualLabel(block,document.blocks,label);
 return <aside className="pdf-visual-study" aria-label={label('visualTitle')}>
  <header><div><h2>{title} <span>{text('研读','study')}</span></h2></div><button aria-label={label('dismiss')} onClick={onClose}>×</button></header>
  <div className="pdf-visual-source">
   {block.table&&<div className="pdf-visual-view-switch" role="group" aria-label={text('表格显示','Table display')}><button aria-pressed={tableView==='cells'} onClick={()=>setTableView('cells')}>{text('交互表格','Interactive table')}</button>{sourcePreview&&<button aria-pressed={tableView==='original'} onClick={()=>setTableView('original')}>{text('原始表格','Original table')}</button>}</div>}
   {block.table&&tableView==='cells'&&<PdfTableStudy embedded sourceKey={paperSourceKey(target,document)} blocks={[block]} allBlocks={document.blocks} selected={block} onSelect={()=>{}} onNavigate={evidence=>onNavigate(evidence.id)} onReturn={onReturn} canReturn={false} onClose={onClose}/>}
   {sourcePreview&&(!block.table||tableView==='original')&&<figure className={`pdf-visual-preview${expanded?' is-expanded':''}`}><button className="pdf-visual-zoom" aria-label={expanded?text('缩小图表','Reduce figure'):text('放大图表','Enlarge figure')} aria-pressed={expanded} onClick={()=>setExpanded(value=>!value)}><img src={sourcePreview} alt={title}/></button></figure>}
   {caption.length>0&&<details className="pdf-visual-caption"><summary>{text('原始图注','Original caption')}</summary><FormulaMathText text={caption.map(readablePaperText).join('\n')}/></details>}
   <div className="pdf-visual-source-actions">{block.type==='table'&&!block.table&&<button onClick={()=>onInspectTable(block)}>{label('openTable')}</button>}<button onClick={()=>onNavigate(block.id)}>{label('originalRegion')}</button>{canReturn&&<button onClick={onReturn}>{label('back')}</button>}</div>
  </div>
  <div className="pdf-visual-body">
   {request.status==='loading'&&<div className="pdf-visual-generation" role="status"><div><span>{request.data?text('正在更新解读…','Updating explanation…'):text('正在解读图表…','Explaining this visual…')}</span><time aria-live="off">{elapsed}s</time></div><progress aria-label={text('图表解读生成进度','Visual explanation progress')}/><div><small>{text('完成后一次呈现全部解读，点击各项无需再次生成。','The complete explanation will appear together; exploring it needs no further generation.')}</small><button onClick={request.cancel}>{label('cancel')}</button></div></div>}
   {request.status==='error'&&<p role="alert">{request.sourceMismatch?label('sourceChanged'):(request.error || label('error'))}{request.data&&text(' 已保留上次解读。',' Your previous explanation is still available.')}</p>}
   {request.status==='cancelled'&&<p role="status">{text('已取消生成。','Generation cancelled.')}{request.data&&text(' 已保留上次解读。',' Your previous explanation is still available.')}</p>}
   {request.data&&<>
    <section className="pdf-visual-takeaway"><h3>{text('这张图表说明什么','What this visual tells us')}</h3><p><FormulaMathText text={request.data.summary}/></p></section>
    {!!request.data.observations.length&&<section className="pdf-visual-findings"><h3>{text('理解要点','Reading the visual')}</h3>{request.data.observations.map((item,index)=><article key={index}><FormulaMathText text={item.text}/></article>)}</section>}
    <details className="pdf-visual-provenance"><summary>{text('原文依据与解读范围','Sources and scope')}</summary><p>{label('visualNote')}</p>{evidence.map(item=><blockquote key={item.id}><button onClick={()=>onNavigate(item.id)}>{label('sourcePage',{page:item.page+1})}</button><p><FormulaMathText text={readablePaperText(item.text)}/></p></blockquote>)}{request.data.warnings?.map((warning,index)=><p key={index}>{warning}</p>)}{request.data.model&&<small>{text('生成模型：','Generated with: ')}{request.data.model}</small>}</details>
   </>}
   {request.status!=='loading'&&<div className="pdf-visual-footer"><button onClick={()=>void request.load(!!request.data)}>{request.data?label('regenerate'):label('retry')}</button></div>}
  </div>
 </aside>;
}
