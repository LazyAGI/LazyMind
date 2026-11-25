type ExcelPreview = typeof import('@js-preview/excel').default;
let pending: Promise<ExcelPreview> | undefined;

export function loadExcelPreview(): Promise<ExcelPreview> {
  const previewWindow = window as typeof window & { jsPreviewExcel?: ExcelPreview };
  if (previewWindow.jsPreviewExcel) return Promise.resolve(previewWindow.jsPreviewExcel);
  if (pending) return pending;
  // Keep the existing standalone UMD build, but load it only for Excel previews.
  pending = new Promise<ExcelPreview>((resolve, reject) => {
    const script = document.createElement('script');
    script.src = '/js-preview-excel.umd.js';
    script.async = true;
    const fail = () => {
      script.remove();
      reject(new Error('Failed to load Excel preview library'));
    };
    script.onload = () => {
      if (previewWindow.jsPreviewExcel) resolve(previewWindow.jsPreviewExcel);
      else fail();
    };
    script.onerror = fail;
    document.head.appendChild(script);
  }).finally(() => { pending = undefined; });
  return pending;
}
