import {useMemo} from 'react';
import katex from 'katex';

function MathSpan({latex,fallback}:{latex:string;fallback:string}) {
  const html=useMemo(()=>{
    try{return katex.renderToString(latex,{displayMode:false,throwOnError:true,trust:false,strict:'ignore',maxExpand:1000});}
    catch{return undefined;}
  },[latex]);
  // Only KaTeX's own sanitized output becomes HTML. Prose and failed parses
  // always remain React text nodes, including model-supplied HTML strings.
  return html?<span className="pdf-study-inline-math" dangerouslySetInnerHTML={{__html:html}}/>:<span>{fallback}</span>;
}
export function FormulaMathText({text,mathOnly=false}:{text:string;mathOnly?:boolean}) {
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
  return <>{parts.map((part,index)=>part.latex===undefined?<span key={index}>{part.text}</span>:<MathSpan key={index} latex={part.latex} fallback={part.text}/>)}</>;
}
