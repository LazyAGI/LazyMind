import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { I18nextProvider } from "react-i18next";
import { testI18n } from "@/test/testUtils";
import AdminLayout from "./AdminLayout";

vi.mock("./index.scss", () => ({}));
vi.mock("@/public/Lazy.png", () => ({ default: "logo.png" }));

const getUserInfoMock = vi.hoisted(() => vi.fn());
vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: getUserInfoMock },
}));

function renderAdminLayout(initialPath = "/admin/groups") {
  return render(
    <I18nextProvider i18n={testI18n}>
      <MemoryRouter initialEntries={[initialPath]}>
        <Routes>
          <Route path="/admin" element={<AdminLayout />}>
            <Route path="groups" element={<div>groups-outlet</div>} />
            <Route path="users" element={<div>users-outlet</div>} />
          </Route>
          <Route path="/login" element={<div>login-page</div>} />
        </Routes>
      </MemoryRouter>
    </I18nextProvider>,
  );
}

describe("AdminLayout", () => {
  beforeEach(() => {
    getUserInfoMock.mockReset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("redirects to /login when there is no logged-in user", () => {
    getUserInfoMock.mockReturnValue(null);
    renderAdminLayout();
    expect(screen.getByText("login-page")).toBeInTheDocument();
  });

  it("renders the sider, menu, and outlet for a logged-in admin user", () => {
    getUserInfoMock.mockReturnValue({ token: "tok", username: "alice", role: "admin" });
    renderAdminLayout("/admin/groups");
    expect(screen.getByText("groups-outlet")).toBeInTheDocument();
    expect(screen.getByText("layout.userManagement")).toBeInTheDocument();
    expect(screen.getByText("layout.groupManagement")).toBeInTheDocument();
    expect(screen.getByText("alice")).toBeInTheDocument();
  });

  it("hides the user management menu item for non-admin users", () => {
    getUserInfoMock.mockReturnValue({ token: "tok", username: "bob", role: "user" });
    renderAdminLayout("/admin/groups");
    expect(screen.queryByText("layout.userManagement")).not.toBeInTheDocument();
    expect(screen.getByText("layout.groupManagement")).toBeInTheDocument();
  });

  it("redirects non-admin users away from /admin/users to /admin/groups", () => {
    getUserInfoMock.mockReturnValue({ token: "tok", username: "bob", role: "user" });
    renderAdminLayout("/admin/users");
    expect(screen.getByText("groups-outlet")).toBeInTheDocument();
  });

  it("allows admin users to access /admin/users", () => {
    getUserInfoMock.mockReturnValue({ token: "tok", username: "alice", role: "admin" });
    renderAdminLayout("/admin/users");
    expect(screen.getByText("users-outlet")).toBeInTheDocument();
  });
});
