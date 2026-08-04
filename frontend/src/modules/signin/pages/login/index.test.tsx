import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";
import Login from "./index";

const navigateMock = vi.hoisted(() => vi.fn());
const useLocationMock = vi.hoisted(() => vi.fn(() => ({ state: null })));

vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...actual,
    useNavigate: () => navigateMock,
    useLocation: () => useLocationMock(),
  };
});

const loginByPasswordMock = vi.hoisted(() => vi.fn());
const storeLoginSessionMock = vi.hoisted(() => vi.fn());
const unwrapLoginResponseMock = vi.hoisted(() => vi.fn());

vi.mock("@/modules/signin/utils/request", () => ({
  loginByPassword: loginByPasswordMock,
  storeLoginSession: storeLoginSessionMock,
  unwrapLoginResponse: unwrapLoginResponseMock,
}));

const getUserInfoMock = vi.hoisted(() => vi.fn());
vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getUserInfo: getUserInfoMock },
}));

vi.mock("@/runtime/features", () => ({
  runtimeFeatures: { hideRegister: false },
}));

describe("Login page", () => {
  beforeEach(() => {
    navigateMock.mockReset();
    useLocationMock.mockReturnValue({ state: null });
    getUserInfoMock.mockReset().mockReturnValue(null);
    loginByPasswordMock.mockReset();
    storeLoginSessionMock.mockReset().mockResolvedValue(null);
    unwrapLoginResponseMock.mockReset().mockReturnValue({ access_token: "tok" });
    localStorage.clear();
    sessionStorage.clear();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders the login form with username and password fields", () => {
    renderWithProviders(<Login />);
    expect(screen.getByText("auth.welcomeLogin")).toBeInTheDocument();
    expect(screen.getByText("auth.account")).toBeInTheDocument();
    expect(screen.getByText("auth.password")).toBeInTheDocument();
    expect(screen.getByText("auth.registerNow")).toBeInTheDocument();
  });

  it("redirects immediately when the user already has a valid session", () => {
    getUserInfoMock.mockReturnValue({ token: "existing-token" });
    renderWithProviders(<Login />);
    expect(navigateMock).toHaveBeenCalledWith("/agent/chat", { replace: true });
  });

  it("pre-fills the username field from router location state", () => {
    useLocationMock.mockReturnValue({ state: { username: "prefilled-user" } });
    renderWithProviders(<Login />);
    expect(screen.getByDisplayValue("prefilled-user")).toBeInTheDocument();
  });

  it("hides the register link when hideRegister runtime feature is enabled", async () => {
    vi.resetModules();
    vi.doMock("@/runtime/features", () => ({
      runtimeFeatures: { hideRegister: true },
    }));
    const { default: LoginWithHiddenRegister } = await import("./index");
    renderWithProviders(<LoginWithHiddenRegister />);
    expect(screen.queryByText("auth.registerNow")).not.toBeInTheDocument();
  });

  it("logs in successfully and navigates to the chat page", async () => {
    loginByPasswordMock.mockResolvedValue({ data: { access_token: "tok" } });
    renderWithProviders(<Login />);

    fireEvent.change(screen.getByPlaceholderText("auth.pleaseInputAccount"), {
      target: { value: "alice" },
    });
    fireEvent.change(screen.getByPlaceholderText("auth.pleaseInputPassword"), {
      target: { value: "secret" },
    });
    fireEvent.click(screen.getByText("auth.login"));

    await waitFor(() => expect(loginByPasswordMock).toHaveBeenCalledWith({
      username: "alice",
      password: "secret",
    }));
    await waitFor(() =>
      expect(navigateMock).toHaveBeenCalledWith("/agent/chat", { replace: true }),
    );
  });

  it("shows a generic error message when login fails without a server response", async () => {
    loginByPasswordMock.mockRejectedValue(new Error("network failure"));
    const { message } = await import("antd");
    const errorSpy = vi.spyOn(message, "error");

    renderWithProviders(<Login />);
    fireEvent.change(screen.getByPlaceholderText("auth.pleaseInputAccount"), {
      target: { value: "alice" },
    });
    fireEvent.change(screen.getByPlaceholderText("auth.pleaseInputPassword"), {
      target: { value: "wrong" },
    });
    fireEvent.click(screen.getByText("auth.login"));

    await waitFor(() => expect(errorSpy).toHaveBeenCalled());
  });
});
