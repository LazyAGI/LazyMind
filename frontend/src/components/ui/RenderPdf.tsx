import { useEffect, useMemo, useRef, useState } from "react";
import { Document, Page, pdfjs } from "react-pdf";
import "react-pdf/dist/Page/AnnotationLayer.css";
import "react-pdf/dist/Page/TextLayer.css";

pdfjs.GlobalWorkerOptions.workerSrc = `https://unpkg.com/pdfjs-dist@${pdfjs.version}/build/pdf.worker.min.mjs`;

interface RenderPdfProps {
  fileData: string | ArrayBuffer | File | null;
  metadata?: Record<string, unknown> | null;
  content?: string | null;
  defaultPageWidth?: number;
  renderMode?: "canvas" | "custom" | "none";
  loadingText?: string;
  className?: string;
  style?: React.CSSProperties;
  gapBackground?: string;
}

const GAP = 20;
const BUFFER_PAGES = 3;
const GAP_BACKGROUND = "#DFE6EF";
const PAGE_BACKGROUND = "#ffffff";
const WIDTH_PT = 595;
const HEIGHT_PT = 842;

export default function RenderPdf({
  fileData,
  metadata,
  defaultPageWidth,
  renderMode = "canvas",
  loadingText = "正在加载PDF...",
  className,
  gapBackground = GAP_BACKGROUND,
  style,
}: RenderPdfProps) {
  const [numPages, setNumPages] = useState(1);
  const [pdfLoaded, setPdfLoaded] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [pageSizesMap, setPageSizesMap] = useState<
    Record<number, { width: number; height: number }>
  >({});
  const pageRefs = useRef<(HTMLDivElement | null)[]>([]);
  const containerRef = useRef<HTMLDivElement | null>(null);

  const [viewportHeight, setViewportHeight] = useState(0);
  const [containerWidth, setContainerWidth] = useState(0);
  const [scrollTop, setScrollTop] = useState(0);

const pageWidthPx = useMemo(() => {
  if (defaultPageWidth) {
    return defaultPageWidth;
  }
  if (!containerWidth) {
    return 900;
  }
  return Math.floor(containerWidth * 0.94);
}, [defaultPageWidth, containerWidth]);
  const [pageSlotHeight, setPageSlotHeight] = useState(0);

  const [pendingHighlight, setPendingHighlight] = useState(false);

  const originBbox = useMemo(
    () => (metadata?.bbox as number[]) || [],
    [metadata?.bbox],
  );
  const pageIndex = useMemo(
    () => (metadata?.page as number) || 0,
    [metadata?.page],
  );

  useEffect(() => {
    if (originBbox.length === 4) {
      setPendingHighlight(true);
    }
  }, [originBbox, pageIndex]);

  useEffect(() => {
    const updateViewport = () => {
      if (!containerRef.current) {
        return;
      }
      setViewportHeight(containerRef.current.clientHeight);
      setContainerWidth(containerRef.current.clientWidth);
    };
    updateViewport();
    window.addEventListener("resize", updateViewport);
    return () => window.removeEventListener("resize", updateViewport);
  }, []);

  const onScroll = () => {
    if (!containerRef.current) {
      return;
    }
    setScrollTop(containerRef.current.scrollTop);
  };

  const pageHeightsPx = useMemo(() => {
    const arr = [];
    for (let i = 0; i < numPages; i++) {
      const { width, height } = pageSizesMap[i] || {
        width: WIDTH_PT,
        height: HEIGHT_PT,
      };
      const pageHeightPx = (pageWidthPx * height) / width;
      arr.push(pageHeightPx);
    }
    return arr;
  }, [pageSizesMap, pageWidthPx, numPages]);

  const pageTops = useMemo(() => {
    const arr = [];
    let cumulativeTop = 0;
    for (let i = 0; i < numPages; i++) {
      arr.push(cumulativeTop);
      cumulativeTop += pageHeightsPx[i] + GAP;
    }
    return arr;
  }, [pageHeightsPx, numPages]);

  useEffect(() => {
    const fallback = pageHeightsPx[0]
      ? pageHeightsPx[0] + GAP
      : Math.ceil((pageWidthPx * HEIGHT_PT) / WIDTH_PT) + GAP;
    setPageSlotHeight(fallback);
  }, [pageHeightsPx, pageWidthPx]);

  const visibleRange = useMemo(() => {
    if (!pageTops.length || !viewportHeight) {
      return { start: 0, end: 0 };
    }
    let start = 0;
    for (let i = 0; i < pageTops.length; i++) {
      const top = pageTops[i];
      const bottom = top + pageHeightsPx[i];
      if (bottom >= scrollTop) {
        start = Math.max(0, i - BUFFER_PAGES);
        break;
      }
    }
    let end = numPages - 1;
    const viewBottom = scrollTop + viewportHeight;
    for (let i = pageTops.length - 1; i >= 0; i--) {
      if (pageTops[i] <= viewBottom) {
        end = Math.min(numPages - 1, i + BUFFER_PAGES);
        break;
      }
    }
    return { start, end };
  }, [scrollTop, viewportHeight, pageTops, pageHeightsPx, numPages]);

  const visiblePages = useMemo(() => {
    const set = new Set<number>();
    if (pageSlotHeight && viewportHeight) {
      for (let i = visibleRange.start; i <= visibleRange.end; i++) {
        set.add(i);
      }
    }
    if (pageIndex >= 0 && pageIndex < numPages) {
      set.add(pageIndex);
    }
    return Array.from(set).sort((a, b) => a - b);
  }, [visibleRange, pageIndex, numPages, pageSlotHeight, viewportHeight]);

  const clearAllHighlights = () => {
    containerRef.current
      ?.querySelectorAll(".pdf-text-highlight-position-box")
      .forEach((el) => el.remove());
  };

  const highlightPositionTextFn = (
    dom: HTMLElement,
    bbox: number[],
    renderedW: number,
    renderedH: number,
    pdfW: number,
    pdfH: number,
  ) => {
    clearAllHighlights();
    if (!bbox || bbox.length !== 4) {
      return;
    }
    const [x0, y0, x1, y1] = bbox;
    const scaleX = renderedW / pdfW;
    const scaleY = renderedH / pdfH;
    const left = Math.ceil(x0 * scaleX);
    const top = Math.ceil(y0 * scaleY);
    const width = Math.ceil((x1 - x0) * scaleX);
    const height = Math.ceil((y1 - y0) * scaleY);
    const div = document.createElement("div");
    div.className = "pdf-text-highlight-position-box";
    div.style.position = "absolute";
    div.style.left = `${left}px`;
    div.style.top = `${top}px`;
    div.style.width = `${width}px`;
    div.style.height = `${height}px`;
    div.style.backgroundColor = "rgba(255, 255, 0, 0.4)";
    div.style.border = "2px solid rgba(255, 255, 0, 0.8)";
    div.style.pointerEvents = "none";
    div.style.zIndex = "10";
    dom.appendChild(div);
    div.scrollIntoView({
      behavior: "smooth",
      block: "center",
      inline: "nearest",
    });
  };

  useEffect(() => {
    if (!pdfLoaded || originBbox.length !== 4 || pageIndex < 0) {
      return;
    }
    if (!containerRef.current || !pageSlotHeight || !pendingHighlight) {
      return;
    }
    const targetScrollTop = pageTops[pageIndex] ?? pageIndex * pageSlotHeight;
    containerRef.current.scrollTo({ top: targetScrollTop, behavior: "auto" });
  }, [
    pendingHighlight,
    pdfLoaded,
    originBbox,
    pageIndex,
    pageSlotHeight,
    pageTops,
  ]);

  useEffect(() => {
    if (
      !pendingHighlight ||
      !pdfLoaded ||
      !originBbox.length ||
      pageIndex < 0
    ) {
      return;
    }
    if (!containerRef.current) {
      return;
    }
    const target = pageTops[pageIndex] ?? pageIndex * pageSlotHeight;
    containerRef.current.scrollTo({ top: target, behavior: "smooth" });

    let cancelled = false;
    const start = Date.now();
    const MAX_WAIT_MS = 3000;
    const tryDrawLoop = () => {
      if (cancelled) {
        return;
      }
      const ref = pageRefs.current[pageIndex];
      const size = pageSizesMap[pageIndex];
      if (ref && size) {
        const renderedH = Math.max(
          1,
          Math.round(
            pageHeightsPx[pageIndex] ||
              (pageWidthPx * (size.height || HEIGHT_PT)) /
                (size.width || WIDTH_PT),
          ),
        );
        highlightPositionTextFn(
          ref,
          originBbox,
          pageWidthPx,
          renderedH,
          size.width,
          size.height,
        );
        setPendingHighlight(false);
        return;
      }
      if (Date.now() - start < MAX_WAIT_MS) {
        requestAnimationFrame(tryDrawLoop);
      }
    };
    const raf = requestAnimationFrame(tryDrawLoop);
    return () => {
      cancelled = true;
      cancelAnimationFrame(raf);
    };
  }, [
    pendingHighlight,
    pdfLoaded,
    originBbox,
    pageIndex,
    pageTops,
    pageSlotHeight,
    pageSizesMap,
    pageHeightsPx,
    pageWidthPx,
  ]);

  const tryHighlightAfterRender = (index: number) => {
    if (!pendingHighlight || index !== pageIndex) {
      return;
    }
    const ref = pageRefs.current[index];
    const size = pageSizesMap[index];
    if (ref && size) {
      const renderedH = Math.max(
        1,
        Math.round(
          pageHeightsPx[index] ||
            (pageWidthPx * (size.height || HEIGHT_PT)) /
              (size.width || WIDTH_PT),
        ),
      );
      highlightPositionTextFn(
        ref,
        originBbox,
        pageWidthPx,
        renderedH,
        size.width,
        size.height,
      );
      setPendingHighlight(false);
    }
  };

  useEffect(() => {
    if (!pendingHighlight || !pdfLoaded) {
      return;
    }
    const size = pageSizesMap[pageIndex];
    const ref = pageRefs.current[pageIndex];
    if (ref && size) {
      const renderedH = Math.max(
        1,
        Math.round(
          pageHeightsPx[pageIndex] ||
            (pageWidthPx * (size.height || HEIGHT_PT)) /
              (size.width || WIDTH_PT),
        ),
      );
      highlightPositionTextFn(
        ref,
        originBbox,
        pageWidthPx,
        renderedH,
        size.width,
        size.height,
      );
      setPendingHighlight(false);
    }
  }, [
    pendingHighlight,
    pdfLoaded,
    pageIndex,
    originBbox,
    pageSizesMap,
    pageHeightsPx,
    pageWidthPx,
  ]);

  const renderNoDataFn = () => {
    return (
      <div
        style={{
          display: "flex",
          justifyContent: "center",
          alignItems: "center",
          height: "90vh",
          color: "#666",
          fontSize: "20px",
        }}
      >
        {loading ? loadingText : "没有可显示的PDF文件"}
      </div>
    );
  };

  const renderVirtualPage = (index: number) => {
    const { width, height } = pageSizesMap[index] || {
      width: WIDTH_PT,
      height: HEIGHT_PT,
    };
    const pageHeightPx =
      pageHeightsPx[index] || Math.ceil((pageWidthPx * height) / width);
    const top = pageTops[index] ?? index * pageSlotHeight;

    return (
      <div
        key={`page_${index + 1}`}
        ref={(el) => (pageRefs.current[index] = el)}
        style={{
          position: "absolute",
          top,
          left: "50%",
          transform: "translateX(-50%)",
          width: pageWidthPx,
          height: pageHeightPx,
          background: PAGE_BACKGROUND,
          boxShadow: "0 2px 6px rgba(0,0,0,0.08)",
          overflow: "hidden",
        }}
      >
        <Page
          pageNumber={index + 1}
          width={pageWidthPx}
          renderTextLayer={false}
          renderAnnotationLayer={false}
          onRenderSuccess={() => {
            tryHighlightAfterRender(index);
          }}
          onLoadSuccess={(page) => {
            const viewport = page.getViewport({ scale: 1 });
            setPageSizesMap((prev) => ({
              ...prev,
              [index]: {
                width: viewport.width,
                height: viewport.height,
              },
            }));
          }}
        />
        <div
          style={{
            position: "absolute",
            bottom: 6,
            right: 10,
            padding: "2px 8px",
            fontSize: 12,
            background: "rgba(0,0,0,0.45)",
            color: "#fff",
            borderRadius: 12,
            lineHeight: 1.2,
            pointerEvents: "none",
            fontFamily: "system-ui, sans-serif",
          }}
        >
          {index + 1}/{numPages}
        </div>
      </div>
    );
  };

  if (error) {
    return (
      <div style={{ padding: 20, textAlign: "center", color: "red" }}>
        <p>加载失败: {error}</p>
      </div>
    );
  }

  const totalHeight =
    (pageHeightsPx.reduce((s, h) => s + h, 0) || pageSlotHeight * numPages) +
    GAP * Math.max(0, numPages - 1);

  return (
    <div
      className={className}
      ref={containerRef}
      onScroll={onScroll}
      style={{
        overflow: "auto",
        height: "calc(100vh - 220px)",
        position: "relative",
        backgroundColor: gapBackground,
        ...style,
      }}
    >
      <Document
        file={fileData}
        renderMode={renderMode}
        loading={renderNoDataFn()}
        noData={renderNoDataFn()}
        onLoadSuccess={(doc) => {
          setNumPages(doc.numPages);
          doc.getPage(1).then((firstPage) => {
            const view = (
              firstPage as unknown as {
                view?: [number, number, number, number];
              }
            ).view;
            setPageSizesMap((prev) => ({
              ...prev,
              0: {
                width: Math.ceil(view ? view[2] : WIDTH_PT),
                height: Math.ceil(view ? view[3] : HEIGHT_PT),
              },
            }));
          });
          setPdfLoaded(true);
          setLoading(false);
          setError(null);
        }}
        onLoadError={(err) => {
          setError(err.message || "加载失败");
          setLoading(false);
        }}
      >
        <div
          style={{
            position: "relative",
            height: totalHeight,
            minWidth: pageWidthPx + 32,
            background: gapBackground,
          }}
        >
          {visiblePages.map((i) => renderVirtualPage(i))}
        </div>
      </Document>
    </div>
  );
}
