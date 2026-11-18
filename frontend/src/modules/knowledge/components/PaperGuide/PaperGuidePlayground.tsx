import { defaultTreeAdapter, parse, parseFragment, serializeOuter, type DefaultTreeAdapterTypes } from "parse5";
import { useEffect, useMemo, useRef, useState } from "react";

const collapsedHeight = 760;
const maxExpandedHeight = 4096;

const policy = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src 'none'; connect-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'";
const removedTags = new Set(["base", "iframe", "frame", "frameset", "object", "embed", "link"]);

// Model markup stays in the parser's plain object tree until the sandbox loads srcdoc.
export function playgroundDocument(html: string, tokens: Record<string, string> = {}) {
  const doc = parse(html);
  const root = doc.childNodes.find(defaultTreeAdapter.isElementNode)!;
  const head = root.childNodes.filter(defaultTreeAdapter.isElementNode).find(node => node.tagName === "head")!;
  const parents: DefaultTreeAdapterTypes.ParentNode[] = [root];
  while (parents.length) {
    const parent = parents.pop()!;
    parent.childNodes = parent.childNodes.filter(node => {
      if (!defaultTreeAdapter.isElementNode(node)) return true;
      if (removedTags.has(node.tagName)
        || (node.tagName === "meta" && node.attrs.some(attr => attr.name === "http-equiv"))
        || (node.tagName === "script" && node.attrs.some(attr => attr.name === "src"))) return false;
      if (node.tagName === "a" || node.tagName === "area") {
        node.attrs = node.attrs.filter(attr => attr.name !== "href" && attr.name !== "target");
      }
      parents.push("content" in node ? (node as DefaultTreeAdapterTypes.Template).content : node);
      return true;
    });
  }
  const defaults = { bg: "#ffffff", fg: "#202b3c", muted: "#657389", accent: "#4263ad", border: "#dce2ec", ...tokens };
  const style = `:root{${Object.entries(defaults).map(([key, value]) => `--guide-${key}:${value.replace(/[;{}<>]/g, "")}`).join(";")}}html{color:var(--guide-fg);background:var(--guide-bg);font-family:system-ui,sans-serif;color-scheme:light dark}body{margin:0;padding:20px;box-sizing:border-box}*{box-sizing:border-box}svg,canvas,img{max-width:100%}button,input,select{font:inherit}button{cursor:pointer}*:focus-visible{outline:2px solid var(--guide-accent);outline-offset:3px}@media(prefers-reduced-motion:reduce){*,*::before,*::after{animation:none!important;transition:none!important}}`;
  const bootstrap = `addEventListener('message', event => {
    if (event.source !== parent || event.data?.type !== 'guide-playground-theme') return;
    for (const key of ['bg','fg','muted','accent','border']) {
      const color = event.data.tokens?.[key];
      if (typeof color === 'string' && CSS.supports('color', color))
        document.documentElement.style.setProperty('--guide-' + key, color);
    }
  });
  (()=>{const send=(type,extra={})=>parent.postMessage({type,...extra},'*');addEventListener('error',()=>send('guide-playground-error'));addEventListener('unhandledrejection',()=>send('guide-playground-error'));addEventListener('DOMContentLoaded',()=>{send('guide-playground-ready');const size=()=>send('guide-playground-height',{height:Math.ceil(Math.max(document.body.getBoundingClientRect().height,document.body.scrollHeight))});new ResizeObserver(size).observe(document.body);size()});document.addEventListener('click',e=>{if(e.target.closest?.('a,area'))e.preventDefault()},true);document.addEventListener('submit',e=>e.preventDefault(),true)})();`;
  const shell = parseFragment(`<meta http-equiv="Content-Security-Policy" content="${policy}"><style>${style}</style><script>${bootstrap}</script>`);
  head.childNodes.unshift(...shell.childNodes);
  return "<!doctype html>" + serializeOuter(root);
}
export default function PaperGuidePlayground({ html, language, title }: { html: string; language: string; title: string }) {
  const zh = language === "zh";
  const frame = useRef<HTMLIFrameElement>(null);
  const container = useRef<HTMLDivElement>(null);
  const tokens = useRef<Record<string, string>>({});
  const [reset, setReset] = useState(0);
  const [height, setHeight] = useState(420);
  const [expanded, setExpanded] = useState(false);
  const [failed, setFailed] = useState(false);
  const srcDoc = useMemo(() => {
    try {
      return playgroundDocument(html);
    } catch {
      return "";
    }
  }, [html]);
  function applyTheme() {
    frame.current?.contentWindow?.postMessage({ type: "guide-playground-theme", tokens: tokens.current }, "*");
  }
  useEffect(() => {
    const read = () => {
      if (!container.current) return;
      const css = getComputedStyle(container.current);
      const next: Record<string, string> = {};
      for (const [key, host] of Object.entries({ bg: "surface", fg: "ink", muted: "muted", accent: "accent", border: "border" })) {
        const value = css.getPropertyValue(`--paper-${host}`).trim();
        if (value) {
          const probe = document.createElement("span");
          probe.style.color = `var(--paper-${host})`;
          container.current.appendChild(probe);
          next[key] = getComputedStyle(probe).color;
          probe.remove();
        }
      }
      tokens.current = next;
      applyTheme();
    };
    read();
    const observer = new MutationObserver(read);
    for (let element: HTMLElement | null = container.current; element; element = element.parentElement) observer.observe(element, { attributes: true, attributeFilter: ["class", "style", "data-theme", "data-guide-paper", "data-guide-light"] });
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    setFailed(!srcDoc);
    setExpanded(false);
    setHeight(420);
    const receive = (event: MessageEvent) => {
      if (event.source !== frame.current?.contentWindow || !event.data || typeof event.data !== "object") return;
      if (event.data.type === "guide-playground-error") setFailed(true);
      if (event.data.type === "guide-playground-height" && typeof event.data.height === "number" && Number.isFinite(event.data.height)) setHeight(Math.max(1, Math.min(maxExpandedHeight, Math.ceil(event.data.height))));
    };
    window.addEventListener("message", receive);
    return () => window.removeEventListener("message", receive);
  }, [srcDoc, reset]);
  return <div className="guide-playground" ref={container}>
    <div className="guide-playground-toolbar"><span>{zh ? "动手探索" : "Explore the idea"}</span><div>
      <button type="button" onClick={() => { setReset(value => value + 1); }}>{zh ? "重载交互" : "Reset"}</button>
      {height > collapsedHeight && <button type="button" aria-expanded={expanded} onClick={() => setExpanded(value => !value)}>{expanded ? (zh ? "收起" : "Collapse") : (zh ? "展开" : "Expand")}</button>}
    </div></div>
    {failed && <p role="status">{zh ? "交互未能运行，可先阅读上方说明，或重载交互。" : "This interaction could not run. Read the explanation above or reset it."}</p>}
    {srcDoc && <iframe key={reset} ref={frame} title={title} onLoad={applyTheme} sandbox="allow-scripts" referrerPolicy="no-referrer" srcDoc={srcDoc} style={{ height: expanded ? height : Math.min(height, collapsedHeight) }} />}
  </div>;
}
