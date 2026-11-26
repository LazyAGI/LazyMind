import { useLayoutEffect, useState, type RefObject } from 'react';

/** Fit complete rows into the table body, whose flex layout excludes its header and footer. */
export function useWorkflowPageSize(contentRef: RefObject<HTMLDivElement>) {
  const [pageSize, setPageSize] = useState(20);

  useLayoutEffect(() => {
    const content = contentRef.current;
    if (!content) return;
    let frame = 0;
    const measure = () => {
      frame = 0;
      const body = content.querySelector<HTMLElement>('.ant-table-body');
      const rows = content.querySelectorAll<HTMLElement>('.ant-table-tbody > .ant-table-row');
      // Hidden tabs and the initial loading/empty state do not have usable row geometry.
      if (!body?.clientHeight || !rows.length) return;
      const contentBounds = content.getBoundingClientRect();
      const bodyBounds = body.getBoundingClientRect();
      if (contentBounds.height <= 0 || bodyBounds.height <= 0) return;
      const footer = content.querySelector<HTMLElement>('.ant-table-pagination');
      const footerStyle = footer ? getComputedStyle(footer) : undefined;
      const pixels = (value?: string) => Number.parseFloat(value || '') || 0;
      const footerHeight = (footer?.getBoundingClientRect().height ?? 0)
        + pixels(footerStyle?.marginTop) + pixels(footerStyle?.marginBottom);
      const table = content.querySelector<HTMLElement>('.ant-table');
      const tableBorder = table ? Math.max(0, table.offsetHeight - table.clientHeight - table.clientTop) : 0;
      // offsetHeight - clientHeight accounts for actual borders and the horizontal scrollbar.
      const bodyChrome = Math.max(0, body.offsetHeight - body.clientHeight);
      // The body can overflow its parent during layout. Its own clientHeight is not
      // the visible budget: reserve the footer even when it has been pushed outside.
      const visibleBodyHeight = Math.min(bodyBounds.height,
        contentBounds.bottom - bodyBounds.top - footerHeight - tableBorder
        - pixels(getComputedStyle(content).paddingBottom)) - bodyChrome;
      const rowHeight = Math.max(...Array.from(rows, row => row.getBoundingClientRect().height));
      if (rowHeight <= 0) return;
      const next = Math.max(1, Math.floor(visibleBodyHeight / rowHeight));
      setPageSize(previous => previous === next ? previous : next);
    };
    const schedule = () => {
      if (!frame) frame = requestAnimationFrame(measure);
    };
    const resizeObserver = new ResizeObserver(schedule);
    const observed = new Set<Element>();
    const observeLayout = () => {
      const elements = new Set<Element>([content, ...content.querySelectorAll(
        '.ant-table-body, .ant-table-header, .ant-table-pagination, .ant-table-tbody > .ant-table-row',
      )]);
      observed.forEach(element => {
        if (!elements.has(element)) { resizeObserver.unobserve(element); observed.delete(element); }
      });
      elements.forEach(element => {
        if (!observed.has(element)) { resizeObserver.observe(element); observed.add(element); }
      });
      schedule();
    };
    observeLayout();
    const mutationObserver = new MutationObserver(observeLayout);
    mutationObserver.observe(content, { childList: true, subtree: true });
    window.addEventListener('resize', schedule);
    return () => {
      cancelAnimationFrame(frame);
      resizeObserver.disconnect();
      mutationObserver.disconnect();
      window.removeEventListener('resize', schedule);
    };
  }, [contentRef]);

  return pageSize;
}
