import { describe, expect, it } from "vitest";
import { guideDownloadDocument } from "./guideDownload";

function sample() {
  const article = document.createElement("article");
  article.innerHTML = '<h1>A &amp; B</h1><section data-guide-role="overview"><div data-guide-block="a"><strong>重点</strong><span class="katex-mathml"><math><mi>x</mi></math></span><span class="katex-html">duplicate</span><details><summary>术语</summary>解释</details><figure><button><img src="data:image/png;base64,AA==" /></button><figcaption>论文图1</figcaption></figure></div></section><section data-guide-role="queued"></section>';
  return article;
}

describe("downloaded paper guide", () => {
  it("puts the edition mark after each readable chapter and outside the print body", async () => {
    const source = sample();
    const { html, title } = await guideDownloadDocument(source, "zh", "html");
    const doc = new DOMParser().parseFromString(html, "text/html");
    expect(title).toBe("A & B");
    expect(doc.documentElement.lang).toBe("zh-CN");
    expect(doc.querySelectorAll("section")).toHaveLength(1);
    expect(doc.querySelector("section > footer")?.textContent).toBe("LazyMind");
    expect(doc.body.textContent).toContain("已导出 1 / 2 个章节");
    expect(doc.querySelector("math mi")?.textContent).toBe("x");
    expect(doc.querySelector("strong")?.textContent).toBe("重点");
    expect(doc.querySelector("figure img")).toBeTruthy();
    expect(doc.querySelector("figcaption")?.textContent).toBe("论文图1");
    expect(doc.querySelector("button,.katex-html,details[open]")).toBeNull();
    expect(source.querySelector("button img")).toBeTruthy();
  });

  it("expands definitions for paper without changing the reader", async () => {
    const source = sample();
    const { html } = await guideDownloadDocument(source, "en", "pdf");
    expect(new DOMParser().parseFromString(html, "text/html").querySelector("details")?.open).toBe(true);
    expect(source.querySelector("details")?.open).toBe(false);
  });

  it("waits for figure rendering before taking the snapshot", async () => {
    const source = sample();
    document.body.append(source);
    try {
      const frame = document.createElement("div");
      frame.className = "paper-guide-image-frame";
      frame.setAttribute("aria-busy", "true");
      source.querySelector("figure")!.replaceChildren(frame);
      const pending = guideDownloadDocument(source, "en", "html");
      await Promise.resolve();
      frame.innerHTML = '<img src="data:image/png;base64,AA==" alt="Late figure">';
      frame.setAttribute("aria-busy", "false");
      expect((await pending).html).toContain('alt="Late figure"');
    } finally {
      source.remove();
    }
  });

  it("cancels an export immediately when its reader is removed while figures load", async () => {
    const source = sample();
    const reader = document.createElement("main");
    reader.append(source);
    document.body.append(reader);
    source.querySelector("figure")!.innerHTML = '<div class="paper-guide-image-frame" aria-busy="true"></div>';
    const pending = guideDownloadDocument(source, "en", "html");
    reader.remove();
    await expect(pending).rejects.toThrow("The reading version changed");
  });

  it("keeps offline demos in an isolated frame", async () => {
    const source = sample();
    const frame = document.createElement("iframe");
    frame.srcdoc = "<p>demo</p>";
    source.append(frame);
    const { html } = await guideDownloadDocument(source, "en", "html");
    const exported = new DOMParser().parseFromString(html, "text/html").querySelector("iframe");
    expect(exported?.getAttribute("sandbox")).toBe("allow-scripts");
    expect(exported?.srcdoc).toBe("<p>demo</p>");
  });
});
