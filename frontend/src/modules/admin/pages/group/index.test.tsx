import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";
import GroupManagement from "./index";

const navigateMock = vi.hoisted(() => vi.fn());
vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...actual,
    useNavigate: () => navigateMock,
  };
});

const listGroupsMock = vi.hoisted(() => vi.fn());
const deleteGroupMock = vi.hoisted(() => vi.fn());
const createGroupApiMock = vi.hoisted(() => vi.fn());

vi.mock("@/modules/signin/utils/request", () => ({
  createGroupApi: createGroupApiMock,
}));

const getUserInfoMock = vi.hoisted(() => vi.fn());
vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: getUserInfoMock },
}));

// The nested modals do their own fetching; stub them out so this test focuses
// on the list page's own behavior.
vi.mock("./components/CreateGroupModal", () => ({
  default: () => null,
}));
vi.mock("./components/ManageMembersModal", () => ({
  default: () => null,
}));

function groupsResponse(groups: Array<{ group_id: string; group_name: string; remark?: string }>) {
  return {
    data: {
      data: {
        groups,
        page: 1,
        page_size: 20,
        total: groups.length,
      },
    },
  };
}

describe("GroupManagement page", () => {
  beforeEach(() => {
    navigateMock.mockReset();
    getUserInfoMock.mockReset().mockReturnValue({ token: "tok", role: "admin" });
    listGroupsMock.mockReset().mockResolvedValue(
      groupsResponse([{ group_id: "g1", group_name: "Engineering", remark: "desc" }]),
    );
    deleteGroupMock.mockReset().mockResolvedValue({ data: {} });
    createGroupApiMock.mockReset().mockReturnValue({
      listGroupsApiAuthserviceGroupGet: listGroupsMock,
      deleteGroupApiAuthserviceGroupGroupIdDelete: deleteGroupMock,
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("fetches and renders the group list on mount", async () => {
    renderWithProviders(<GroupManagement />);
    await waitFor(() => expect(listGroupsMock).toHaveBeenCalledWith({
      page: 1,
      pageSize: 20,
      search: undefined,
    }));
    expect(await screen.findByText("Engineering")).toBeInTheDocument();
  });

  it("shows the new-group button for admin users and navigates to detail on name click", async () => {
    renderWithProviders(<GroupManagement />);
    expect(screen.getByText("admin.newGroup")).toBeInTheDocument();

    const nameLink = await screen.findByText("Engineering");
    fireEvent.click(nameLink);
    expect(navigateMock).toHaveBeenCalledWith("/admin/groups/g1");
  });

  it("hides admin-only actions for non-admin users", async () => {
    getUserInfoMock.mockReturnValue({ token: "tok", role: "user" });
    renderWithProviders(<GroupManagement />);
    await screen.findByText("Engineering");
    expect(screen.queryByText("admin.newGroup")).not.toBeInTheDocument();
  });

  it("searches groups by name", async () => {
    renderWithProviders(<GroupManagement />);
    await waitFor(() => expect(listGroupsMock).toHaveBeenCalledTimes(1));

    const searchInput = screen.getByPlaceholderText("admin.searchGroupName");
    fireEvent.change(searchInput, { target: { value: "eng" } });
    fireEvent.keyDown(searchInput, { key: "Enter", code: "Enter" });

    await waitFor(() =>
      expect(listGroupsMock).toHaveBeenCalledWith({ page: 1, pageSize: 20, search: "eng" }),
    );
  });

  it("deletes a group after confirmation and refetches the list", async () => {
    renderWithProviders(<GroupManagement />);
    await screen.findByText("Engineering");

    fireEvent.click(screen.getByText("common.delete"));
    fireEvent.click(await screen.findByText("common.confirm"));

    await waitFor(() => expect(deleteGroupMock).toHaveBeenCalledWith({ groupId: "g1" }));
    await waitFor(() => expect(listGroupsMock).toHaveBeenCalledTimes(2));
  });
});
