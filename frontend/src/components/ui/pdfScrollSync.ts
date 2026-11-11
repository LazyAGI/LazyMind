export interface PdfScrollSyncEndpoint {
  connect: (element: HTMLElement) => () => void;
  onScroll: () => void;
}

// Share scroll geometry directly: a wheel tick must not rerender the workspace.
export function createPdfScrollSync() {
  let enabled = false;
  const elements: Array<HTMLElement | undefined> = [];
  const expected: Array<number | undefined> = [];
  const align = (from: number) => {
    const source = elements[from], target = elements[1 - from];
    if (!enabled || !source || !target || !source.clientHeight || !target.clientHeight) return;
    const extent = source.scrollHeight - source.clientHeight;
    const fraction = extent > 0 ? Math.min(1, Math.max(0, source.scrollTop / extent)) : 0;
    const top = fraction * Math.max(0, target.scrollHeight - target.clientHeight);
    if (Math.abs(target.scrollTop - top) < .5) return;
    target.scrollTop = top;
    expected[1 - from] = target.scrollTop;
  };
  const endpoint = (index: number): PdfScrollSyncEndpoint => ({
    connect: element => {
      elements[index] = element;
      expected[index] = undefined;
      align(0);
      return () => { if (elements[index] === element) { elements[index] = undefined; expected[index] = undefined; } };
    },
    onScroll: () => {
      const element = elements[index];
      if (!element) return;
      const echo = expected[index];
      expected[index] = undefined;
      if (echo !== undefined && Math.abs(element.scrollTop - echo) < .5) return;
      align(index);
    },
  });
  return {
    source: endpoint(0), translation: endpoint(1),
    setEnabled: (value: boolean) => { enabled = value; expected.length = 0; if (enabled) align(0); },
  };
}
