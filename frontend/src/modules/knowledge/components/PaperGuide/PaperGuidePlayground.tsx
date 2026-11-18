import { useEffect, useMemo, useRef, useState } from "react";

const policy = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src 'none'; connect-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'";
// Parse in an inert document; only the isolated opaque-origin iframe executes it.
export function playgroundDocument(html: string, tokens: Record<string, string> = {}) {
  const doc = new DOMParser().parseFromString(html, "text/html");
  doc.querySelectorAll("base, iframe, frame, frameset, object, embed, meta[http-equiv], script[src], link").forEach(el => el.remove());
  doc.querySelectorAll("a, area").forEach(el => { el.removeAttribute("href"); el.removeAttribute("target"); });
  const csp = doc.createElement("meta");
  csp.httpEquiv = "Content-Security-Policy";
  csp.content = policy;
  doc.head.prepend(csp);
  const style = doc.createElement("style");
  const defaults = { bg: "#ffffff", fg: "#202b3c", muted: "#657389", accent: "#4263ad", border: "#dce2ec", ...tokens };
  style.textContent = `:root{${Object.entries(defaults).map(([key, value]) => `--guide-${key}:${value.replace(/[;{}<>]/g, "")}`).join(";")}}html{color:var(--guide-fg);background:var(--guide-bg);font-family:system-ui,sans-serif;color-scheme:light dark}body{margin:0;padding:20px;box-sizing:border-box}*{box-sizing:border-box}svg,canvas,img{max-width:100%}button,input,select{font:inherit}button{cursor:pointer}*:focus-visible{outline:2px solid var(--guide-accent);outline-offset:3px}@media(prefers-reduced-motion:reduce){*,*::before,*::after{animation:none!important;transition:none!important}}`;
  doc.head.insertBefore(style, csp.nextSibling);
  const bootstrap = doc.createElement("script");
  bootstrap.textContent = `(()=>{const send=(type,extra={})=>parent.postMessage({type,...extra},'*');addEventListener('error',()=>send('guide-playground-error'));addEventListener('unhandledrejection',()=>send('guide-playground-error'));addEventListener('DOMContentLoaded',()=>{send('guide-playground-ready');const size=()=>send('guide-playground-height',{height:document.documentElement.scrollHeight});new ResizeObserver(size).observe(document.body);size()});document.addEventListener('click',e=>{if(e.target.closest?.('a,area'))e.preventDefault()},true);document.addEventListener('submit',e=>e.preventDefault(),true)})();`;
  doc.head.insertBefore(bootstrap, style.nextSibling);
  return "<!doctype html>" + doc.documentElement.outerHTML;
}
export default function PaperGuidePlayground({ html, language, title }: { html: string; language: string; title: string }) {
  const zh = language === "zh";
  const frame = useRef<HTMLIFrameElement>(null);
  const container = useRef<HTMLDivElement>(null);
  const [tokens, setTokens] = useState<Record<string, string>>({});
  const [reset, setReset] = useState(0);
  const [height, setHeight] = useState(420);
  const [expanded, setExpanded] = useState(false);
  const [failed, setFailed] = useState(false);
  const srcDoc = useMemo(() => playgroundDocument(html, tokens), [html, tokens]);
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
      setTokens(previous => JSON.stringify(previous) === JSON.stringify(next) ? previous : next);
    };
    read();
    const observer = new MutationObserver(read);
    for (let element: HTMLElement | null = container.current; element; element = element.parentElement) observer.observe(element, { attributes: true, attributeFilter: ["class", "style", "data-theme"] });
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    setFailed(false);
    const receive = (event: MessageEvent) => {
      if (event.source !== frame.current?.contentWindow || !event.data || typeof event.data !== "object") return;
      if (event.data.type === "guide-playground-error") setFailed(true);
      if (event.data.type === "guide-playground-height" && typeof event.data.height === "number" && Number.isFinite(event.data.height)) setHeight(Math.max(240, Math.min(760, event.data.height)));
    };
    window.addEventListener("message", receive);
    return () => window.removeEventListener("message", receive);
  }, [srcDoc, reset]);
  return <div className="guide-playground" ref={container}>
    <div className="guide-playground-toolbar"><span>{zh ? "动手探索" : "Explore the idea"}</span><div>
      <button type="button" onClick={() => { setReset(value => value + 1); setHeight(420); }}>{zh ? "重载交互" : "Reset"}</button>
      <button type="button" aria-expanded={expanded} onClick={() => setExpanded(value => !value)}>{expanded ? (zh ? "收起" : "Collapse") : (zh ? "展开" : "Expand")}</button>
    </div></div>
    {failed && <p role="status">{zh ? "交互未能运行，可先阅读上方说明，或重载交互。" : "This interaction could not run. Read the explanation above or reset it."}</p>}
    <iframe key={reset} ref={frame} title={title} sandbox="allow-scripts" referrerPolicy="no-referrer" srcDoc={srcDoc} style={{ height: expanded ? 900 : height }} />
  </div>;
}
