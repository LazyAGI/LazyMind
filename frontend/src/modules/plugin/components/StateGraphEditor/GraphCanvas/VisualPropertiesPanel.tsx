import { Button, ColorPicker as BaseColorPicker, InputNumber, Popover, Select, Slider, Switch, Tabs, Tooltip } from 'antd';
import { CloseOutlined, DownOutlined } from '@ant-design/icons';
import type { Color } from 'antd/es/color-picker';
import { useTranslation } from 'react-i18next';
import { useState } from 'react';
import type { EdgeVisual, GradientStop, NodeFill, NodeLayout, NodeVisibility } from '../core/model';
import { NODE_MIN_HEIGHT } from '../core/layout';
import { NODE_MIN_WIDTH } from './StepNode';

const colorString = (color: Color) => color.toHexString();
const visibilityFields: Array<[keyof NodeVisibility, string]> = [
  ['stepId', '步骤标识'], ['label', '展示名称'], ['outputs', '产出'], ['approval', '审批/执行标志'],
  ['conditionalRoute', '条件路由标志'], ['parallelRoute', '并行标志'], ['skippable', '可跳过标志'],
];
const gradientDirections = [
  { angle: 0, label: '从下到上' }, { angle: 45, label: '左下到右上' },
  { angle: 90, label: '从左到右' }, { angle: 135, label: '左上到右下' },
  { angle: 180, label: '从上到下' }, { angle: 225, label: '右上到左下' },
  { angle: 270, label: '从右到左' }, { angle: 315, label: '右下到左上' },
];
const gradientPresets = [
  { label:'清透蓝', angle:90, stops:[{offset:0,color:'#E6F4FF',opacity:1},{offset:.42,color:'#91CAFF',opacity:1},{offset:1,color:'#1677FF',opacity:1}] },
  { label:'深海', angle:135, stops:[{offset:0,color:'#36CFC9',opacity:1},{offset:.55,color:'#1677FF',opacity:1},{offset:1,color:'#10239E',opacity:1}] },
  { label:'薄荷绿', angle:90, stops:[{offset:0,color:'#F6FFED',opacity:1},{offset:.38,color:'#B7EB8F',opacity:1},{offset:1,color:'#52C41A',opacity:1}] },
  { label:'日落', angle:135, stops:[{offset:0,color:'#FFF7E6',opacity:1},{offset:.45,color:'#FFD591',opacity:1},{offset:.72,color:'#FF7A45',opacity:1},{offset:1,color:'#CF1322',opacity:1}] },
  { label:'晨光', angle:90, stops:[{offset:0,color:'#FFFBE6',opacity:1},{offset:.35,color:'#FFE58F',opacity:1},{offset:1,color:'#FA8C16',opacity:1}] },
  { label:'紫霞', angle:135, stops:[{offset:0,color:'#F9F0FF',opacity:1},{offset:.45,color:'#D3ADF7',opacity:1},{offset:1,color:'#722ED1',opacity:1}] },
  { label:'淡彩', angle:90, stops:[{offset:0,color:'#FFFFFF',opacity:.2},{offset:.3,color:'#91CAFF',opacity:.55},{offset:.72,color:'#D3ADF7',opacity:.8},{offset:1,color:'#FFADD2',opacity:1}] },
  { label:'银灰', angle:135, stops:[{offset:0,color:'#FFFFFF',opacity:1},{offset:.4,color:'#D9D9D9',opacity:1},{offset:1,color:'#595959',opacity:1}] },
  { label:'冰川', angle:135, stops:[{offset:0,color:'#F0FFFF',opacity:1},{offset:.32,color:'#87E8DE',opacity:1},{offset:.72,color:'#40A9FF',opacity:1},{offset:1,color:'#1D39C4',opacity:1}] },
  { label:'青柠', angle:90, stops:[{offset:0,color:'#FCFFE6',opacity:1},{offset:.38,color:'#D3F261',opacity:1},{offset:.7,color:'#7CB305',opacity:1},{offset:1,color:'#3F6600',opacity:1}] },
  { label:'珊瑚', angle:135, stops:[{offset:0,color:'#FFF2E8',opacity:1},{offset:.4,color:'#FFBB96',opacity:1},{offset:.72,color:'#FF7A45',opacity:1},{offset:1,color:'#AD2102',opacity:1}] },
  { label:'莓果', angle:90, stops:[{offset:0,color:'#FFF0F6',opacity:1},{offset:.36,color:'#FFADD2',opacity:1},{offset:.68,color:'#EB2F96',opacity:1},{offset:1,color:'#780650',opacity:1}] },
  { label:'极光', angle:135, stops:[{offset:0,color:'#13C2C2',opacity:1},{offset:.3,color:'#52C41A',opacity:.9},{offset:.62,color:'#722ED1',opacity:.9},{offset:1,color:'#1890FF',opacity:1}] },
  { label:'暮色', angle:90, stops:[{offset:0,color:'#120338',opacity:1},{offset:.42,color:'#391085',opacity:1},{offset:.74,color:'#C41D7F',opacity:1},{offset:1,color:'#FF85C0',opacity:1}] },
  { label:'香槟', angle:135, stops:[{offset:0,color:'#FFFFFF',opacity:1},{offset:.28,color:'#FFF1B8',opacity:1},{offset:.65,color:'#D4B106',opacity:.85},{offset:1,color:'#874D00',opacity:1}] },
  { label:'玻璃', angle:90, stops:[{offset:0,color:'#FFFFFF',opacity:.15},{offset:.28,color:'#E6F7FF',opacity:.45},{offset:.58,color:'#69C0FF',opacity:.7},{offset:1,color:'#FFFFFF',opacity:.25}] },
];
const colorGroups = [
  { label:'蓝色', colors:['#E6F4FF','#BAE0FF','#91CAFF','#4096FF','#1677FF','#0958D9','#003EB3'] },
  { label:'绿色', colors:['#F6FFED','#D9F7BE','#B7EB8F','#73D13D','#52C41A','#237804','#135200'] },
  { label:'红色', colors:['#FFF1F0','#FFCCC7','#FFA39E','#FF4D4F','#D9363E','#A8071A','#820014'] },
  { label:'橙黄', colors:['#FFF7E6','#FFE7BA','#FFD591','#FA8C16','#FADB14','#AD6800','#873800'] },
  { label:'紫色', colors:['#F9F0FF','#EFDBFF','#D3ADF7','#9254DE','#722ED1','#391085','#22075E'] },
  { label:'中性', colors:['#FFFFFF','#F0F0F0','#D9D9D9','#8C8C8C','#595959','#434343','#000000'] },
];

function VisualColorPicker({value,onChange,disabled=false}: {value:string;onChange:(color:string)=>void;disabled?:boolean}) {
  const presets=<div className="visual-color-presets">{colorGroups.map(group=><div className="visual-color-group" key={group.label}><span>{group.label}</span><div>{group.colors.map(color=><Tooltip title={color} key={color}><button type="button" className={value.toUpperCase()===color?'is-active':''} style={{background:color}} onClick={()=>onChange(color)} /></Tooltip>)}</div></div>)}</div>;
  return <BaseColorPicker disabled={disabled} value={value} onChange={(color)=>onChange(colorString(color))} panelRender={(panel)=><div className="visual-color-panel"><Tabs defaultActiveKey="preset" items={[{key:'preset',label:'常用颜色',children:presets},{key:'custom',label:'自定义',children:panel}]} /></div>} />;
}
function ColorPicker({value,onChange,disabled=false}: {value:string;onChange:(color:Color)=>void;disabled?:boolean}) {
  const presets=<div className="visual-color-presets">{colorGroups.map(group=><div className="visual-color-group" key={group.label}><span>{group.label}</span><div>{group.colors.map(color=><Tooltip title={color} key={color}><button type="button" style={{background:color}} onClick={()=>onChange({toHexString:()=>color} as Color)} /></Tooltip>)}</div></div>)}</div>;
  return <BaseColorPicker disabled={disabled} value={value} onChange={onChange} panelRender={(panel)=><div className="visual-color-panel"><Tabs defaultActiveKey="preset" items={[{key:'preset',label:'常用颜色',children:presets},{key:'custom',label:'自定义',children:panel}]} /></div>} />;
}

function GradientStopsEditor({fill,onChange,readonly}: {fill:NodeFill;onChange:(fill:NodeFill)=>void;readonly:boolean}) {
  const [requestedIndex,setRequestedIndex]=useState(0);
  const [dragStops,setDragStops]=useState<GradientStop[]|null>(null);
  const stops=dragStops??fill.stops??[];
  const selectedIndex=Math.max(0,Math.min(requestedIndex,stops.length-1));
  const selected=stops[selectedIndex];
  const background=`linear-gradient(90deg, ${stops.map(stop=>`${stop.color}${Math.round(stop.opacity*255).toString(16).padStart(2,'0')} ${stop.offset*100}%`).join(', ')})`;
  const replaceSelected=(patch:Partial<GradientStop>)=>{
    const updated={...selected,...patch};
    const next=stops.map((stop,index)=>index===selectedIndex?updated:stop).sort((a,b)=>a.offset-b.offset);
    const nextIndex=next.indexOf(updated);
    setRequestedIndex(nextIndex<0?selectedIndex:nextIndex);
    onChange({...fill,stops:next});
  };
  const offsetFromPointer=(clientX:number,currentTarget:HTMLElement)=>{
    const track=currentTarget.closest('.gradient-track') as HTMLElement|null;
    const rect=track?.getBoundingClientRect();
    if(!rect||rect.width===0)return selected.offset;
    return Math.max(0,Math.min(1,(clientX-rect.left)/rect.width));
  };
  const moveStop=(index:number,offset:number,sort=false)=>{
    const moved={...stops[index],offset};
    const next=stops.map((stop,itemIndex)=>itemIndex===index?moved:stop);
    if(sort){
      next.sort((a,b)=>a.offset-b.offset);
      setRequestedIndex(next.indexOf(moved));
      setDragStops(null);
      onChange({...fill,stops:next});
    }else{
      setRequestedIndex(index);
      setDragStops(next);
    }
  };
  const deleteSelected=()=>{
    if(readonly||stops.length<=2)return;
    onChange({...fill,stops:stops.filter((_,index)=>index!==selectedIndex)});
    setRequestedIndex(Math.max(0,selectedIndex-1));
  };
  if(!selected)return null;
  return <div className="gradient-stops-editor" onKeyDown={(event)=>{event.stopPropagation();const target=event.target as HTMLElement;if((event.key==='Backspace'||event.key==='Delete')&&!target.closest('input, textarea')&&stops.length>2){event.preventDefault();deleteSelected();}}} onPointerDown={(event)=>event.stopPropagation()}>
    <div className="gradient-track" style={{background}}>
      {stops.map((stop,index)=><button key={index} type="button" className={`gradient-track-stop${index===selectedIndex?' is-active':''}`} style={{left:`${stop.offset*100}%`,background:stop.color}} disabled={readonly} aria-label={`色标 ${Math.round(stop.offset*100)}%`} onClick={()=>setRequestedIndex(index)} onPointerDown={(event)=>{event.preventDefault();event.stopPropagation();setRequestedIndex(index);setDragStops((fill.stops??[]).map(item=>({...item})));event.currentTarget.setPointerCapture(event.pointerId);}} onPointerMove={(event)=>{if(event.currentTarget.hasPointerCapture(event.pointerId))moveStop(index,offsetFromPointer(event.clientX,event.currentTarget));}} onPointerUp={(event)=>{if(event.currentTarget.hasPointerCapture(event.pointerId)){moveStop(index,offsetFromPointer(event.clientX,event.currentTarget),true);event.currentTarget.releasePointerCapture(event.pointerId);}}} onPointerCancel={()=>setDragStops(null)} />)}
    </div>
    <div className="gradient-stop-editor-fields">
      <Row label="颜色"><ColorPicker disabled={readonly} value={selected.color} onChange={(color)=>replaceSelected({color:colorString(color)})} /></Row>
      <Row label="位置"><InputNumber disabled={readonly} min={0} max={100} value={Math.round(selected.offset*100)} addonAfter="%" onChange={(offset)=>replaceSelected({offset:Number(offset??0)/100})} /></Row>
      <Row label="透明度"><Slider disabled={readonly} min={0} max={100} value={Math.round(selected.opacity*100)} onChange={(opacity)=>replaceSelected({opacity:opacity/100})} /></Row>
    </div>
    <div className="gradient-stop-actions">
      <Button disabled={readonly} size="small" onClick={()=>{const next=[...stops,{offset:.5,color:'#ffffff',opacity:1}].sort((a,b)=>a.offset-b.offset);setRequestedIndex(next.findIndex(stop=>stop.offset===.5&&stop.color==='#ffffff'));onChange({...fill,stops:next});}}>添加色标</Button>
      {stops.length>2&&<Button disabled={readonly} type="text" size="small" danger icon={<CloseOutlined />} onPointerDown={(event)=>event.stopPropagation()} onClick={(event)=>{event.stopPropagation();deleteSelected();}}>删除当前色标</Button>}
    </div>
  </div>;
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return <div className="npp-field-row"><span className="npp-field-label">{label}</span><div className="npp-field-control">{children}</div></div>;
}
function VisualSection({title,children}: {title:string;children:React.ReactNode}) { const [open,setOpen]=useState(true);return <section className="visual-section"><button type="button" className="visual-section-title" onClick={()=>setOpen(value=>!value)}><span>{title}</span><span>{open?'⌄':'›'}</span></button>{open&&<div className="visual-section-content">{children}</div>}</section>; }

export function NodeVisualPanel({ value, onChange, onReset, readonly = false, terminal = false, batch = false, onCopy, onPaste }: {
  value: NodeLayout; onChange: (value: NodeLayout) => void; onReset: () => void; readonly?: boolean; terminal?: boolean; batch?:boolean; onCopy?:()=>void; onPaste?:()=>void;
}) {
  useTranslation();
  const update = (patch: Partial<NodeLayout>) => onChange({ ...value, ...patch });
  const visible = value.visible ?? {};
  const fill = value.fill;
  const border = value.border;
  return <div className="node-props-panel-body visual-props">
    {!terminal && <VisualSection title="内容显示"><div className="visibility-grid">{visibilityFields.map(([key, label]) => <Row key={key} label={label}><Switch size="small" disabled={readonly} checked={visible[key] !== false} onChange={(checked) => update({ visible: { ...visible, [key]: checked } })} /></Row>)}</div></VisualSection>}
    <VisualSection title="位置与大小">
      <div className="visual-grid">
        {!batch&&<><Row label="X"><InputNumber disabled={readonly} value={Math.round(value.x)} onChange={(x) => update({ x: Number(x ?? 0) })} /></Row>
        <Row label="Y"><InputNumber disabled={readonly} value={Math.round(value.y)} onChange={(y) => update({ y: Number(y ?? 0) })} /></Row></>}
        <Row label="宽度"><InputNumber disabled={readonly} min={NODE_MIN_WIDTH} value={value.width} placeholder="自动" onChange={(width) => update({ width: width == null ? undefined : Math.max(NODE_MIN_WIDTH, width) })} /></Row>
        <Row label="高度"><InputNumber disabled={readonly} min={NODE_MIN_HEIGHT} value={value.height} placeholder="自动" onChange={(height) => update({ height: height == null ? undefined : Math.max(NODE_MIN_HEIGHT, height) })} /></Row>
      </div>
      <Button disabled={readonly || value.height == null} size="small" onClick={() => update({ height: undefined })}>恢复自动高度</Button>
    </VisualSection>
    <VisualSection title="填充">
      <Row label="类型"><Select disabled={readonly} value={fill?.type ?? 'default'} options={[{value:'default',label:'默认'},{value:'none',label:'无填充'},{value:'solid',label:'纯色'},{value:'linear-gradient',label:'线性渐变'}]} onChange={(type) => update({ fill: type === 'default' ? undefined : { type, ...(type === 'solid' ? { color: '#ffffff', opacity: 1 } : type === 'linear-gradient' ? { angle: 90, stops: [{offset:0,color:'#ffffff',opacity:1},{offset:1,color:'#1677ff',opacity:1}] } : {}) } as NodeLayout['fill'] })} /></Row>
      {fill?.type === 'solid' && <><Row label="颜色"><VisualColorPicker disabled={readonly} value={fill.color ?? '#ffffff'} onChange={(color) => update({ fill: { ...fill, color } })} /></Row><Row label="透明度"><Slider disabled={readonly} min={0} max={100} value={Math.round((fill.opacity ?? 1)*100)} onChange={(v) => update({ fill: {...fill, opacity:v/100} })} /></Row></>}
      {fill?.type === 'linear-gradient' && <>
        <Row label="预设渐变"><Popover trigger="click" placement="bottomRight" content={<div className="gradient-preset-grid">{gradientPresets.map((preset)=><Tooltip title={preset.label} key={preset.label}><button type="button" disabled={readonly} aria-label={preset.label} onClick={()=>update({fill:{type:'linear-gradient',angle:preset.angle,stops:preset.stops.map(stop=>({...stop}))}})}><span style={{background:`linear-gradient(${preset.angle}deg, ${preset.stops.map(stop=>`${stop.color}${Math.round(stop.opacity*255).toString(16).padStart(2,'0')} ${stop.offset*100}%`).join(', ')})`}} /></button></Tooltip>)}</div>}><Button disabled={readonly} className="gradient-preset-trigger"><span style={{background:`linear-gradient(${fill.angle??90}deg, ${(fill.stops??[]).map(stop=>`${stop.color} ${stop.offset*100}%`).join(', ')})`}} /><DownOutlined /></Button></Popover></Row>
        <Row label="方向"><Popover trigger="click" placement="bottomRight" content={<div className="gradient-direction-grid">{gradientDirections.map(({angle,label})=><Tooltip title={label} key={angle}><button type="button" className={(fill.angle??90)===angle?'is-active':''} disabled={readonly} onClick={()=>update({fill:{...fill,angle}})}><span style={{background:`linear-gradient(${angle}deg, #e6f4ff, #1677ff)`}} /></button></Tooltip>)}</div>}><Button disabled={readonly} className="gradient-direction-trigger"><span style={{background:`linear-gradient(${fill.angle??90}deg, #e6f4ff, #1677ff)`}} /><DownOutlined /></Button></Popover></Row>
        <GradientStopsEditor fill={fill} readonly={readonly} onChange={(nextFill)=>update({fill:nextFill})} />
      </>}
    </VisualSection>
    <VisualSection title="边框">
      <Row label="样式"><Select disabled={readonly} value={border?.style ?? 'default'} options={[{value:'default',label:'默认'},{value:'none',label:'无边框'},{value:'solid',label:'实线'},{value:'dashed',label:'虚线'},{value:'dotted',label:'点线'}]} onChange={(style: 'default'|'none'|'solid'|'dashed'|'dotted') => update({border:style==='default'?undefined:{...border,style}})} /></Row>
      {border?.style && border.style !== 'none' && <><Row label="颜色"><VisualColorPicker disabled={readonly} value={border.color ?? '#d9d9d9'} onChange={(color)=>update({border:{...border,color}})} /></Row><Row label="粗细"><InputNumber disabled={readonly} min={0} max={12} value={border.width ?? 1.5} onChange={(width)=>update({border:{...border,width:Number(width??0)}})} /></Row></>}
      <Row label="圆角"><InputNumber disabled={readonly} min={0} max={100} value={border?.radius ?? 10} onChange={(radius)=>update({border:{...border,radius:Number(radius??0)}})} /></Row>
    </VisualSection>
    <div className="visual-actions"><Button disabled={readonly} onClick={onReset}>恢复默认样式</Button>{onCopy&&<Button onClick={onCopy}>复制样式</Button>}{onPaste&&<Button disabled={readonly} onClick={onPaste}>粘贴样式</Button>}</div>
  </div>;
}

export function EdgeVisualPanel({ value, onChange, onReset, readonly = false, onCopy, onPaste }: { value: EdgeVisual; onChange:(value:EdgeVisual)=>void; onReset:()=>void; readonly?:boolean; onCopy?:()=>void; onPaste?:()=>void }) {
  const stroke=value.stroke??{};
  return <div className="node-props-panel-body visual-props"><VisualSection title="连线">
    <Row label="颜色"><VisualColorPicker disabled={readonly} value={stroke.color??'#8c8c8c'} onChange={(color)=>onChange({...value,stroke:{...stroke,color}})} /></Row>
    <Row label="粗细"><InputNumber disabled={readonly} min={1} max={12} value={stroke.width??1.5} onChange={(width)=>onChange({...value,stroke:{...stroke,width:Number(width??1)}})} /></Row>
    <Row label="样式"><Select disabled={readonly} value={stroke.style??'solid'} options={[{value:'solid',label:'实线'},{value:'dashed',label:'虚线'},{value:'dotted',label:'点线'}]} onChange={(style)=>onChange({...value,stroke:{...stroke,style}})} /></Row>
    <Row label="线型"><Select disabled={readonly} value={value.pathType??'bezier'} options={[{value:'bezier',label:'曲线'},{value:'straight',label:'直线'},{value:'smoothstep',label:'折线'}]} onChange={(pathType)=>onChange({...value,pathType})} /></Row>
    <Row label="箭头"><Switch disabled={readonly} checked={value.showArrow!==false} onChange={(showArrow)=>onChange({...value,showArrow})} /></Row>
    <Row label="箭头大小"><InputNumber disabled={readonly} min={4} max={24} value={value.arrowSize??10} onChange={(arrowSize)=>onChange({...value,arrowSize:Number(arrowSize??10)})} /></Row>
    <Row label="条件标签"><Switch disabled={readonly} checked={value.showLabel!==false} onChange={(showLabel)=>onChange({...value,showLabel})} /></Row>
  </VisualSection><div className="visual-actions"><Button disabled={readonly} onClick={onReset}>恢复默认样式</Button>{onCopy&&<Button onClick={onCopy}>复制样式</Button>}{onPaste&&<Button disabled={readonly} onClick={onPaste}>粘贴样式</Button>}</div></div>;
}
