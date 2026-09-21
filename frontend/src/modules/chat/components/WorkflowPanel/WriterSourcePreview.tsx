import { Children, isValidElement, useEffect, useMemo, useState, type ReactNode } from 'react';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import remarkMath from 'remark-math';
import rehypeRaw from 'rehype-raw';
import rehypeSanitize, { defaultSchema } from 'rehype-sanitize';
import rehypeKatex from 'rehype-katex';
import katex from 'katex';
import 'katex/dist/katex.min.css';
import { useTranslation } from 'react-i18next';
import MermaidBlock from '../MarkdownViewer/MermaidBlock';
import { highlightCode } from '../MarkdownViewer/syntaxHighlight';
import { resolveMarkdownImageUrlAsync, type MarkdownImageResolver } from '@/modules/knowledge/utils/imageUrl';
import { writerCallouts, writerNoteTarget, writerSafeUrl, writerSourceMarkdown, writerPreviewMarkdown } from './writerSourceSyntax';
import type { DocumentRenderContext } from '@/api/generated/core-client';
import { WriterMapPreview, WriterModelPreview } from './WriterGeometryPreview';
import './WriterSourcePreview.scss';

const schema = {
  ...defaultSchema,
  tagNames: [...(defaultSchema.tagNames ?? []), 'details','summary','video','audio','source','picture'],
  attributes: {
    ...defaultSchema.attributes,
    details: ['open', 'dataCallout'],
    video: ['src', 'controls', 'poster'], audio: ['src', 'controls'], source: ['src', 'srcSet', 'type'],
    img:[...(defaultSchema.attributes?.img ?? []),'width','height'],
    code:[['className', /^language-./]],
  },
  protocols: { ...defaultSchema.protocols, href:['http','https','mailto','writer-note'], src:['http','https','writer-embed'] },
};

function nodeText(node: ReactNode): string {
  return Children.toArray(node).map((child) => isValidElement<{children?:ReactNode}>(child) ? nodeText(child.props.children) : String(child)).join('');
}
export function WriterFormula({ source, block = true }: { source: string; block?: boolean }) {
  const input = block ? source.trim().replace(/^\$\$([\s\S]*)\$\$$/, '$1').replace(/^\\\[([\s\S]*)\\\]$/, '$1') : source.replace(/^\$([^]*)\$$/, '$1');
  try { return <span className={block ? 'writer-source__formula' : 'writer-source__inline-formula'} dangerouslySetInnerHTML={{__html:katex.renderToString(input,{displayMode:block,throwOnError:true,trust:false,strict:'ignore'})}} />; }
  catch { return <code>{source}</code>; }
}

function SourceImage({ src, alt, width, height, resolve }: {src:string;alt?:string;width?:number|string;height?:number|string;resolve:MarkdownImageResolver}) {
  const [url,setUrl]=useState(''); const [failed,setFailed]=useState(false); const [attempt,setAttempt]=useState(0);
  const {t}=useTranslation();
  useEffect(() => { let active=true; setUrl(''); setFailed(false);
    void resolve(src).then((value)=> {if(active) { const safe=writerSafeUrl(value); if(safe)setUrl(safe);else setFailed(true); }}).catch(()=>{if(active)setFailed(true);});
    return ()=>{active=false;};
  },[src,resolve,attempt]);
  return failed ? <span role='status'>{alt || src} <button type='button' onClick={()=>setAttempt(attempt+1)}>{t('chat.writerSource.retry')}</button></span>
    : url ? <img src={url} alt={alt ?? ''} width={width} height={height} style={height ? {height: Number(height), objectFit:'contain'} : undefined} loading='lazy' onError={()=>setFailed(true)} /> : <span role='status'>{t('chat.writerSource.loading')}</span>;
}

function SourceCode({ source, language }: {source:string;language:string}) {
  const {t}=useTranslation(); const html=highlightCode(source,language);
  let preview:ReactNode;
  if(language==='mermaid') return <MermaidBlock code={source} />;
  if(language==='math'||language==='latex') preview=<WriterFormula source={source} />;
  if(language==='geojson'||language==='topojson') preview=<WriterMapPreview source={source} language={language} />;
  if(language==='stl') preview=<WriterModelPreview source={source} />;
  return <div className='writer-source__code'>{preview}
    {preview ? <details><summary>{t('chat.writerSource.source')}</summary><pre><code>{source}</code></pre></details>
      : <pre><code className={`language-${language}`} {...(html?{dangerouslySetInnerHTML:{__html:html}}:{children:source})} /></pre>}
  </div>;
}

export interface WriterSourcePreviewProps {source:string;resolveImage?:MarkdownImageResolver;renderContext?:DocumentRenderContext}
export function WriterSourcePreview({source,resolveImage=resolveMarkdownImageUrlAsync,renderContext}:WriterSourcePreviewProps) {
  const {t}=useTranslation();
  const [projected,setProjected]=useState<{source:string;context?:DocumentRenderContext;value:string}>();
  useEffect(()=>{
    let active=true;
    if (renderContext) void writerPreviewMarkdown(source,renderContext).then(value=>{
      if(active)setProjected({source,context:renderContext,value});
    }).catch(()=>{if(active)setProjected({source,context:renderContext,value:source});});
    return ()=>{active=false;};
  },[source,renderContext]);
  const display=projected?.source===source && projected.context===renderContext ? projected.value : source;
  const markdown=useMemo(()=>writerSourceMarkdown(display),[display]);
  const components:Components={
    pre:({children})=>{
      const code=Children.toArray(children).find(isValidElement) as React.ReactElement<{className?:string;children?:ReactNode}>|undefined;
      return code ? <SourceCode source={nodeText(code.props.children).replace(/\n$/,'')} language={code.props.className?.replace('language-','')??'text'} /> : <pre>{children}</pre>;
    },
    img:({src,alt,width,height})=>{
      if(src?.startsWith('writer-embed:')) {
        try {
          const [path,label] = decodeURIComponent(src.slice(13)).split('|');
          if (!/\.(png|jpe?g|gif|webp|svg|avif)(?:[?#]|$)/i.test(path)) return <span title={t('chat.writerSource.unavailable')}>{label || path}</span>;
          const dimensions = label?.match(/^([0-9]+)(?:x([0-9]+))?$/);
          return <SourceImage src={path} alt={dimensions ? '' : label || ''} width={dimensions?.[1]} height={dimensions?.[2]} resolve={resolveImage} />;
        } catch { return <span>{alt}</span>; }
      }
      return <SourceImage src={src??''} alt={alt} width={width} height={height} resolve={resolveImage} />;
    },
    a:({href,children,id})=>{
      let url=href;
      if(href?.startsWith('writer-note:')) {try{url=writerNoteTarget(decodeURIComponent(href.slice(12))).url;}catch{url=undefined;}}
      const safe=url&&writerSafeUrl(url);
      return safe ? <a id={id} href={safe} target={safe.startsWith('#')?undefined:'_blank'} rel='noreferrer' onClick={(event)=>{
        if(!safe.startsWith('#'))return;
        const root=event.currentTarget.closest('[data-writer-source-preview]');
        let target=safe.slice(1);try{target=decodeURIComponent(target);}catch{return;}
        const nodes=Array.from(root?.querySelectorAll<HTMLElement>('[id]')??[]).filter((node)=>node.id===target||node.id===`user-content-${target}`);
        if(nodes.length===1){event.preventDefault();nodes[0].scrollIntoView?.({block:'start'});}
      }}>{children}</a> : <span id={id} title={t('chat.writerSource.unavailable')}>{children}</span>;
    },

    video: ({src,children}) => <video src={src && writerSafeUrl(src)} controls preload='none'>{children}</video>,
    audio: ({src,children}) => <audio src={src && writerSafeUrl(src)} controls preload='none'>{children}</audio>,
    source: ({src,type,srcSet}) => <source src={src && writerSafeUrl(src)} type={type} srcSet={srcSet?.split(',').every(item => writerSafeUrl(item.trim().split(/\s+/)[0])) ? srcSet : undefined} />,

  };
  for(const tag of ['h1','h2','h3','h4','h5','h6'] as const) {
    components[tag]=({children})=>{ const id=nodeText(children).trim().replace(/\s+/g,'-'); const Tag=tag; return <Tag id={id}>{children}</Tag>; };
  }
  return <div className='writer-source' data-writer-source-preview>
    <ReactMarkdown remarkRehypeOptions={{footnoteLabel:t('chat.writerSource.footnotes'),footnoteBackLabel:t('chat.writerSource.backToText')}} remarkPlugins={[remarkGfm,remarkMath,writerCallouts]} rehypePlugins={[rehypeRaw,[rehypeSanitize,schema],rehypeKatex]}
      urlTransform={(url)=>url.startsWith('writer-note:')||url.startsWith('writer-embed:')?url:writerSafeUrl(url)??''} components={components}>{markdown}</ReactMarkdown>
  </div>;
}
