import Prism from "prismjs";
import "prismjs/components/prism-markup";
import "prismjs/components/prism-css";
import "prismjs/components/prism-clike";
import "prismjs/components/prism-javascript";
import "prismjs/components/prism-typescript";
import "prismjs/components/prism-jsx";
import "prismjs/components/prism-tsx";
import "prismjs/components/prism-python";
import "prismjs/components/prism-bash";
import "prismjs/components/prism-shell-session";
import "prismjs/components/prism-json";
import "prismjs/components/prism-yaml";
import "prismjs/components/prism-markdown";
import "prismjs/components/prism-mermaid";
import "prismjs/components/prism-sql";
import "prismjs/components/prism-go";
import "prismjs/components/prism-java";
import "prismjs/components/prism-c";
import "prismjs/components/prism-cpp";
import "prismjs/components/prism-csharp";
import "prismjs/components/prism-ruby";
import "prismjs/components/prism-rust";
import "prismjs/components/prism-kotlin";
import "prismjs/components/prism-swift";
import "prismjs/components/prism-dart";
import "prismjs/components/prism-r";
import "prismjs/components/prism-scala";
import "prismjs/components/prism-lua";
import "prismjs/components/prism-docker";
import "prismjs/components/prism-nginx";
import "prismjs/components/prism-toml";
import "prismjs/components/prism-ini";
import "prismjs/components/prism-diff";
import "prismjs/components/prism-powershell";

const LANGUAGE_ALIASES: Record<string, string> = {
  csharp: "csharp",
  cs: "csharp",
  dockerfile: "docker",
  golang: "go",
  html: "markup",
  js: "javascript",
  jsx: "jsx",
  md: "markdown",
  plaintext: "text",
  py: "python",
  rb: "ruby",
  sh: "bash",
  shell: "bash",
  text: "text",
  ts: "typescript",
  tsx: "tsx",
  xml: "markup",
  yml: "yaml",
};

export function getLanguageFromClassName(className?: string) {
  const match = /(?:^|\s)language-([^\s]+)/.exec(className || "");
  const rawLanguage = match?.[1]?.toLowerCase() || "";
  return LANGUAGE_ALIASES[rawLanguage] || rawLanguage;
}

export function highlightCode(code: string, language: string) {
  const grammar = Prism.languages[language];
  if (!language || !grammar) {
    return "";
  }

  try {
    return Prism.highlight(code, grammar, language);
  } catch {
    return "";
  }
}
