import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, fireEvent, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { I18nextProvider } from "react-i18next";
import { testI18n } from "@/test/testUtils";
import GroupDetail from "./detail";

const navigateMock = vi.hoisted(() => vi.fn());
vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...actual,
    useNavigate: () => navigateMock,
  };
});

const getGroupMock = vi.hoisted(() => vi.fn());
const listGroupUsersMock = vi.hoisted(() => vi.fn());
const removeGroupUsersMock = vi.hoisted(() => vi.fn());
const createGroupApiMock = vi.hoisted(() => vi.fn());

vi.mock("@/modules/signin/utils/request", () => ({
  createGroupApi: createGroupApiMock,
}));

const getUserInfoMock = vi.hoisted(() => vi.fn());
vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: getUserInfoMock },
}));

vi.mock("./components/CreateGroupModal", () => ({ default: () => null }));
vi.mock("./components/ManageMembersModal", () => ({ default: () => null }));

function renderDetail(id = "g1") {
  return render(
    <I18nextProvider i18n={testI18n}>
      <MemoryRouter initialEntries={[`/admin/groups/${id}`]}>
        <Routes>
          <Route path="/admin/groups/:id" element={<GroupDetail />} />
        </Routes>
      </MemoryRouter>
    </I18nextProvider>,
  );
}

describe("GroupDetail page", () => {
  beforeEach(() => {
    navigateMock.mockReset();
    getUserInfoMock.mockReset().mockReturnValue({ token: "tok", role: "admin" });
    getGroupMock.mockReset().mockResolvedValue({
      data: { data: { group_id: "g1", group_name: "Engineering", remark: "desc" } },
    });
    listGroupUsersMock.mockReset().mockResolvedValue({
      data: { users: [{ user_id: "u1", username: "alice", role: "member" }] },
    });
    removeGroupUsersMock.mockReset().mockResolvedValue({ data: {} });
    createGroupApiMock.mockReset().mockReturnValue({
      getGroupApiAuthserviceGroupGroupIdGet: getGroupMock,
      listGroupUsersApiAuthserviceGroupGroupIdUserGet: listGroupUsersMock,
      removeGroupUsersApiAuthserviceGroupGroupIdUserRemovePost: removeGroupUsersMock,
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("redirects non-admin users back to the group list", () => {
    getUserInfoMock.mockReturnValue({ token: "tok", role: "user" });
    renderDetail();
    expect(navigateMock).toHaveBeenCalledWith("/admin/groups", { replace: true });
    expect(getGroupMock).not.toHaveBeenCalled();
  });

  it("fetches and renders the group's basic info and member list for admins", async () => {
    renderDetail();
    await waitFor(() => expect(getGroupMock).toHaveBeenCalledWith({ groupId: "g1" }));
    await waitFor(() => expect(listGroupUsersMock).toHaveBeenCalledWith({ groupId: "g1" }));
    expect(await screen.findAllByText("Engineering")).not.toHaveLength(0);
    expect(await screen.findByText("alice")).toBeInTheDocument();
  });

  it("filters members by the search box", async () => {
    listGroupUsersMock.mockResolvedValue({
      data: {
        users: [
          { user_id: "u1", username: "alice", role: "member" },
          { user_id: "u2", username: "bob", role: "member" },
        ],
      },
    });
    renderDetail();
    await screen.findByText("bob");

    fireEvent.change(screen.getByPlaceholderText("admin.searchUsername"), {
      target: { value: "ali" },
    });

    expect(screen.getByText("alice")).toBeInTheDocument();
    expect(screen.queryByText("bob")).not.toBeInTheDocument();
  });

  it("removes a member after confirmation", async () => {
    renderDetail();
    await screen.findByText("alice");

    fireEvent.click(screen.getByText("admin.removeMember"));
    fireEvent.click(await screen.findByText("common.confirm"));

    await waitFor(() =>
      expect(removeGroupUsersMock).toHaveBeenCalledWith({
        groupId: "g1",
        groupRemoveUsersBody: { user_ids: ["u1"] },
      }),
    );
  });
});
