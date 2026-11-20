import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useEffect, type ReactNode } from 'react';
import type { PdfIRDocument } from '@/modules/knowledge/api/pdfIR';

const pdf = vi.hoisted(() => ({
  pageLoads: new Map<number, (width: number, height: number) => void>(),
  nativeClick: undefined as undefined | ((item: { pageNumber: number }) => void),
  document: undefined as PdfIRDocument | undefined,
}));

vi.mock('react-i18next', async original => ({
  ...await original<typeof import('react-i18next')>(),
  useTranslation: () => ({ t: (key: string) => key }),
}));
vi.mock('react-pdf', () => ({
  pdfjs: { GlobalWorkerOptions: {} },
  Document: ({ children, file, onLoadSuccess, onItemClick }: {
    children: ReactNode;
    file: unknown;
    onLoadSuccess: (document: unknown) => void;
    onItemClick?: typeof pdf.nativeClick;
  }) => {
    pdf.nativeClick = onItemClick;
    useEffect(() => {
      onLoadSuccess({
        numPages: 3,
        getPage: async () => ({
          view: [0, 0, 600, 800],
          getViewport: () => ({ width: 600, height: 800, convertToViewportPoint: (x: number, y: number) => [x, 800 - y] }),
        }),
      });
    }, [file]);
    return <>{children}</>;
  },
  Page: ({ pageNumber, onLoadSuccess }: {
    pageNumber: number;
    onLoadSuccess: (page: unknown) => void;
  }) => {
    // Keep PDF.js page measurements deferred until the test releases them.
    pdf.pageLoads.set(pageNumber, (width, height) => onLoadSuccess({
      getViewport: () => ({ width, height, convertToViewportPoint: (x: number, y: number) => [x, height - y] }),
    }));
    return <div />;
  },
}));
vi.mock('@/modules/knowledge/api/pdfIR', async original => ({
  ...await original<typeof import('@/modules/knowledge/api/pdfIR')>(),
  usePdfIR: () => ({ state: { status: 'ready', document: pdf.document }, starting: false, start: vi.fn() }),
}));
vi.mock('./pdfPinStore', async original => ({
  ...await original<typeof import('./pdfPinStore')>(),
  readPdfPinBoard: vi.fn().mockResolvedValue([]),
  writePdfPinBoard: vi.fn().mockResolvedValue(true),
}));

import RenderPdf from './RenderPdf';

const readerProps = {
  fileData: 'navigation-races.pdf',
  defaultPageWidth: 600,
  pdfIRTarget: { dataset: 'dataset', document: 'document' },
  pdfSourceHash: 'hash',
};
const studyRequest = { id: 'delayed-formula', requestId: 1 };

beforeEach(() => {
  vi.useFakeTimers();
  sessionStorage.clear();
  pdf.pageLoads.clear();
  pdf.nativeClick = undefined;
  pdf.document = {
    version: 1,
    projection_version: 2,
    source_sha256: 'hash',
    parser: { name: 'mineru', model: 'vlm' },
    coordinate_space: 'normalized_top_left',
    pages: [{ index: 0 }, { index: 1 }, { index: 2 }],
    blocks: [{ id: 'delayed-formula', type: 'formula', page: 1, latex: 'x', bbox: [.1, .1, .3, .2] }],
  };
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(800);
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(600);
  // jsdom has no scrolling layout; scrollTo must synchronously change the
  // viewport offset, while scroll events remain explicitly driven by tests.
  vi.spyOn(HTMLElement.prototype, 'scrollTo').mockImplementation(function (this: HTMLElement, options?: ScrollToOptions | number, y?: number) {
    if (typeof options === 'number') { this.scrollLeft = options; this.scrollTop = y ?? 0; }
    else {
      if (options?.top !== undefined) this.scrollTop = options.top;
      if (options?.left !== undefined) this.scrollLeft = options.left;
    }
  });
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

async function mountReader() {
  const view = render(<RenderPdf {...readerProps} />);
  await act(async () => {});
  const viewport = view.container.querySelector('.pdf-reader-with-study')!.firstElementChild as HTMLElement;
  return { ...view, viewport };
}

async function advanceStudyNavigation() {
  await act(async () => { await vi.advanceTimersByTimeAsync(100); });
}

async function startPendingStudyNavigation() {
  const view = await mountReader();
  view.rerender(<RenderPdf {...readerProps} pdfNavigateTo={studyRequest} />);
  await advanceStudyNavigation();
  // Page two begins at 820px; its 10% evidence anchor sits 64px below the top.
  expect(view.viewport.scrollTop).toBe(836);
  fireEvent.scroll(view.viewport);
  return view;
}

describe('PDF navigation races', () => {
  it('accepts a fresh navigation to the coordinates of an ignored acknowledgement', async () => {
    const report = vi.fn();
    const props = { ...readerProps, onViewPositionChange: report };
    const view = render(<RenderPdf {...props} viewPosition={{ page: 1, progress: 0 }} />);
    await act(async () => {});
    const viewport = view.container.querySelector('.pdf-reader-with-study')!.firstElementChild as HTMLElement;
    viewport.scrollTop = 1000;
    fireEvent.scroll(viewport);
    const reported = report.mock.calls.at(-1)![0];
    viewport.scrollTop = 1100;
    fireEvent.scroll(viewport);
    view.rerender(<RenderPdf {...props} viewPosition={reported} />);
    expect(viewport.scrollTop).toBe(1100);
    view.rerender(<RenderPdf {...props} viewPosition={{ ...reported }} />);
    expect(viewport.scrollTop).toBe(1000);
  });

  it('keeps user scrolling after the study target finishes loading', async () => {
    const { viewport } = await startPendingStudyNavigation();
    viewport.scrollTop = 1100;
    fireEvent.scroll(viewport);
    // Matching the estimate rules out a legitimate geometry correction.
    await act(async () => { pdf.pageLoads.get(2)!(600, 800); });
    await advanceStudyNavigation();
    expect(viewport.scrollTop).toBe(1100);
  });

  it('cancels a pending study jump on wheel intent before the viewport moves', async () => {
    const view = await mountReader();
    view.rerender(<RenderPdf {...readerProps} pdfNavigateTo={studyRequest} />);
    fireEvent.wheel(view.viewport, { deltaY: 80 });
    await advanceStudyNavigation();
    expect(view.viewport.scrollTop).toBe(0);
  });

  it('does not apply a pending study jump to a replacement PDF', async () => {
    const view = await mountReader();
    view.rerender(<RenderPdf {...readerProps} pdfNavigateTo={studyRequest} />);
    view.rerender(<RenderPdf {...readerProps} fileData="replacement.pdf" />);
    await act(async () => {});
    await advanceStudyNavigation();
    expect(view.viewport.scrollTop).toBe(0);
  });

  it.each([
    ['page request', 1640],
    ['position request', 2040],
    ['native link', 1640],
    ['toolbar page', 1640],
  ] as const)('lets a newer %s supersede the pending study jump', async (navigation, expectedTop) => {
    const view = await startPendingStudyNavigation();
    if (navigation === 'page request') {
      view.rerender(<RenderPdf {...readerProps} pdfNavigateTo={studyRequest} pdfNavigateToPage={{ page: 3, requestId: 2 }} />);
    } else if (navigation === 'position request') {
      view.rerender(<RenderPdf {...readerProps} pdfNavigateTo={studyRequest} pdfNavigateToPosition={{ page: 3, progress: .5, requestId: 2 }} />);
    } else if (navigation === 'native link') {
      await act(async () => { pdf.nativeClick!({ pageNumber: 3 }); });
    } else {
      fireEvent.click(screen.getByRole('button', { name: 'pdfReader.nextPage' }));
    }
    expect(view.viewport.scrollTop).toBe(expectedTop);
    // A new command must win immediately, even before the browser delivers
    // the scroll event caused by that command.
    await act(async () => { pdf.pageLoads.get(2)!(600, 800); });
    await advanceStudyNavigation();
    expect(view.viewport.scrollTop).toBe(expectedTop);
  });

  it('still corrects the study anchor when only its own scroll event precedes the page measurement', async () => {
    const { viewport } = await startPendingStudyNavigation();
    await act(async () => { pdf.pageLoads.get(2)!(600, 1600); });
    await advanceStudyNavigation();
    expect(viewport.scrollTop).toBe(916);
  });
});
