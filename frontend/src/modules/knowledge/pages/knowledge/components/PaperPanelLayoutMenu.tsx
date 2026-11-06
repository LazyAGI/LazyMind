import {useRef,useState} from 'react';
import {Button,Dropdown} from 'antd';
import {AlignLeftOutlined,AlignRightOutlined,BlockOutlined,CheckOutlined,EllipsisOutlined,FullscreenOutlined} from '@ant-design/icons';
import {useTranslation} from 'react-i18next';
export type PaperPanelLayout='left'|'right'|'floating'|'fullscreen';
export interface PaperPanelLayoutMenuProps {value?:PaperPanelLayout;onChange:(mode:PaperPanelLayout)=>void}
/** Controlled layout selection; the reader owns positioning and persistence. */
export default function PaperPanelLayoutMenu({value='right',onChange}:PaperPanelLayoutMenuProps){
 const {i18n}=useTranslation();const zh=i18n.language?.startsWith('zh');const [open,setOpen]=useState(false);const triggerRef=useRef<HTMLButtonElement>(null);
 const options=[
  {key:'left' as const,label:zh?'停靠在左侧':'Dock left',icon:<AlignLeftOutlined aria-hidden="true"/>},
  {key:'right' as const,label:zh?'停靠在右侧':'Dock right',icon:<AlignRightOutlined aria-hidden="true"/>},
  {key:'floating' as const,label:zh?'浮动面板':'Floating panel',icon:<BlockOutlined aria-hidden="true"/>},
  {key:'fullscreen' as const,label:zh?'全屏':'Fullscreen',icon:<FullscreenOutlined aria-hidden="true"/>},
 ];
 const close=()=>{setOpen(false);triggerRef.current?.focus();};
 return <Dropdown destroyOnHidden open={open} onOpenChange={setOpen} trigger={['click']} placement="bottomRight" autoFocus menu={{
  selectable:true,selectedKeys:[value],
  items:options.map(option=>({key:option.key,icon:option.icon,label:<span aria-current={value===option.key?'true':undefined} style={{display:'inline-flex',alignItems:'center',justifyContent:'space-between',gap:24,minWidth:112}}>{option.label}{value===option.key&&<CheckOutlined aria-hidden="true"/>}</span>})),
  onClick:({key})=>{const option=options.find(item=>item.key===key);if(option)onChange(option.key);close();},
  onKeyDown:event=>{if(event.key==='Escape'){event.preventDefault();event.stopPropagation();close();}},
 }}>
  <Button ref={triggerRef} className="paper-panel-layout-trigger" type="text" icon={<EllipsisOutlined/>} aria-label={zh?'面板布局':'Panel layout'} title={zh?'面板布局':'Panel layout'} aria-haspopup="menu" aria-expanded={open} onKeyDown={event=>{
   if(['ArrowDown','Enter',' '].includes(event.key)){event.preventDefault();setOpen(true);}
   if(event.key==='Escape')close();
  }}/>
 </Dropdown>;
}
