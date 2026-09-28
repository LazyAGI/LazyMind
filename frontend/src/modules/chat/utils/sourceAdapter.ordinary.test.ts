import { describe, expect, it } from "vitest";
import { getReferenceSources, getSourceHref, getSourceSubtitle, publicSourcesToChatSources } from "./sourceAdapter";

describe("ordinary public sources", () => {
  it("keeps platform, domain, query identity and knowledge resource routing", () => {
    const sources = publicSourcesToChatSources([
      { source_id: "web1", platform: "GitHub", domain: "github.com", title: "Issue one", kind: "repository", url: "https://github.com/a/b?issue=1" },
      { source_id: "web2", platform: "GitHub", domain: "github.com", title: "Issue two", kind: "repository", url: "https://github.com/a/b?issue=2" },
      { source_id: "doc", platform: "Knowledge", domain: null, title: "Reference", kind: "knowledge", resource_id: "doc/1", dataset_id: "kb-1" },
    ]);
    expect(getReferenceSources(sources)).toHaveLength(3);
    expect(getSourceSubtitle(sources[0])).toBe("GitHub · github.com");
    expect(getSourceHref(sources[2])).toContain("/knowledge/kb-1/doc%2F1?");
  });
  it.each(["javascript:alert(1)", "data:text/html,a", "file:///etc/passwd", "//example.com", "https://user:password@example.com"])("does not execute untrusted source target %s", url => {
    const [source] = publicSourcesToChatSources([{ source_id: "unsafe", kind: "web", title: "Unsafe", platform: "Web", domain: null, url }]);
    expect(getSourceHref(source)).toBe("#source-unsafe");
  });
});
