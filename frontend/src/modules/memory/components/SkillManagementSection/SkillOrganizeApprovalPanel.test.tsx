import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => ({
  listSkillOrganizeApprovals: vi.fn(),
  resolveSkillOrganizeApprovals: vi.fn(),
}));

vi.mock("../../skillApi", () => api);

import SkillOrganizeApprovalPanel from "./SkillOrganizeApprovalPanel";

const t = (key: string, options?: Record<string, unknown>) => options ? `${key}:${JSON.stringify(options)}` : key;

const merge = {
  id: "0",
  type: "merge",
  status: "pending",
  sourceKeys: ["research/论文精读", "research/论文阅读"],
  targetSourceKey: "research/论文精读",
  targetName: "论文精读",
  sourceSkillIds: ["skill1", "skill2"],
  targetSkillId: "skill1",
  deleteKeys: ["research/论文阅读"],
  dependsOn: [],
  content: "merged body",
};
const meeting = { ...merge, id: "1", type: "refactor", targetName: "会议纪要", sourceKeys: ["research/会议纪要"], targetSourceKey: "", targetSkillId: "skill3", sourceSkillIds: ["skill3"], deleteKeys: [], content: "notes" };
const accepted = { ...meeting, id: "2", status: "accepted", targetName: "已接受项", content: "" };

describe("SkillOrganizeApprovalPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.listSkillOrganizeApprovals.mockResolvedValue([
      { requestId: "running-task", items: [merge], error: "" },
      { requestId: "finished-task", items: [merge, meeting, accepted], error: "" },
    ]);
    api.resolveSkillOrganizeApprovals.mockResolvedValue([{ id: "0", status: "accepted", error: "" }]);
  });

  it("hides the running task and applies one merge item without the independent change", async () => {
    const onApplied = vi.fn();
    render(
      <SkillOrganizeApprovalPanel
        t={t}
        hiddenRequestId="running-task"
        expanded
        refreshToken={1}
        onExpandedChange={() => undefined}
        onApplied={onApplied}
      />,
    );
    expect(await screen.findByText("论文精读")).toBeVisible();
    expect(screen.queryByText("admin.memorySkillOrganizeApprovalTask:{\"id\":\"running-task\"}")).toBeNull();
    expect(screen.getByText("会议纪要")).toBeVisible();
    fireEvent.click(screen.getByRole("checkbox", { name: "论文精读" }));
    fireEvent.click(screen.getByRole("button", { name: /memorySkillOrganizeApprovalAccept/ }));
    await waitFor(() => expect(api.resolveSkillOrganizeApprovals).toHaveBeenCalledWith("finished-task", ["0"], "accept"));
    expect(onApplied).toHaveBeenCalled();
  });

  it("revokes accepted items separately from pending approval", async () => {
    api.resolveSkillOrganizeApprovals.mockResolvedValue([{ id: "2", status: "pending", error: "" }]);
    render(
      <SkillOrganizeApprovalPanel
        t={t}
        expanded
        refreshToken={1}
        onExpandedChange={() => undefined}
      />,
    );
    expect(await screen.findByText("已接受项")).toBeVisible();
    fireEvent.click(screen.getByRole("checkbox", { name: "已接受项" }));
    fireEvent.click(screen.getByRole("button", { name: /memorySkillOrganizeApprovalRevoke/ }));
    await waitFor(() => expect(api.resolveSkillOrganizeApprovals).toHaveBeenCalledWith("finished-task", ["2"], "revoke"));
    expect(api.resolveSkillOrganizeApprovals).not.toHaveBeenCalledWith("running-task", expect.anything(), expect.anything());
  });
});
