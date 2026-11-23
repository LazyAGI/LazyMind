// Runs inside the opaque-origin scene. KaTeX produces native MathML so no
// stylesheet, external font, or request to the parent frame is necessary.
document.addEventListener("DOMContentLoaded", () => {
  const ignoredTags = ["script", "noscript", "style", "textarea", "pre", "code", "option", "svg", "math", "template"];
  const ignored = [...ignoredTags, ".katex"].join(",");
  const options = {
    output: "mathml",
    trust: false,
    throwOnError: false,
    delimiters: [
      { left: "$$", right: "$$", display: true },
      { left: "\\[", right: "\\]", display: true },
      { left: "\\(", right: "\\)", display: false },
      { left: "$", right: "$", display: false },
    ],
    ignoredTags,
    ignoredClasses: ["katex"],
  };
  const render = element => {
    if (!element || element.closest(ignored)) return;
    window.renderMathInElement(element, options);
    // Older scenes wrote named subscripts such as L_simple directly in prose.
    const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
    const labels = [];
    while (walker.nextNode()) {
      const text = walker.currentNode;
      if (!text.parentElement.closest(ignored) && /\b[A-Z]_[a-z]+\b/.test(text.data)) labels.push(text);
    }
    for (const text of labels) {
      if (!text.isConnected) continue;
      const marked = text.data.replace(/\b([A-Z])_([a-z]+)\b/g, "\\($1_{\\mathrm{$2}}\\)");
      text.data = marked;
      window.renderMathInElement(text.parentElement, options);
    }
  };

  render(document.body);
  new MutationObserver(records => {
    const changed = new Set();
    for (const record of records) {
      if (record.type === "characterData") changed.add(record.target.parentElement);
      for (const node of record.addedNodes) {
        if (node.nodeType === Node.TEXT_NODE) changed.add(node.parentElement);
        if (node.nodeType === Node.ELEMENT_NODE) changed.add(node);
      }
    }
    for (const element of changed) {
      if (element?.isConnected) render(element);
    }
  }).observe(document.body, { childList: true, subtree: true, characterData: true });
});
