export interface SlideCanvasSize { width: number; height: number }
export const DEFAULT_SLIDE_CANVAS: SlideCanvasSize = { width: 1600, height: 900 };

function pixelLength(value: string): number {
  return /^\d+(?:\.\d+)?px$/i.test(value.trim()) ? parseFloat(value) : NaN;
}

function validSize(width: number, height: number): SlideCanvasSize | null {
  return [width, height].every(value => Number.isFinite(value) && value >= 64 && value <= 8192)
    ? { width, height } : null;
}

/** Body/html have used pixel dimensions even for auto, %, and viewport units.
 * Only treat them as a canvas when the author explicitly supplies both sizes.
 * Be conservative about competing relative declarations rather than feeding
 * the iframe's current dimensions back into its own sizing. */
function explicitDocumentCanvas(root: HTMLElement, doc: Document): SlideCanvasSize | null {
  const declarations: CSSStyleDeclaration[] = [root.style];
  const collect = (rules: CSSRuleList) => {
    for (const rule of Array.from(rules)) {
      if (rule.type === 1) {
        const styleRule = rule as CSSStyleRule;
        if (root.matches(styleRule.selectorText)) declarations.push(styleRule.style);
      } else if (rule.type === 4) {
        const media = rule as CSSMediaRule;
        if (doc.defaultView?.matchMedia(media.conditionText).matches) collect(media.cssRules);
      } else if (rule.type === 12) {
        const supports = rule as CSSSupportsRule;
        if (doc.defaultView?.CSS?.supports(supports.conditionText)) collect(supports.cssRules);
      }
    }
  };
  for (const sheet of Array.from(doc.styleSheets)) {
    if (sheet.disabled) continue;
    const media = sheet.media?.mediaText;
    if (media && !doc.defaultView?.matchMedia(media).matches) continue;
    try { collect(sheet.cssRules); } catch { /* Cross-origin CSS cannot declare a trusted fixed canvas. */ }
  }
  for (const property of ['width', 'height']) {
    const values = declarations.map(style => style.getPropertyValue(property).trim()).filter(Boolean);
    if (!values.length || values.some(value => !Number.isFinite(pixelLength(value)))) return null;
  }
  const style = doc.defaultView?.getComputedStyle(root);
  return validSize(root.offsetWidth || pixelLength(style?.width ?? ''),
    root.offsetHeight || pixelLength(style?.height ?? ''));
}

/** Historical and current slides may use different fixed canvas sizes. */
export function measureSlideCanvas(doc: Document): SlideCanvasSize {
  const root = doc.querySelector<HTMLElement>('.wrapper, [data-slide-root], .slide');
  if (root) {
    const style = doc.defaultView?.getComputedStyle(root);
    const size = validSize(root.offsetWidth || pixelLength(style?.width ?? ''),
      root.offsetHeight || pixelLength(style?.height ?? ''));
    if (size) return size;
  }
  return (doc.body && explicitDocumentCanvas(doc.body, doc))
    || explicitDocumentCanvas(doc.documentElement, doc)
    || DEFAULT_SLIDE_CANVAS;
}
