import flowStyles from "./PaperGuideFlow.scss?inline";
import styles from "./guideDownload.css?inline";

type Format = "html" | "pdf";
const imprint = '<footer class="guide-imprint"><svg aria-hidden="true" viewBox="0 0 18 18" fill="none" stroke="currentColor" stroke-width="1.25" stroke-linejoin="round"><path d="M9 5Q6 2.8 1.8 3.7v9q4.2-.9 7.2 1.3 3-2.2 7.2-1.3v-9Q12 2.8 9 5Zm0 0v9"/></svg>LazyMind</footer>';

async function embedImage(img: HTMLImageElement) {
  if (/^data:image\/(png|jpeg|webp);base64,/i.test(img.src)) return;
  if (!img.src.startsWith("blob:")) throw new Error("Invalid figure source");
  const response = await fetch(img.src);
  if (!response.ok) throw new Error("Figure download failed");
  const blob = await response.blob();
  if (!/^image\/(png|jpeg|webp)$/i.test(blob.type)) throw new Error("Invalid figure format");
  img.src = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(blob);
  });
}

function waitForFigures(article: HTMLElement, zh: boolean) {
  const pending = '.paper-guide-image-frame[aria-busy="true"]';
  if (!article.querySelector(pending)) return Promise.resolve();
  const heading = article.querySelector("h2")?.id;
  return new Promise<void>((resolve, reject) => {
    function finish(error?: string) {
      observer.disconnect();
      clearTimeout(timer);
      if (error) reject(new Error(error));
      else resolve();
    }
    const observer = new MutationObserver(() => {
      if (!article.isConnected || heading !== article.querySelector("h2")?.id) {
        finish(zh ? "阅读版本已切换，请重新下载。" : "The reading version changed. Download again.");
      } else if (!article.querySelector(pending)) finish();
    });
    const timer = setTimeout(() => finish(zh ? "原图仍在加载，请稍后重试。" : "Figures are still loading. Try again shortly."), 20000);
    observer.observe(article, { childList: true, subtree: true, attributes: true, attributeFilter: ["aria-busy"] });
    // Removing a reader does not emit a mutation inside its own subtree.
    for (let parent = article.parentNode; parent; parent = parent.parentNode) {
      observer.observe(parent, { childList: true });
    }
  });
}

export async function guideDownloadDocument(source: HTMLElement, language: string, format: Format) {
  const zh = language === "zh";
  await waitForFigures(source, zh);
  const article = source.cloneNode(true) as HTMLElement;
  const doc = document.implementation.createHTMLDocument();
  doc.documentElement.lang = zh ? "zh-CN" : "en";
  doc.title = article.querySelector("h1")?.textContent?.trim() || (zh ? "论文导读" : "Paper guide");
  doc.head.insertAdjacentHTML("afterbegin", '<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">');
  const style = doc.createElement("style");
  style.textContent = flowStyles + styles;
  doc.head.append(style);
  article.querySelectorAll(".paper-guide-block-tools,.guide-playground-toolbar,.guide-glossary-chevron,.guide-section-icon,.katex-html,.paper-guide-next").forEach(el => el.remove());
  const sections = Array.from(article.querySelectorAll("section[data-guide-role]"));
  const readyCount = sections.filter(section => section.querySelector("[data-guide-block]")).length;
  if (readyCount < sections.length) {
    const note = doc.createElement("p");
    note.textContent = zh ? `已导出 ${readyCount} / ${sections.length} 个章节` : `Exported ${readyCount} of ${sections.length} chapters`;
    article.querySelector("h1")?.after(note);
  }
  for (const section of sections) {
    if (!section.querySelector("[data-guide-block]")) section.remove();
    else section.insertAdjacentHTML("beforeend", imprint);
  }
  for (const control of article.querySelectorAll(".guide-interaction__steps,.guide-interaction__actions,.guide-interaction__filter,.guide-interaction input,.guide-interaction__range,.guide-interaction__panel > small.guide-interaction__direction")) control.remove();
  for (const item of article.querySelectorAll("[data-export-item]")) {
    item.removeAttribute("hidden");
    item.removeAttribute("data-export-item");
  }
  for (const button of article.querySelectorAll("button")) {
    const image = button.querySelector("img");
    if (image) {
      image.hidden = false;
      button.replaceWith(image);
    }
    else button.replaceWith(button.textContent || "");
  }
  for (const status of article.querySelectorAll(".paper-guide-image-frame [role=status]")) {
    status.textContent = zh ? "原图未能导出" : "Figure unavailable in this export";
  }
  for (const frame of article.querySelectorAll("iframe")) {
    frame.removeAttribute("src");
    frame.setAttribute("sandbox", "allow-scripts");
    frame.referrerPolicy = "no-referrer";
  }
  if (format === "pdf") article.querySelectorAll("details").forEach(el => { el.open = true; });
  await Promise.all(Array.from(article.querySelectorAll("img"), embedImage));
  doc.body.append(article);
  return { title: doc.title, html: "<!doctype html>" + doc.documentElement.outerHTML };
}

export async function downloadPaperGuide(source: HTMLElement, language: string, format: Format) {
  // Open during the click gesture, before figure blobs are read.
  const sheet = format === "pdf" ? window.open("", "_blank") : null;
  if (format === "pdf" && !sheet) throw new Error(language === "zh" ? "请允许弹出打印窗口后重试。" : "Allow the print window and try again.");
  try {
    if (sheet) {
      sheet.opener = null;
      sheet.document.body.textContent = language === "zh" ? "正在准备打印文档…" : "Preparing the print document…";
    }
    const { title, html } = await guideDownloadDocument(source, language, format);
    if (sheet) {
      sheet.document.open();
      // document.open clears listeners, so attach the document readiness listener afterwards.
      const ready = new Promise<void>((resolve, reject) => {
        const timer = setTimeout(() => reject(new Error(language === "zh" ? "打印窗口加载超时" : "Print window timed out")), 30000);
        sheet.addEventListener("load", () => { clearTimeout(timer); resolve(); }, { once: true });
      });
      sheet.document.write(html);
      sheet.document.close();
      await ready;
      await sheet.document.fonts.ready;
      sheet.focus();
      sheet.print();
      return;
    }
    const url = URL.createObjectURL(new Blob([html], { type: "text/html;charset=utf-8" }));
    const link = document.createElement("a");
    link.href = url;
    link.download = title.replace(/[\\/:*?"<>|]/g, "-").slice(0, 120) + ".html";
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 60000);
  } catch (error) {
    sheet?.close();
    throw error;
  }
}
