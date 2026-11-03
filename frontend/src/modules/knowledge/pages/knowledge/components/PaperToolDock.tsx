import type {ReactNode} from 'react';
import {Tooltip} from 'antd';
interface Props {
  label: string;
  tools: {id:string; label:string; icon:ReactNode}[];
  activeTool?: string;
  onChange: (tool:string|undefined)=>void;
  children?: ReactNode;
}
export function PaperToolDock({label,tools,activeTool,onChange,children}:Props) {
  return <nav className="paper-workspace-dock" aria-label={label}>
    {children}
    {tools.map(tool=><Tooltip key={tool.id} title={tool.label} placement="left" trigger={["hover","focus"]} mouseEnterDelay={0.15}><button type="button" aria-label={tool.label} aria-pressed={activeTool===tool.id} onClick={()=>onChange(activeTool===tool.id?undefined:tool.id)}>{tool.icon}</button></Tooltip>)}
  </nav>;
}
