import {useEffect, useMemo, useRef, useState} from 'react';
import {useTranslation} from 'react-i18next';
import katex from 'katex';
import type {PdfIRBlock, PdfIRTarget} from '@/modules/knowledge/api/pdfIR';
import {useFormulaExplanation, type FormulaEvidence} from '@/modules/knowledge/api/formulaExplanation';
import {InteractiveFormula} from './InteractiveFormula';
import {FormulaMathText} from './FormulaMathText';
import {formulaNumber} from './pdfFormulaReferences';
import {interactiveFormulaSymbols} from './interactiveFormulaSymbols';
import './PdfFormulaStudy.css';
interface Props {
 target:PdfIRTarget; sourceKey:string; blocks:PdfIRBlock[]; selected?:PdfIRBlock; autoExplain?:boolean; compact?:boolean;
 onSelect:(block:PdfIRBlock)=>void; onNavigate:(evidence:FormulaEvidence)=>void;
 onReturn:()=>void; canReturn:boolean; onClose:()=>void;
}
export function PdfFormulaStudy({target,sourceKey,blocks,selected,autoExplain=false,compact=false,onSelect,onNavigate,onReturn,canReturn,onClose}:Props){
 const {t}=useTranslation();
 const panel=useRef<HTMLElement>(null);
 useEffect(()=>{panel.current?.focus({preventScroll:true});},[]);
 const explanation=useFormulaExplanation(target,sourceKey,selected?.id,autoExplain);
 const data=explanation.data;
 const [elapsed,setElapsed]=useState(0);
 useEffect(()=>{
  if(explanation.status!=='loading'){setElapsed(0);return;}
  const start=Date.now();const timer=window.setInterval(()=>setElapsed(Math.floor((Date.now()-start)/1000)),1000);
  return()=>window.clearInterval(timer);
 },[explanation.status,selected?.id]);
 const repairedLatex=useMemo(()=>{
  const candidate=data?.display_latex?.trim();
  if(!candidate || candidate===selected?.latex || !interactiveFormulaSymbols(candidate,[]).safe)return;
  try{katex.renderToString(candidate,{displayMode:true,throwOnError:true,trust:false,strict:'ignore',maxExpand:1000});return candidate;}
  catch{return undefined;}
 },[data?.display_latex,selected?.latex]);
 const citedEvidence=useMemo(()=>{
  const cited=new Set([...(data?.symbols || []).flatMap(symbol=>symbol.evidence_ids || []),...(data?.steps || []).flatMap(step=>step.evidence_ids || [])]);
  const seen=new Set<string>();
  return (data?.evidence || []).filter(evidence=>{
   if(!cited.has(evidence.id) || seen.has(evidence.id))return false;
   seen.add(evidence.id);return true;
  });
 },[data]);
 const sources=(ids:string[],_inferred:boolean)=>{
  const evidence=Array.from(new Set(ids || [])).map(id=>data?.evidence.find(e=>e.id===id)).filter((e):e is FormulaEvidence=>!!e);
  return <div className="pdf-study-citations">{evidence.map(e=><button key={e.id} className="pdf-study-citation" onClick={()=>onNavigate(e)} title={e.text}>{t('pdfFormula.sourcePage',{page:e.page+1})}</button>)}</div>;
 };
 const symbolList=data?.symbols?.length?<dl className="pdf-study-symbols">{data.symbols.map((symbol,index)=><div key={index}><dt><FormulaMathText text={symbol.symbol} mathOnly/></dt><dd><FormulaMathText text={symbol.meaning}/>{!compact&&sources(symbol.evidence_ids,symbol.inferred)}</dd></div>)}</dl>:null;
 const catalog=<div className="pdf-study-navigation"><label>{t('pdfFormula.catalog')}<select aria-label={t('pdfFormula.catalog')} value={selected?.id || ''} onChange={event=>{const block=blocks.find(b=>b.id===event.target.value);if(block)onSelect(block);}}><option value="" disabled>{t('pdfFormula.chooseFormula')}</option>{blocks.map((block,index)=><option key={block.id} value={block.id}>{formulaNumber(block)?`(${formulaNumber(block)})`:t('pdfFormula.formulaIndex',{number:index+1})} · {t('pdfFormula.page',{page:block.page+1})}</option>)}</select></label>{canReturn&&<button onClick={onReturn}>{t('pdfFormula.returnReading')}</button>}</div>;
 const explanationDetails=data&&<><p className="pdf-study-disclaimer">{t('pdfFormula.interpretationNote')}</p>
  {!!data.steps?.length&&<section><h3>{t('pdfFormula.steps')}</h3><ol className="pdf-study-steps">{data.steps.map((step,index)=><li key={index}><p><FormulaMathText text={step.text}/></p>{sources(step.evidence_ids,step.inferred)}</li>)}</ol></section>}
  <section className="pdf-study-evidence"><h3>{t('pdfFormula.evidence')}</h3><p className="pdf-study-section-label">{t('pdfFormula.quoteNote')}</p>{citedEvidence.length?citedEvidence.map(e=><article key={e.id}><button className="pdf-study-citation" onClick={()=>onNavigate(e)}>{t('pdfFormula.sourcePage',{page:e.page+1})}</button><blockquote>{e.text}</blockquote>{!e.bbox&&<small>{t('pdfFormula.pageOnly')}</small>}</article>):<p>{t('pdfFormula.noEvidence')}</p>}</section>
  {!!data.warnings?.length&&<section className="pdf-study-warnings"><h3>{t('pdfFormula.limitations')}</h3><ul>{data.warnings.map((warning,index)=><li key={index}>{warning}</li>)}</ul></section>}
  {data.model&&<p className="pdf-study-model">{t('pdfFormula.model')}: {data.model}</p>}
 </>;
 return <aside ref={panel} tabIndex={-1} className="pdf-formula-study" aria-label={t('pdfFormula.study')} onMouseUp={event=>event.stopPropagation()}>
  <header><div><span className="pdf-study-eyebrow">{t('pdfFormula.studyEyebrow')}</span><h2>{t('pdfFormula.study')}</h2></div><button className="pdf-study-close" aria-label={t('pdfFormula.closeStudy')} onClick={onClose}>×</button></header>
  {!selected&&catalog}
  {!selected?<div className="pdf-study-empty"><strong>{t('pdfFormula.chooseFormula')}</strong><p>{t('pdfFormula.catalogHint')}</p></div>:<div className="pdf-study-body">
   <section className="pdf-study-selected">{repairedLatex&&<div className="pdf-study-repair" role="note"><strong>{t('pdfFormula.repairSuggested')}</strong><span>{data?.repair_note || t('pdfFormula.repairNote')}</span></div>}<InteractiveFormula key={`${sourceKey}:${selected.id}`} latex={repairedLatex || selected.latex || ""} originalLatex={selected.latex || ""} sourceKey={`${target.dataset}/${target.document}/${sourceKey}`} definitions={data?.symbols || []} evidence={data?.evidence || []} compactEvidence={compact} onNavigate={onNavigate}/></section>
   {data&&<section className="pdf-study-summary"><h3>{t('pdfFormula.summary')}</h3><p><FormulaMathText text={data.summary}/></p></section>}
   {explanation.status==='loading'?<div className="pdf-study-loading" role="status"><div className="pdf-study-generation"><div className="pdf-study-generation-label"><span>{t('pdfFormula.explaining')}</span><time aria-live="off">{elapsed}s</time></div><progress aria-label={t('pdfFormula.explaining')}/></div><button onClick={explanation.cancel}>{t('pdfFormula.cancel')}</button></div>:<div className="pdf-study-actions"><button className="pdf-study-primary" onClick={()=>void explanation.load(explanation.status==='ready')}>{explanation.status==='ready'?t('pdfFormula.regenerate'):explanation.status==='error'?t('pdfFormula.explainRetry'):t('pdfFormula.explain')}</button>{explanation.status==='error'&&<p role="alert">{explanation.error || t('pdfFormula.explainError')}</p>}{explanation.status==='cancelled'&&<p role="status">{t('pdfFormula.explainCancelled')}</p>}</div>}

   {data&&symbolList&&(compact?<details className="pdf-study-symbol-catalog"><summary>{t('pdfFormula.symbols')} ({data.symbols.length})</summary>{symbolList}</details>:<section><h3>{t('pdfFormula.symbols')}</h3>{symbolList}</section>)}
   {compact?<details className="pdf-study-more"><summary>{data?t('pdfFormula.moreDetails'):t('pdfFormula.catalog')}</summary>{explanationDetails}{catalog}</details>:<>{explanationDetails}{catalog}</>}
  </div>}
 </aside>;
}
