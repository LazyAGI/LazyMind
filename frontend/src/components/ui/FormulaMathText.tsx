import {useMemo} from 'react';
import katex from 'katex';

type KnownMathAlias={alias:string;latex:string};
const noKnownSymbols:readonly string[]=[];
const unsafeLatexCommand=/\\(?:href|url|includegraphics|html(?:Class|Id|Style|Data))\b/;

function groupedCommand(source:string,start:number) {
  if(source[start]!=='{')return;
  let depth=0;
  for(let index=start;index<source.length;index++){
    if(source[index]==='{')depth++;
    else if(source[index]==='}'&&--depth===0)return {text:source.slice(start+1,index),end:index+1};
  }
}
function legacyAlias(latex:string,root:'ascii'|'unicode') {
  const source=latex.replace(/\s+/g,'');
  let output='';
  for(let index=0;index<source.length;){
    if(source[index]!=="\\"){output+=source[index++];continue;}
    const command=source.slice(index+1).match(/^[A-Za-z]+/)?.[0];
    if(!command){output+=source[index+1] || '';index+=2;continue;}
    const after=index+command.length+1;
    const group=groupedCommand(source,after);
    if(group&&command==='sqrt'){
      const inner=legacyAlias(group.text,root);
      output+=root==='unicode'?`√${inner}`:`sqrt(${inner})`;
      index=group.end;continue;
    }
    if(group&&['hat','bar','tilde','vec'].includes(command)){
      output+=`${command}_${legacyAlias(group.text,root)}`;
      index=group.end;continue;
    }
    if(group&&['mathbf','mathrm','mathcal','boldsymbol'].includes(command)){
      output+=legacyAlias(group.text,root);
      index=group.end;continue;
    }
    output+=command;index=after;
  }
  return output.replace(/_\{([A-Za-z0-9]+)\}/g,'_$1');
}
function knownMathAliases(symbols:readonly string[]) {
  const aliases=new Map<string,string>();
  for(const value of symbols){
    const latex=value.trim();
    if(!latex||unsafeLatexCommand.test(latex))continue;
    try{katex.renderToString(latex,{displayMode:false,throwOnError:true,trust:false,strict:'ignore',maxExpand:1000});}
    catch{continue;}
    const compact=latex.replace(/\s+/g,'');
    for(const alias of [compact,legacyAlias(compact,'ascii'),legacyAlias(compact,'unicode')]){
      if(!alias||!(/[\\_^]/.test(alias)||alias.includes('√')||alias.includes('sqrt(')))continue;
      if(!aliases.has(alias))aliases.set(alias,latex);
    }
  }
  return Array.from(aliases,([alias,latex])=>({alias,latex})).sort((left,right)=>right.alias.length-left.alias.length);
}

function MathSpan({latex,fallback}:{latex:string;fallback:string}) {
  const html=useMemo(()=>{
    try{return katex.renderToString(latex,{displayMode:false,throwOnError:true,trust:false,strict:'ignore',maxExpand:1000});}
    catch{return undefined;}
  },[latex]);
  // Only KaTeX's own sanitized output becomes HTML. Prose and failed parses
  // always remain React text nodes, including model-supplied HTML strings.
  return html?<span className="pdf-study-inline-math" dangerouslySetInnerHTML={{__html:html}}/>:<span>{fallback}</span>;
}
function IndexedProseMath({text}:{text:string}) {
  // Conservatively recognize bare single-letter indexed variables, including
  // legacy cached meanings. Do not reinterpret ordinary snake_case words.
  const pattern=/(?<![A-Za-z0-9_\\])([A-Za-z])_([A-Za-z0-9]+|\{[^{}\n]+\})(?![A-Za-z0-9_])/g;
  const nodes:React.ReactNode[]=[];let offset=0;
  for(const match of text.matchAll(pattern)){
    nodes.push(text.slice(offset,match.index));
    const sub=match[2].startsWith('{')?match[2]:`{${match[2]}}`;
    nodes.push(<MathSpan key={match.index} latex={`${match[1]}_${sub}`} fallback={match[0]}/>);
    offset=match.index!+match[0].length;
  }
  nodes.push(text.slice(offset));return <>{nodes}</>;
}
function ProseMath({text,aliases}:{text:string;aliases:KnownMathAlias[]}) {
  const pattern=useMemo(()=>aliases.length?new RegExp(`(?<![A-Za-z0-9_])(${aliases.map(item=>item.alias.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')).join('|')})(?![A-Za-z0-9_])`,'g'):undefined,[aliases]);
  if(!pattern)return <IndexedProseMath text={text}/>;
  const byAlias=new Map(aliases.map(item=>[item.alias,item.latex]));
  const nodes:React.ReactNode[]=[];let offset=0;
  for(const match of text.matchAll(pattern)){
    if(match.index!>offset)nodes.push(<IndexedProseMath key={`text-${offset}`} text={text.slice(offset,match.index)}/>);
    nodes.push(<MathSpan key={`math-${match.index}`} latex={byAlias.get(match[0])!} fallback={match[0]}/>);
    offset=match.index!+match[0].length;
  }
  if(offset<text.length)nodes.push(<IndexedProseMath key={`text-${offset}`} text={text.slice(offset)}/>);
  return <>{nodes}</>;
}
export function FormulaMathText({text,mathOnly=false,knownSymbols=noKnownSymbols}:{text:string;mathOnly?:boolean;knownSymbols?:readonly string[]}) {
  const aliases=useMemo(()=>knownMathAliases(knownSymbols),[knownSymbols]);
  const parts=useMemo(()=>{
    const pattern=/\\\(([\s\S]*?)\\\)|\\\[([\s\S]*?)\\\]|(?<!\\)\$\$([\s\S]*?)(?<!\\)\$\$|(?<!\\)\$([^\n$]*?)(?<!\\)\$/g;
    const output:Array<{text:string;latex?:string}>=[];
    let offset=0;
    for(const match of text.matchAll(pattern)) {
      if(match.index!>offset)output.push({text:text.slice(offset,match.index)});
      output.push({text:match[0],latex:match[1]??match[2]??match[3]??match[4]});
      offset=match.index!+match[0].length;
    }
    if(offset<text.length)output.push({text:text.slice(offset)});
    return output;
  },[text]);
  if(mathOnly && !parts.some(part=>part.latex!==undefined))return <MathSpan latex={text} fallback={text}/>;
  return <>{parts.map((part,index)=>part.latex===undefined?<ProseMath key={index} text={part.text} aliases={aliases}/>:<MathSpan key={index} latex={part.latex} fallback={part.text}/>)}</>;
}
