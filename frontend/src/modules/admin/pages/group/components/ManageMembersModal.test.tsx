import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";
import ManageMembersModal from "./ManageMembersModal";

const listGroupUsersMock = vi.hoisted(() => vi.fn());
const addGroupUsersMock = vi.hoisted(() => vi.fn());
const listUsersMock = vi.hoisted(() => vi.fn());
const createGroupApiMock = vi.hoisted(() => vi.fn());
const createUserApiMock = vi.hoisted(() => vi.fn());

vi.mock("@/modules/signin/utils/request", () => ({
  createGroupApi: createGroupApiMock,
  createUserApi: createUserApiMock,
}));

const group = { group_id: "g1", group_name: "Engineering" } as any;

describe("ManageMembersModal", () => {
  beforeEach(() => {
    listGroupUsersMock.mockReset().mockResolvedValue({
      data: { users: [{ user_id: "u1", username: "alice" }] },
    });
    addGroupUsersMock.mockReset().mockResolvedValue({ data: {} });
    listUsersMock.mockReset().mockResolvedValue({
      data: {
        data: {
          total: 2,
          users: [
            { user_id: "u1", username: "alice" },
            { user_id: "u2", username: "bob" },
          ],
        },
      },
    });
    createGroupApiMock.mockReset().mockReturnValue({
      listGroupUsersApiAuthserviceGroupGroupIdUserGet: listGroupUsersMock,
      addGroupUsersApiAuthserviceGroupGroupIdUserPost: addGroupUsersMock,
    });
    createUserApiMock.mockReset().mockReturnValue({
      listUsersApiAuthserviceUserGet: listUsersMock,
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("does not fetch when not visible", () => {
    renderWithProviders(
      <ManageMembersModal
        visible={false}
        group={group}
        isAdmin
        onCancel={vi.fn()}
      />,
    );
    expect(listGroupUsersMock).not.toHaveBeenCalled();
  });

  it("fetches current members and available users for an admin, excluding existing members", async () => {
    renderWithProviders(
      <ManageMembersModal visible group={group} isAdmin onCancel={vi.fn()} />,
    );

    await waitFor(() => expect(listGroupUsersMock).toHaveBeenCalledWith({ groupId: "g1" }));
    await waitFor(() => expect(listUsersMock).toHaveBeenCalled());
    // "alice" (u1) is already a member, so only "bob" should show on the left panel.
    await waitFor(() => expect(screen.getByText("bob")).toBeInTheDocument());
    expect(screen.queryByText("alice")).not.toBeInTheDocument();
  });

  it("does not fetch the full user list for non-admin viewers", async () => {
    renderWithProviders(
      <ManageMembersModal visible group={group} isAdmin={false} onCancel={vi.fn()} />,
    );
    await waitFor(() => expect(listGroupUsersMock).toHaveBeenCalled());
    expect(listUsersMock).not.toHaveBeenCalled();
  });

  it("warns instead of calling the API when confirming with no pending users selected", async () => {
    const { message } = await import("antd");
    const warningSpy = vi.spyOn(message, "warning");

    renderWithProviders(
      <ManageMembersModal visible group={group} isAdmin onCancel={vi.fn()} />,
    );
    await waitFor(() => expect(listUsersMock).toHaveBeenCalled());

    fireEvent.click(screen.getByRole("button", { name: "admin.confirmAdd" }));

    expect(warningSpy).toHaveBeenCalledWith("admin.selectUsersToAdd");
    expect(addGroupUsersMock).not.toHaveBeenCalled();
  });

  it("moves a selected user to the pending list and submits the add-members request", async () => {
    const onSuccess = vi.fn();
    renderWithProviders(
      <ManageMembersModal visible group={group} isAdmin onCancel={vi.fn()} onSuccess={onSuccess} />,
    );
    await waitFor(() => expect(screen.getByText("bob")).toBeInTheDocument());

    const bobRow = screen.getByText("bob").closest("tr");
    expect(bobRow).toBeTruthy();
    const rowCheckbox = bobRow!.querySelector("input[type='checkbox']");
    expect(rowCheckbox).toBeTruthy();
    fireEvent.click(rowCheckbox!);

    // The move-right button is the only enabled ant-btn with the ">" icon
    // (antd's pagination "next" arrow also uses .anticon-right but is disabled here).
    const moveRightButtons = screen.getAllByRole("button");
    const moveRightButton = moveRightButtons.find(
      (btn) => btn.querySelector(".anticon-right") && !btn.hasAttribute("disabled"),
    );
    expect(moveRightButton).toBeTruthy();
    fireEvent.click(moveRightButton!);

    fireEvent.click(screen.getByRole("button", { name: "admin.confirmAdd" }));

    await waitFor(() =>
      expect(addGroupUsersMock).toHaveBeenCalledWith({
        groupId: "g1",
        groupAddUsersBody: { user_ids: ["u2"] },
      }),
    );
    await waitFor(() => expect(onSuccess).toHaveBeenCalled());
  });
});
