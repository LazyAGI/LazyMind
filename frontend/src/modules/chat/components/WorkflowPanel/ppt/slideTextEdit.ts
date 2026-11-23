// Address text runs in the source document before preview scripts, resolved URLs,
// or selection overlays can change its DOM. Only the preview gets editor markup.
export function slideTextEditor(source: string) {
  const doc = new DOMParser().parseFromString(source, 'text/html');
  const preview = doc.cloneNode(true) as Document;
  const changed = new Map<number, string>();
  const skip = 'script,style,defs,symbol,clipPath,mask,pattern,marker,canvas,math,textarea,button,a,template,noscript,[hidden],[aria-hidden="true"]';
  const collect = (root: Document) => {
    const walker = root.createTreeWalker(root.body, NodeFilter.SHOW_TEXT);
    const nodes: Text[] = [];
    while (walker.nextNode()) {
      const node = walker.currentNode as Text;
      const parent = node.parentElement;
      if (!parent || !node.data.trim() || parent.closest(skip)) continue;
      if (parent.closest('svg') && !parent.matches('text, tspan, textPath')) continue;
      nodes.push(node);
    }
    return nodes;
  };
  const originals = collect(doc);
  collect(preview).forEach((node, index) => {
    let host: HTMLElement | SVGTSpanElement = node.parentElement!;
    if (host.childNodes.length !== 1) {
      const span = host.closest('svg')
        ? preview.createElementNS('http://www.w3.org/2000/svg', 'tspan')
        : preview.createElement('span');
      node.replaceWith(span);
      span.append(node);
      host = span;
    }
    host.dataset.pptTextRun = String(index);
  });
  const serialize = (document: Document) => (document.doctype ? '<!DOCTYPE html>\n' : '') + document.documentElement.outerHTML;
  return {
    preview: serialize(preview),
    value(index: number) { return originals[index]?.data; },
    edit(index: number, text: string) {
      const node = originals[index];
      if (!node) return source;
      if (!changed.has(index)) changed.set(index, node.data);
      node.data = text;
      if (changed.get(index) === text) changed.delete(index);
      return changed.size ? serialize(doc) : source;
    },
    original: source,
  };
}
