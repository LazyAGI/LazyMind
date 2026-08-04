import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";
import Register from "./index";

const navigateMock = vi.hoisted(() => vi.fn());
vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...actual,
    useNavigate: () => navigateMock,
  };
});

const registerByPasswordMock = vi.hoisted(() => vi.fn());
vi.mock("@/modules/signin/utils/request", () => ({
  registerByPassword: registerByPasswordMock,
}));

describe("Register page", () => {
  beforeEach(() => {
    navigateMock.mockReset();
    registerByPasswordMock.mockReset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders the registration form fields", () => {
    renderWithProviders(<Register />);
    expect(screen.getByText("auth.newUserRegister")).toBeInTheDocument();
    expect(screen.getByText("auth.username")).toBeInTheDocument();
    expect(screen.getByText("auth.setPassword")).toBeInTheDocument();
    expect(screen.getByText("auth.confirmPassword")).toBeInTheDocument();
  });

  it("registers successfully and navigates to the login page with the username prefilled", async () => {
    registerByPasswordMock.mockResolvedValue({ data: {} });
    const { message } = await import("antd");
    const successSpy = vi.spyOn(message, "success");

    renderWithProviders(<Register />);

           fireEvent.change(screen.getByPlaceholderText("auth.pleaseInputUsername"), {
             target: { value: "bob12345" },
           });
           fireEvent.change(screen.getByPlaceholderText("auth.pleaseInputPasswordSet"), {
             target: { value: "Secret123." },
           });
           fireEvent.change(screen.getByPlaceholderText("auth.pleaseInputConfirmPassword"), {
             target: { value: "Secret123." },
           });
           fireEvent.click(screen.getByText("auth.register"));

           await waitFor(() =>
             expect(registerByPasswordMock).toHaveBeenCalledWith({
               username: "bob12345",
               password: "Secret123.",
               confirm_password: "Secret123.",
               email: undefined,
             }),
           );
    await waitFor(() => expect(successSpy).toHaveBeenCalled());
    await waitFor(() =>
      expect(navigateMock).toHaveBeenCalledWith("/login", {
        state: { username: "bob12345" },
      }),
    );
  });

  it("rejects mismatched password confirmation before calling the API", async () => {
    renderWithProviders(<Register />);

    fireEvent.change(screen.getByPlaceholderText("auth.pleaseInputUsername"), {
      target: { value: "bob12345" },
    });
    fireEvent.change(screen.getByPlaceholderText("auth.pleaseInputPasswordSet"), {
      target: { value: "Secret123" },
    });
    fireEvent.change(screen.getByPlaceholderText("auth.pleaseInputConfirmPassword"), {
      target: { value: "Mismatch123" },
    });
    fireEvent.click(screen.getByText("auth.register"));

    await waitFor(() => expect(screen.getByText("auth.passwordNotMatch")).toBeInTheDocument());
    expect(registerByPasswordMock).not.toHaveBeenCalled();
  });

  it("navigates back to the login page when the link is clicked", () => {
    renderWithProviders(<Register />);
    fireEvent.click(screen.getByText("auth.backToLogin"));
    expect(navigateMock).toHaveBeenCalledWith("/login");
  });
});
