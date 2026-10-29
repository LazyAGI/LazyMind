import i18n from "@/i18n";
import { act, fireEvent, render, screen } from '@testing-library/react';
import { beforeAll, afterEach, describe, expect, it, vi } from 'vitest';
import { PdfFormulaLayer, formulaAtPoint } from './PdfFormulaLayer';
beforeAll(async()=>{await i18n.changeLanguage("zh-CN");});
const formula = { id: 'f1', type: 'formula', page: 0, bbox: [0.1, 0.2, 0.5, 0.3] as [number, number, number, number], latex: 'x^2' };
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });
describe('PDF formula interaction', () => {
 it('uses visible page normalized coordinates at every zoom without rotating twice', () => {
  expect(formulaAtPoint([formula], 150, 250, {left: 100, top: 50, width: 200, height: 800})?.id).toBe('f1');
  expect(formulaAtPoint([formula], 200, 450, {left: 100, top: 50, width: 400, height: 1600})?.id).toBe('f1');
  expect(formulaAtPoint([{...formula, bbox: [-1,0,1,1]}], 150, 250, {left:100,top:50,width:200,height:800})).toBeUndefined();
 });
 it('supports keyboard/click, escape, and reports clipboard failure with raw latex available', async () => {
  Object.defineProperty(navigator, 'clipboard', { configurable:true, value: {writeText: vi.fn().mockRejectedValue(new Error('denied'))} });
  render(<div><PdfFormulaLayer blocks={[formula]} /></div>);
  fireEvent.click(screen.getByRole('button', {name: '查看公式 1'}));
  expect(screen.getByRole('textbox', {name:'原始 LaTeX'})).toHaveValue('x^2');
  await act(async () => fireEvent.click(screen.getByRole('button', {name: '复制 LaTeX'})));
  expect(screen.getByRole('status')).toHaveTextContent('复制失败');
  fireEvent.keyDown(document, {key: 'Escape'});
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
 });
 it('delays hover and cancels on native text selection', () => {
  vi.useFakeTimers();
  const view=render(<div data-testid="page"><PdfFormulaLayer blocks={[formula]} /></div>);
  const page=screen.getByTestId('page');
  vi.spyOn(page, 'getBoundingClientRect').mockReturnValue({left:0,top:0,width:100,height:100} as DOMRect);
  fireEvent.pointerMove(page, {clientX:20, clientY:25, buttons:0});
  expect(screen.queryByRole('dialog')).toBeNull();
  act(() => vi.advanceTimersByTime(350));
  expect(screen.getByRole('dialog')).toBeInTheDocument();
  fireEvent.pointerDown(page);
  expect(screen.queryByRole('dialog')).toBeNull();
  view.unmount();
 });
});

describe('formula hover grace',()=>{
 it('allows entering the card and cancels pending hover for selection or unmount',()=>{
  vi.useFakeTimers();
  const view=render(<div data-testid="page"><PdfFormulaLayer blocks={[formula]} /></div>);
  const page=screen.getByTestId('page');
  vi.spyOn(page,'getBoundingClientRect').mockReturnValue({left:0,top:0,width:100,height:100} as DOMRect);
  fireEvent.pointerMove(page,{clientX:20,clientY:25,buttons:0});
  fireEvent.pointerDown(page);
  act(()=>vi.advanceTimersByTime(350));
  expect(screen.queryByRole('dialog')).toBeNull();
  fireEvent.pointerMove(page,{clientX:20,clientY:25,buttons:0});
  act(()=>vi.advanceTimersByTime(350));
  fireEvent.pointerLeave(page);
  fireEvent.pointerEnter(screen.getByRole('dialog'));
  act(()=>vi.advanceTimersByTime(300));
  expect(screen.getByRole('dialog')).toBeInTheDocument();
  fireEvent.pointerLeave(screen.getByRole('dialog'));
  act(()=>vi.advanceTimersByTime(300));
  expect(screen.queryByRole('dialog')).toBeNull();
  fireEvent.pointerMove(page,{clientX:20,clientY:25,buttons:0});
  view.unmount();
  act(()=>vi.advanceTimersByTime(350));
  expect(screen.queryByRole('dialog')).toBeNull();
 });
 it('keeps malformed LaTeX available when preview fails',()=>{
  render(<div><PdfFormulaLayer blocks={[{...formula,latex:'\\invalidcommand'}]} /></div>);
  fireEvent.click(screen.getByRole('button',{name:'查看公式 1'}));
  expect(screen.getByText('此公式无法预览，仍可复制原始 LaTeX。')).toBeInTheDocument();
  expect(screen.getByRole('textbox')).toHaveValue('\\invalidcommand');
 });
});
