import type {PdfIRBlock,PdfIRDocument,PdfIRTarget} from '@/modules/knowledge/api/pdfIR';
import {PAPER_STAGES,paperSourceKey,readablePaperText,usePaperReaderResource,type PaperGuide} from '@/modules/knowledge/api/paperReader';
import {FormulaMathText} from './FormulaMathText';
import {usePaperReaderLabels,paperVisualLabel} from './paperReaderLabels';
import './PdfPaperOverview.css';
interface Props{target:PdfIRTarget;document:PdfIRDocument;active:boolean;paperTitle?:string;onNavigate:(id:string)=>void;onStudy:(block:PdfIRBlock)=>void}

// Only explicit title blocks and explicitly labelled abstracts qualify as metadata.
function paperMetadata(document:PdfIRDocument,paperTitle?:string){
 const firstPage=document.blocks.filter(block=>block.page===0);
 const isAbstractLabel=(block:PdfIRBlock)=>/^(?:abstract|摘要)\s*[.:：]?$/i.test(block.text?.trim()||'');
 const title=firstPage.find(block=>block.type==='title'&&block.text?.trim()&&!isAbstractLabel(block));
 const abstract=firstPage.find(block=>block.type==='abstract'&&!isAbstractLabel(block)||/^(?:abstract|摘要)\s*[.:：—-]\s*\S/i.test(block.text?.trim()||''));
 let abstractText=abstract?.text?.replace(/^(?:abstract|摘要)\s*[.:：—-]?\s*/i,'')||'';
 if(!abstractText){
  const headingIndex=firstPage.findIndex(isAbstractLabel);
  if(headingIndex>=0){
   const paragraphs:string[]=[];
   for(const block of firstPage.slice(headingIndex+1)){
    if(block.type==='heading'||block.type==='title'||/^(?:\d+(?:\.\d+)*\.?\s+)?(?:introduction|background|keywords|引言|绪论|关键词)\s*[.:：]?$/i.test(block.text?.trim()||''))break;
    if(block.type!=='text'||!block.text?.trim())continue;
    if(paragraphs.length>=3||paragraphs.join(' ').length+block.text.length>6000)break;
    paragraphs.push(block.text.trim());
   }
   abstractText=paragraphs.join(' ');
  }
 }
 return {title:readablePaperText(title?.text||paperTitle||''),abstract:readablePaperText(abstractText)};
}
function VisualPreview({block}:{block:PdfIRBlock}){
 if(block.type==='formula'&&(block.latex||block.text))return <div className="pdf-overview-formula"><FormulaMathText text={block.latex||block.text||''} mathOnly/></div>;
 if(block.type==='table'&&block.table)return <>{!!block.table.rows.length&&<div className="pdf-overview-table"><table><tbody>{block.table.rows.slice(0,4).map((row,index)=><tr key={index}>{row.cells.slice(0,5).map((cell,column)=><td key={column}><FormulaMathText text={cell.text}/></td>)}</tr>)}</tbody></table></div>}{!!block.table.captions.length&&<p className="pdf-overview-caption"><FormulaMathText text={block.table.captions.join(' ')}/></p>}</>;
 if(block.type==='image'){
  const caption=block.image?.captions.join(' ')||block.text;
  return caption?<p className="pdf-overview-caption"><FormulaMathText text={caption}/></p>:null;
 }
 return null;
}
export function PdfPaperGuide({target,document,active,paperTitle,onNavigate,onStudy}:Props){
 const {label,language}=usePaperReaderLabels();
 const request=usePaperReaderResource<PaperGuide>(target,paperSourceKey(target,document),document.source_sha256,'guide',language,active);
 const loading=!request.status || request.status==='loading';
 const blocks=new Map(document.blocks.map(block=>[block.id,block]));
 const metadata=paperMetadata(document,paperTitle);
 return <article className="pdf-paper-guide pdf-paper-overview">
  {(metadata.title||metadata.abstract)&&<header className="pdf-overview-paper-header">{metadata.title&&<h1>{metadata.title}</h1>}{metadata.abstract&&<div className="pdf-overview-abstract"><h2>{language==='zh'?'摘要':'Abstract'}</h2><p><FormulaMathText text={metadata.abstract}/></p></div>}</header>}
  <header className="pdf-overview-guide-header"><div><h2>{language==='zh'?'论文概览':'Paper overview'}</h2><p>{label('guideNote')}</p></div><button onClick={()=>void request.load(true)} disabled={loading}>{request.status==='error'?label('retry'):label('regenerate')}</button></header>
  {loading&&<div className="pdf-reader-status" role="status">{label('loading')} <button onClick={request.cancel}>{label('cancel')}</button></div>}
  {request.status==='error'&&<p className="pdf-reader-error" role="alert">{request.sourceMismatch?label('sourceChanged'):(request.error || label('error'))}</p>}
  <div className="pdf-overview-sections">{PAPER_STAGES.map(key=>{
   const stage=request.data?.stages.find(item=>item.key===key);
   const evidence=Array.from(new Set(stage?.evidence_ids || [])).map(id=>request.data?.evidence.find(item=>item.id===id&&blocks.has(id))).filter(Boolean);
   const related=Array.from(new Set([...(stage?.related_block_ids || []),...(stage?.evidence_ids || [])])).map(id=>blocks.get(id)).filter((block):block is PdfIRBlock=>!!block&&['formula','table','image'].includes(block.type));
   return <section key={key} className="pdf-guide-stage pdf-overview-section"><h3>{label(key==='insight'?'insightStage':key)}</h3>{(stage || loading || request.status==='ready')&&<p className="pdf-overview-summary"><FormulaMathText text={stage?.summary || label(loading?'loading':'noStage')}/></p>}<div className="pdf-overview-evidence">{evidence.map(item=><button key={item!.id} title={item!.text} onClick={()=>onNavigate(item!.id)}>{label('sourcePage',{page:blocks.get(item!.id)!.page+1})}</button>)}</div>{!!related.length&&<div className="pdf-overview-visuals">{related.map(block=><figure key={block.id} className="pdf-overview-visual"><VisualPreview block={block}/><figcaption><button onClick={()=>onStudy(block)}>{paperVisualLabel(block,document.blocks,label)}</button><span>{label('pageLabel',{page:block.page+1})}</span></figcaption></figure>)}</div>}</section>;
  })}</div>
  {!!request.data?.warnings?.length&&<details className="pdf-reader-notes"><summary>{label('limitations')}</summary>{request.data.warnings.map((warning,index)=><p key={index}>{warning}</p>)}</details>}
 </article>;
}
