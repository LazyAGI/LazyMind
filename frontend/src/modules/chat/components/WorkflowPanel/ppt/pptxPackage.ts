import JSZip from '@progress/jszip-esm';

/** PptxGenJS 4 declares one master per slide but only writes the actual masters. */
export async function normalizePptxPackage(source: ArrayBuffer): Promise<Blob> {
  const zip = await new JSZip().loadAsync(source);
  const contentTypes = zip.file('[Content_Types].xml');
  if (!contentTypes) throw new Error('PPTX content types are missing');
  const xml = await contentTypes.async('string');
  const document = new DOMParser().parseFromString(xml, 'application/xml');
  for (const override of Array.from(document.getElementsByTagName('Override'))) {
    const part = (override.getAttribute('PartName') || '').replace(/^\//, '');
    if (/^ppt\/slideMasters\/slideMaster\d+\.xml$/.test(part) && !zip.file(part)) {
      override.remove();
    }
  }
  zip.file('[Content_Types].xml', new XMLSerializer().serializeToString(document));
  return zip.generateAsync({
    type: 'blob', compression: 'DEFLATE',
    mimeType: 'application/vnd.openxmlformats-officedocument.presentationml.presentation',
  });
}
