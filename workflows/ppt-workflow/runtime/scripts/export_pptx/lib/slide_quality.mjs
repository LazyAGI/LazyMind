/** Runs in the slide's browser document; also serialized into the PDF host.
 * Report geometry defects rather than widening text or silently rasterizing it.
 */
export function inspectSlideQuality(doc = document) {
  const win = doc.defaultView;
  const root = doc.querySelector('.wrapper, .slide.canvas, .slide') || doc.body;
  const bounds = root.getBoundingClientRect();
  const findings = [];
  const seen = new Set();
  const add = (node, code) => {
    const label = node.getAttribute('data-el') || node.id || node.getAttribute('data-ppt-asset') || node.tagName;
    const key = code + ':' + label;
    if (!seen.has(key)) findings.push({ code, element: label });
    seen.add(key);
  };
  const visible = node => {
    const style = win.getComputedStyle(node);
    const rect = node.getBoundingClientRect();
    return style.display !== 'none' && style.visibility !== 'hidden' && Number(style.opacity) > 0
      && rect.width > 0 && rect.height > 0;
  };
  const distorted = matrix => {
    const sx = Math.hypot(matrix.a, matrix.b), sy = Math.hypot(matrix.c, matrix.d);
    if (!sx || !sy) return true;
    return Math.abs(sx / sy - 1) > .03
      || Math.abs(matrix.a * matrix.c + matrix.b * matrix.d) / (sx * sy) > .03;
  };
  const screenMatrix = node => {
    if (typeof node.getScreenCTM === 'function') return node.getScreenCTM();
    let matrix = new win.DOMMatrix();
    for (let parent = node; parent; parent = parent.parentElement) {
      const transform = win.getComputedStyle(parent).transform;
      if (transform && transform !== 'none') matrix = new win.DOMMatrix(transform).multiply(matrix);
    }
    return matrix;
  };
  const inspected = new Set();
  const walker = doc.createTreeWalker(root, win.NodeFilter.SHOW_TEXT);
  let text;
  while ((text = walker.nextNode())) {
    const node = text.parentElement;
    if (!text.textContent.trim() || !node || node.closest('script,style,noscript,template') || !visible(node)) continue;
    if (!inspected.has(node)) {
      inspected.add(node);
      const matrix = screenMatrix(node);
      if (matrix && distorted(matrix)) add(node, 'NON_UNIFORM_TEXT_SCALE');
      if (node.closest('text[textLength],tspan[textLength]')) add(node, 'FORCED_GLYPH_WIDTH');
    }
    const range = doc.createRange();
    range.selectNodeContents(text);
    for (const rect of range.getClientRects()) {
      if (rect.left < bounds.left - 2 || rect.top < bounds.top - 2
        || rect.right > bounds.right + 2 || rect.bottom > bounds.bottom + 2) add(node, 'TEXT_OUTSIDE_SLIDE');
    }
  }
  for (const img of root.querySelectorAll('img')) {
    if (!visible(img)) continue;
    if (!img.complete || !img.naturalWidth || !img.naturalHeight) {
      add(img, 'IMAGE_NOT_LOADED');
      continue;
    }
    const style = win.getComputedStyle(img);
    if (distorted(screenMatrix(img))) add(img, 'IMAGE_STRETCHED');
    if (['contain', 'cover', 'scale-down', 'none'].includes(style.objectFit)) continue;
    const w = parseFloat(style.width), h = parseFloat(style.height);
    if (w && h && Math.abs((w / h) / (img.naturalWidth / img.naturalHeight) - 1) > .03) add(img, 'IMAGE_STRETCHED');
  }
  return findings;
}
