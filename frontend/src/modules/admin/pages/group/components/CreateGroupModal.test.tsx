import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";
import CreateGroupModal from "./CreateGroupModal";

const createGroupMock = vi.hoisted(() => vi.fn());
const updateGroupMock = vi.hoisted(() => vi.fn());
const createGroupApiMock = vi.hoisted(() => vi.fn());

vi.mock("@/modules/signin/utils/request", () => ({
  createGroupApi: createGroupApiMock,
}));

describe("CreateGroupModal", () => {
  beforeEach(() => {
    createGroupMock.mockReset().mockResolvedValue({ data: {} });
    updateGroupMock.mockReset().mockResolvedValue({ data: {} });
    createGroupApiMock.mockReset().mockReturnValue({
      createGroupApiAuthserviceGroupPost: createGroupMock,
      updateGroupApiAuthserviceGroupGroupIdPatch: updateGroupMock,
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("does not render form fields when not visible", () => {
    renderWithProviders(
      <CreateGroupModal visible={false} onCancel={vi.fn()} onSuccess={vi.fn()} />,
    );
    expect(screen.queryByText("admin.groupName")).not.toBeInTheDocument();
  });

  it("creates a new group and calls onSuccess", async () => {
    const onSuccess = vi.fn();
    renderWithProviders(
      <CreateGroupModal visible onCancel={vi.fn()} onSuccess={onSuccess} />,
    );

    fireEvent.change(screen.getByPlaceholderText("admin.enterGroupNameWithMax"), {
      target: { value: "Engineering" },
    });
    fireEvent.click(screen.getByRole("button", { name: "OK" }));

    await waitFor(() =>
      expect(createGroupMock).toHaveBeenCalledWith({
        groupCreateBody: { group_name: "Engineering", remark: undefined, tenant_id: undefined },
      }),
    );
    await waitFor(() => expect(onSuccess).toHaveBeenCalled());
  });

  it("prefills fields and calls the update API when editing an existing group", async () => {
    const onSuccess = vi.fn();
    renderWithProviders(
      <CreateGroupModal
        visible
        editingGroup={{ group_id: "g1", group_name: "Old Name", remark: "note" } as any}
        onCancel={vi.fn()}
        onSuccess={onSuccess}
      />,
    );

    expect(screen.getByDisplayValue("Old Name")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "OK" }));

    await waitFor(() =>
      expect(updateGroupMock).toHaveBeenCalledWith({
        groupId: "g1",
        groupUpdateBody: { group_name: "Old Name", remark: "note", tenant_id: undefined },
      }),
    );
    await waitFor(() => expect(onSuccess).toHaveBeenCalled());
  });

  it("shows a validation error when the group name is empty", async () => {
    renderWithProviders(
      <CreateGroupModal visible onCancel={vi.fn()} onSuccess={vi.fn()} />,
    );
    fireEvent.click(screen.getByRole("button", { name: "OK" }));

    await waitFor(() => expect(screen.getByText("admin.enterGroupName")).toBeInTheDocument());
    expect(createGroupMock).not.toHaveBeenCalled();
  });
});
