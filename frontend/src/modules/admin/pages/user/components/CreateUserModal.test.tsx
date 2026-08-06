import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";
import CreateUserModal from "./CreateUserModal";

const createUserMock = vi.hoisted(() => vi.fn());
const setUserRoleMock = vi.hoisted(() => vi.fn());
const listRolesMock = vi.hoisted(() => vi.fn());
const createUserApiMock = vi.hoisted(() => vi.fn());
const createRoleApiMock = vi.hoisted(() => vi.fn());

vi.mock("@/modules/signin/utils/request", () => ({
  createUserApi: createUserApiMock,
  createRoleApi: createRoleApiMock,
}));

describe("CreateUserModal", () => {
  beforeEach(() => {
    createUserMock.mockReset().mockResolvedValue({ data: {} });
    setUserRoleMock.mockReset().mockResolvedValue({ data: {} });
    listRolesMock.mockReset().mockResolvedValue({
      data: { data: [{ id: "role-user", name: "user" }, { id: "role-admin", name: "admin" }] },
    });
    createUserApiMock.mockReset().mockReturnValue({
      createUserApiAuthserviceUserPost: createUserMock,
      setUserRoleApiAuthserviceUserUserIdPatch: setUserRoleMock,
    });
    createRoleApiMock.mockReset().mockReturnValue({
      listRolesApiAuthserviceRoleGet: listRolesMock,
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("fetches the role list when opened and shows create-only fields", async () => {
    renderWithProviders(<CreateUserModal visible onCancel={vi.fn()} onSuccess={vi.fn()} />);
    await waitFor(() => expect(listRolesMock).toHaveBeenCalled());
    expect(screen.getByPlaceholderText("auth.pleaseInputPasswordSet")).toBeInTheDocument();
  });

  it("creates a new user with the entered fields and default role", async () => {
    const onSuccess = vi.fn();
    renderWithProviders(<CreateUserModal visible onCancel={vi.fn()} onSuccess={onSuccess} />);
    await waitFor(() => expect(listRolesMock).toHaveBeenCalled());

    fireEvent.change(screen.getByPlaceholderText("admin.enterUsernameWithMax"), {
      target: { value: "newuser" },
    });
    fireEvent.change(screen.getByPlaceholderText("auth.pleaseInputPasswordSet"), {
      target: { value: "Secret123." },
    });
    fireEvent.change(screen.getByPlaceholderText("admin.confirmPasswordWithMax"), {
      target: { value: "Secret123." },
    });
    fireEvent.click(screen.getByRole("button", { name: "OK" }));

    await waitFor(() =>
      expect(createUserMock).toHaveBeenCalledWith({
        createUserBody: {
          username: "newuser",
          password: "Secret123.",
          role_id: "role-user",
        },
      }),
    );
    await waitFor(() => expect(onSuccess).toHaveBeenCalled());
  });

  it("prefills fields and updates the role when editing an existing user", async () => {
    const onSuccess = vi.fn();
    renderWithProviders(
      <CreateUserModal
        visible
        editingUser={{ user_id: "u1", username: "alice", role_id: "role-user" } as any}
        onCancel={vi.fn()}
        onSuccess={onSuccess}
      />,
    );
    await waitFor(() => expect(listRolesMock).toHaveBeenCalled());
    expect(screen.getByDisplayValue("alice")).toBeInTheDocument();
    // Editing an existing user should not show password fields.
    expect(screen.queryByPlaceholderText("auth.pleaseInputPasswordSet")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "OK" }));

    await waitFor(() =>
      expect(setUserRoleMock).toHaveBeenCalledWith({
        userId: "u1",
        userRoleBody: { role_id: "role-user" },
      }),
    );
    await waitFor(() => expect(onSuccess).toHaveBeenCalled());
  });

  it("disables the OK button and the role select when editing a bootstrap admin", async () => {
    renderWithProviders(
      <CreateUserModal
        visible
        editingUser={{ user_id: "u1", username: "root", role_id: "role-admin", is_bootstrap_admin: true } as any}
        onCancel={vi.fn()}
        onSuccess={vi.fn()}
      />,
    );
    await waitFor(() => expect(listRolesMock).toHaveBeenCalled());

    expect(screen.getByRole("button", { name: "OK" })).toBeDisabled();
    expect(setUserRoleMock).not.toHaveBeenCalled();
  });
});
