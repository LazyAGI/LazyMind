import {act,cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react';
import {afterEach,expect,it,vi} from 'vitest';
import PaperPanelLayoutMenu from './PaperPanelLayoutMenu';
const language=vi.hoisted(()=>({value:'zh-CN'}));
vi.mock('react-i18next',()=>({useTranslation:()=>({i18n:{language:language.value}})}));
afterEach(()=>{cleanup();language.value='zh-CN';});
it('offers four real layouts and marks the current right dock',async()=>{
 const onChange=vi.fn();render(<PaperPanelLayoutMenu onChange={onChange}/>);
 fireEvent.click(screen.getByRole('button',{name:'面板布局'}));
 for(const label of ['停靠在左侧','停靠在右侧','浮动面板','全屏'])expect(await screen.findByRole('menuitem',{name:label})).toBeInTheDocument();
 expect(screen.getByText('停靠在右侧')).toHaveAttribute('aria-current','true');
 fireEvent.click(screen.getByRole('menuitem',{name:'浮动面板'}));expect(onChange).toHaveBeenCalledWith('floating');
});
it.each([['停靠在左侧','left'],['停靠在右侧','right'],['全屏','fullscreen']] as const)('selects %s',async(label,mode)=>{
 const onChange=vi.fn();render(<PaperPanelLayoutMenu value="floating" onChange={onChange}/>);fireEvent.click(screen.getByRole('button',{name:'面板布局'}));fireEvent.click(await screen.findByRole('menuitem',{name:label}));expect(onChange).toHaveBeenCalledOnce();expect(onChange).toHaveBeenCalledWith(mode);
});
it('opens by arrow key and restores trigger focus on Escape',async()=>{
 render(<PaperPanelLayoutMenu value="left" onChange={vi.fn()}/>);const trigger=screen.getByRole('button',{name:'面板布局'});trigger.focus();fireEvent.keyDown(trigger,{key:'ArrowDown'});
 const menu=await screen.findByRole('menu');fireEvent.keyDown(menu,{key:'Escape'});await waitFor(()=>expect(trigger).toHaveFocus());expect(trigger).toHaveAttribute('aria-expanded','false');
});
it('uses English labels when selected',async()=>{
 language.value='en-US';render(<PaperPanelLayoutMenu value="fullscreen" onChange={vi.fn()}/>);fireEvent.click(screen.getByRole('button',{name:'Panel layout'}));expect(await screen.findByRole('menuitem',{name:'Dock left'})).toBeInTheDocument();expect(screen.getByText('Fullscreen')).toHaveAttribute('aria-current','true');
});
it('lets the Ant menu move focus between layouts with arrow keys',async()=>{
 const geometry=vi.spyOn(HTMLElement.prototype,'offsetParent','get').mockImplementation(function(){return document.body;});
 render(<PaperPanelLayoutMenu onChange={vi.fn()}/>);fireEvent.click(screen.getByRole('button',{name:'面板布局'}));
 const left=await screen.findByRole('menuitem',{name:'停靠在左侧'});act(()=>left.focus());fireEvent.keyDown(left,{key:'ArrowDown',keyCode:40,which:40});
 await waitFor(()=>expect(screen.getByRole('menuitem',{name:'停靠在右侧'})).toHaveFocus());geometry.mockRestore();
});
