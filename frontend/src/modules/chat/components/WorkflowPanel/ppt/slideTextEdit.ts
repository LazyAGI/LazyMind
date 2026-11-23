// Stable addresses are derived from the source, before the preview inserts overlays or resolves assets.
const skipped = 'script,style,defs,symbol,clipPath,mask,pattern,marker,canvas,math,textarea,button,a,template,noscript,[hidden],[aria-hidden="true"]';
const semantic = 'h1,h2,h3,h4,h5,h6,p,li,td,th,figcaption,text,[data-el]';
const inline = 'span,b,strong,i,em,u,s,small,sub,sup,br,mark,code,tspan,textPath';

function textNodes(root: Node) {
  const walker = root.ownerDocument!.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  const nodes: Text[] = [];
  while (walker.nextNode()) nodes.push(walker.currentNode as Text);
  return nodes;
}

function blockRoot(text: Text): Element | Text {
  const parent = text.parentElement!;
  const candidate = parent.closest('td,th,text') ?? parent.closest(semantic);
  for (const root of [candidate, parent]) {
    if (!root || root.matches('body,html,#ct,#bg,.wrapper,table,ul,ol,svg,g')) continue;
    const multiline = root.matches('td,th,h1,h2,h3,h4,h5,h6,[data-el="title"],[data-el="subtitle"]');
    const allowed = multiline ? inline + ',div,p' : inline;
    const children = Array.from(root.querySelectorAll('*'));
    if (children.every(child => child.matches(allowed) && !child.matches(skipped + ',[data-el]'))) return root;
  }
  return text;
}

export function slideTextEditor(source: string) {
  const doc = new DOMParser().parseFromString(source, 'text/html');
  const preview = doc.cloneNode(true) as Document;
  const previewTexts = textNodes(preview.body);
  const previewElements = preview.body.querySelectorAll('*');
  const counterparts = new Map(Array.from(doc.body.querySelectorAll('*'), (el, i) => [el, previewElements[i]] as const));
  const roots = new Set<Element | Text>();
  const blocks = textNodes(doc.body).flatMap((text, position) => {
    const parent = text.parentElement!;
    if (!text.data.trim() || parent.closest(skipped) || parent.closest('svg') && !parent.matches('text,tspan,textPath')) return [];
    const root = blockRoot(text);
    if (roots.has(root)) return [];
    roots.add(root);
    const isText = root.nodeType === Node.TEXT_NODE;
    const owner = isText ? parent : root as Element;
    const tag = owner.localName.toLowerCase();
    const occurrence = owner === doc.body ? 0 : Array.from(doc.body.getElementsByTagName(owner.localName)).indexOf(owner);
    let host = counterparts.get(owner) ?? preview.body;
    if (isText) {
      host = parent.closest('svg') ? preview.createElementNS(parent.namespaceURI, 'tspan') : preview.createElement('span');
      previewTexts[position].replaceWith(host);
      host.append(previewTexts[position]);
    }
    host.setAttribute('data-ppt-text-block', String(roots.size - 1));
    const sourceFormats = owner.querySelectorAll('*');
    const formats = Array.from(host.querySelectorAll('*')).map((child, index) => {
      child.setAttribute('data-ppt-format', String(index));
      return sourceFormats[index].cloneNode(false) as Element;
    });
    return [{root, owner, tag, occurrence, formats, initial: isText ? text.data : owner.innerHTML, previewHTML: undefined as string | undefined}];
  });

  const changed = new Set<number>();
  function result(index: number) {
    const {root, owner, initial} = blocks[index];
    const current = root.nodeType === Node.TEXT_NODE ? root.textContent : owner.innerHTML;
    if (initial === current) changed.delete(index);
    else changed.add(index);
    return changed.size ? (doc.doctype ? '<!DOCTYPE html>\n' : '') + doc.documentElement.outerHTML : source;
  }

  return {
    original: source,
    preview: (doc.doctype ? '<!DOCTYPE html>\n' : '') + preview.documentElement.outerHTML,
    content(index: number) { return blocks[index]?.previewHTML; },
    selection(index: number) {
      const block = blocks[index];
      if (!block) return;
      const {root, owner, tag, occurrence} = block;
      const direct = root.nodeType === Node.TEXT_NODE;
      const nodes = direct ? [root as Text] : textNodes(owner);
      return {tag, occurrence, el: owner.getAttribute('data-el') || '', segments: nodes.map(text => text.data.trim()).filter(Boolean),
        ...(direct ? {direct_index: textNodes(owner).filter(n => n.parentElement === owner && n.data.trim()).indexOf(root as Text)} : {})};
    },
    editBlock(index: number, host: Element) {
      const {root, owner, formats} = blocks[index];
      if (root.nodeType === Node.TEXT_NODE) {
        root.textContent = (host as HTMLElement).innerText ?? host.textContent;
      } else {
        // Keep source attributes only. Rich pasted markup and iframe-only decorations must not enter the artifact.
        const copy = (node: Node): Node => {
          if (node.nodeType === Node.TEXT_NODE) return doc.createTextNode(node.textContent || '');
          const el = node as Element;
          const format = el.getAttribute?.('data-ppt-format');
          const original = format != null ? formats[Number(format)] : undefined;
          let next: Node = doc.createDocumentFragment();
          if (original && original.localName === el.localName) next = original.cloneNode(false);
          else if (el.localName === 'br' || el.localName === 'div') next = doc.createElement(el.localName);
          for (const child of Array.from(node.childNodes)) next.appendChild(copy(child));
          return next;
        };
        owner.replaceChildren(...Array.from(host.childNodes, copy));
      }
      blocks[index].previewHTML = host.innerHTML;
      return result(index);
    },
  };
}

// An SVG label uses an HTML input. Apply its changed range to the existing tspans,
// so editing the second word retains the first word's position and formatting.
export function editSvgText(label: Element, next: string) {
  const nodes = textNodes(label);
  const old = label.textContent || '';
  let start = 0;
  while (start < old.length && start < next.length && old[start] === next[start]) start++;
  let end = old.length;
  let tail = next.length;
  while (end > start && tail > start && old[end - 1] === next[tail - 1]) { end--; tail--; }
  let offset = 0;
  let inserted = false;
  for (const [index, node] of nodes.entries()) {
    const stop = offset + node.length;
    const replacement: boolean = !inserted && (start < stop || index === nodes.length - 1) && start >= offset;
    node.data = node.data.slice(0, Math.max(0, start - offset)) + (replacement ? next.slice(start, tail) : '')
      + node.data.slice(Math.max(0, end - offset));
    inserted ||= replacement;
    offset = stop;
  }
  if (!nodes.length) label.append(next);
}
