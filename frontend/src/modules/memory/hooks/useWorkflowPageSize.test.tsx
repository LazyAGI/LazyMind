import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useWorkflowPageSize } from './useWorkflowPageSize';

let resize: () => void;
let disconnect: ReturnType<typeof vi.fn>;
beforeEach(() => {
  disconnect = vi.fn();
  vi.stubGlobal('ResizeObserver', class {
    constructor(callback: () => void) { resize = callback; }
    observe() {}
    unobserve() {}
    disconnect = disconnect;
  });
});
afterEach(() => { vi.unstubAllGlobals(); });

function mountTable(height = 300, rowHeight = 60) {
  const content = document.createElement('div');
  let bodyHeight: number | undefined;
  let footerHeight = 50;
  let scrollbar = 0;
  content.innerHTML = '<div class="ant-table-body"><table><tbody class="ant-table-tbody"><tr class="ant-table-row"><td>Workflow</td></tr></tbody></table></div><div class="ant-table-pagination"></div>';
  const body = content.querySelector<HTMLElement>('.ant-table-body')!;
  const row = content.querySelector<HTMLElement>('.ant-table-row')!;
  const footer = content.querySelector<HTMLElement>('.ant-table-pagination')!;
  Object.defineProperty(body, 'clientHeight', { get: () => (bodyHeight ?? height) - scrollbar });
  Object.defineProperty(body, 'offsetHeight', { get: () => bodyHeight ?? height });
  vi.spyOn(content, 'getBoundingClientRect').mockImplementation(() => ({ top: 0, bottom: height ? height + 100 : 0, height: height ? height + 100 : 0 }) as DOMRect);
  vi.spyOn(body, 'getBoundingClientRect').mockImplementation(() => ({ top: 50, height: bodyHeight ?? height }) as DOMRect);
  vi.spyOn(footer, 'getBoundingClientRect').mockImplementation(() => ({ height: footerHeight }) as DOMRect);
  vi.spyOn(row, 'getBoundingClientRect').mockImplementation(() => ({ height: rowHeight }) as DOMRect);
  const ref = { current: content };
  const hook = renderHook(() => useWorkflowPageSize(ref));
  return { ...hook, content, setBodyHeight(value: number) { bodyHeight = value; }, setFooterHeight(value: number) { footerHeight = value; }, setScrollbar(value: number) { scrollbar = value; }, setHeight(value: number) { height = value; }, setRowHeight(value: number) { rowHeight = value; } };
}

describe('workflow automatic page size', () => {
  it('fits complete rows and updates in both resize directions', async () => {
    const table = mountTable(359);
    await waitFor(() => expect(table.result.current).toBe(5));
    act(() => { table.setHeight(600); resize(); });
    await waitFor(() => expect(table.result.current).toBe(10));
    act(() => { table.setHeight(180); resize(); });
    await waitFor(() => expect(table.result.current).toBe(3));
  });

  it('reserves pagination space even when the body and footer overflow the container', async () => {
    const table = mountTable(300);
    await waitFor(() => expect(table.result.current).toBe(5));
    act(() => { table.setBodyHeight(600); resize(); });
    await new Promise(resolve => requestAnimationFrame(resolve));
    expect(table.result.current).toBe(5);
    act(() => { table.setFooterHeight(82); resize(); });
    await waitFor(() => expect(table.result.current).toBe(4));
  });

  it('does not count a row clipped by a horizontal scrollbar or fractional pixels', async () => {
    const table = mountTable(300);
    await waitFor(() => expect(table.result.current).toBe(5));
    act(() => { table.setScrollbar(15); resize(); });
    await waitFor(() => expect(table.result.current).toBe(4));
    act(() => { table.setScrollbar(0); table.setHeight(299.8); resize(); });
    await new Promise(resolve => requestAnimationFrame(resolve));
    expect(table.result.current).toBe(4);
  });

  it('accounts for row height changes from font scaling or narrower columns', async () => {
    const table = mountTable(600);
    await waitFor(() => expect(table.result.current).toBe(10));
    act(() => { table.setRowHeight(75); resize(); });
    await waitFor(() => expect(table.result.current).toBe(8));
  });

  it('keeps the last capacity while hidden and shows at least one row in tiny windows', async () => {
    const table = mountTable();
    await waitFor(() => expect(table.result.current).toBe(5));
    act(() => { table.setHeight(0); resize(); });
    await new Promise(resolve => requestAnimationFrame(resolve));
    expect(table.result.current).toBe(5);
    act(() => { table.setHeight(30); resize(); });
    await waitFor(() => expect(table.result.current).toBe(1));
  });

  it('recalculates when rows arrive after an empty result, without treating a short last page as capacity', async () => {
    const table = mountTable(600);
    await waitFor(() => expect(table.result.current).toBe(10));
    act(() => { table.content.querySelector('tr')!.remove(); });
    await new Promise(resolve => requestAnimationFrame(resolve));
    expect(table.result.current).toBe(10);
    const row = document.createElement('tr');
    row.className = 'ant-table-row';
    vi.spyOn(row, 'getBoundingClientRect').mockReturnValue({ height: 100 } as DOMRect);
    act(() => { table.content.querySelector('tbody')!.appendChild(row); });
    await waitFor(() => expect(table.result.current).toBe(6));
    table.unmount();
    expect(disconnect).toHaveBeenCalledOnce();
  });
});
