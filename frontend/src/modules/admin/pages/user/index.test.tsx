import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";
import UserManagement from "./index";

const listUsersMock = vi.hoisted(() => vi.fn());
const disableUserMock = vi.hoisted(() => vi.fn());
const createUserApiMock = vi.hoisted(() => vi.fn());

vi.mock("@/modules/signin/utils/request", () => ({
  createUserApi: createUserApiMock,
}));

vi.mock("./components/CreateUserModal", () => ({
  default: () => null,
}));

function usersResponse(users: Array<Record<string, unknown>>) {
  return {
    data: {
      data: {
        users,
        page: 1,
        page_size: 20,
        total: users.length,
      },
    },
  };
}

describe("UserManagement page", () => {
  beforeEach(() => {
    listUsersMock.mockReset().mockResolvedValue(
      usersResponse([
        { user_id: "u1", username: "alice", email: "alice@example.com", role_name: "user", status: "active" },
        { user_id: "u2", username: "bob", email: "", role_name: "admin", status: "disabled", is_bootstrap_admin: true },
      ]),
    );
    disableUserMock.mockReset().mockResolvedValue({ data: {} });
    createUserApiMock.mockReset().mockReturnValue({
      listUsersApiAuthserviceUserGet: listUsersMock,
      disableUserApiAuthserviceUserUserIdDisablePatch: disableUserMock,
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("fetches and renders the user list on mount", async () => {
    renderWithProviders(<UserManagement />);
    await waitFor(() => expect(listUsersMock).toHaveBeenCalledWith({
      page: 1,
      pageSize: 20,
      search: undefined,
    }));
    expect(await screen.findByText("alice")).toBeInTheDocument();
    expect(screen.getByText("bob")).toBeInTheDocument();
  });

  it("disables an active user after confirmation", async () => {
    renderWithProviders(<UserManagement />);
    await screen.findByText("alice");

    const disableButtons = screen.getAllByText("admin.disable");
    fireEvent.click(disableButtons[0]);
    fireEvent.click(await screen.findByText("common.confirm"));

    await waitFor(() =>
      expect(disableUserMock).toHaveBeenCalledWith({
        userId: "u1",
        disableUserBody: { disabled: true },
      }),
    );
  });

  it("disables the disable action for a bootstrap admin that is still active", async () => {
    listUsersMock.mockResolvedValue(
      usersResponse([
        { user_id: "u3", username: "root", role_name: "admin", status: "active", is_bootstrap_admin: true },
      ]),
    );
    renderWithProviders(<UserManagement />);
    await screen.findByText("root");

    const disableButton = screen.getByText("admin.disable").closest("button");
    expect(disableButton).toBeDisabled();
  });

  it("searches users by username", async () => {
    renderWithProviders(<UserManagement />);
    await waitFor(() => expect(listUsersMock).toHaveBeenCalledTimes(1));

    const searchInput = screen.getByPlaceholderText("admin.searchUsername");
    fireEvent.change(searchInput, { target: { value: "alice" } });
    fireEvent.keyDown(searchInput, { key: "Enter", code: "Enter" });

    await waitFor(() =>
      expect(listUsersMock).toHaveBeenCalledWith({ page: 1, pageSize: 20, search: "alice" }),
    );
  });
});
