import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
const read = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");
describe("Local workspace request integration", () => {
  it("adds workspace fields to send and context-preview requests", () => {
    const input = read("./index.tsx");
    expect(input).toContain("workspace_id: workspaceId");
    expect(input).toContain("workspace_permission_mode: workspacePermissionMode");
    expect(input).toContain("run_in_background: true");
    expect(input).toContain("workspaceId,");
  });
  it("forwards workspace fields through both conversation request layers", () => {
    const hook = read("../newChatContainer/hooks/useChatConversation.ts");
    const layout = read("../../pages/chatLayout/index.tsx");
    for (const source of [hook, layout]) {
      expect(source).toContain("workspace_id");
      expect(source).toContain("workspace_permission_mode");
    }
  });
  it("keeps native selection behind a candidate token", () => {
    const control = read("./LocalWorkspaceControl.tsx");
    expect(control).toContain("selection_token");
    expect(control).toContain("authorizeWorkspace(runtime, candidate.token)");
    expect(control).not.toContain("canonical_path:");
  });
});
