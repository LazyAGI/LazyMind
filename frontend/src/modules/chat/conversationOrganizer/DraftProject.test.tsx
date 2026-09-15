import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import DraftProject from "./DraftProject";
import { listConversationGroups, type ConversationGroup } from "./api";

vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock("@/runtime/mode", () => ({ getRuntimeMode: () => "local" }));
vi.mock("@/modules/chat/utils/localWorkspace", () => ({ authorizeWorkspace: vi.fn(), selectWorkspaceCandidate: vi.fn() }));
vi.mock("./api", () => ({ listConversationGroups: vi.fn() }));
const workspace = { workspace_id: "workspace", display_name: "demo", path: "/code/demo", status: "active" as const, version: 1, source: "local" as const };
beforeEach(() => { vi.mocked(listConversationGroups).mockResolvedValue([]); });

it("keeps an editable project draft until sending, and reuses an exact existing directory", async () => {
 const onChange = vi.fn();
 const { unmount } = render(<DraftProject workspace={workspace} onChange={onChange} />);
 await waitFor(() => expect(onChange).toHaveBeenLastCalledWith("demo", true));
 fireEvent.change(screen.getByRole("textbox", { name: "conversationProject.name" }), { target: { value: "My project" } });
 expect(onChange).toHaveBeenLastCalledWith("My project", true);
 fireEvent.change(screen.getByRole("textbox"), { target: { value: " " } });
 expect(onChange).toHaveBeenLastCalledWith("", false);
 unmount();
 vi.mocked(listConversationGroups).mockResolvedValue([{ id: "existing", kind: "project", name: "Existing", path: workspace.path } as ConversationGroup]);
 render(<DraftProject workspace={workspace} onChange={onChange} />);
 await screen.findByText("Existing");
 expect(screen.queryByRole("textbox")).toBeNull();
 expect(onChange).toHaveBeenLastCalledWith(undefined, true);
});
