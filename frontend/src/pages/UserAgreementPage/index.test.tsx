import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { I18nextProvider } from "react-i18next";
import { testI18n } from "@/test/testUtils";
import UserAgreementPage from "./index";

vi.mock("./index.scss", () => ({}));
vi.mock("@/public/Lazy.png", () => ({ default: "logo.png" }));

vi.mock("@/legal/LegalLanguageToggle", () => ({
  default: () => <div data-testid="language-toggle" />,
}));

const getUserAgreementMarkdownMock = vi.hoisted(() => vi.fn());
vi.mock("@/legal/agreementContent", () => ({
  getUserAgreementMarkdown: getUserAgreementMarkdownMock,
}));

const markUserAgreementReadMock = vi.hoisted(() => vi.fn());
vi.mock("@/legal/consent", () => ({
  markUserAgreementRead: markUserAgreementReadMock,
  USER_AGREEMENT_VERSION: "v2",
}));

const navigateMock = vi.hoisted(() => vi.fn());
vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return { ...actual, useNavigate: () => navigateMock };
});

function renderPage(fromState?: string) {
  return render(
    <I18nextProvider i18n={testI18n}>
      <MemoryRouter
        initialEntries={[
          { pathname: "/legal/user-agreement", state: fromState ? { from: fromState } : undefined },
        ]}
      >
        <Routes>
          <Route path="/legal/user-agreement" element={<UserAgreementPage />} />
        </Routes>
      </MemoryRouter>
    </I18nextProvider>,
  );
}

describe("UserAgreementPage", () => {
  beforeEach(() => {
    navigateMock.mockReset();
    getUserAgreementMarkdownMock.mockReset().mockReturnValue("# Agreement\n\nSome content.");
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders the title, version, and markdown content", () => {
    renderPage();
    expect(screen.getByText("legal.consentTitle")).toBeInTheDocument();
    expect(screen.getByText("legal.consentVersion")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Agreement" })).toBeInTheDocument();
    expect(screen.getByText("Some content.")).toBeInTheDocument();
  });

  it("marks the agreement read and navigates to the default path when confirmed", () => {
    renderPage();
    fireEvent.click(screen.getByText("legal.detailsReadAndReturn"));
    expect(markUserAgreementReadMock).toHaveBeenCalledTimes(1);
    expect(navigateMock).toHaveBeenCalledWith("/", { replace: true });
  });

  it("navigates back to a valid internal 'from' path when the back button is clicked", () => {
    renderPage("/agent/chat/home");
    fireEvent.click(screen.getByText("legal.detailsBack"));
    expect(navigateMock).toHaveBeenCalledWith("/agent/chat/home", { replace: true });
  });

  it("falls back to '/' when the 'from' path is a protocol-relative URL", () => {
    renderPage("//evil.example.com");
    fireEvent.click(screen.getByText("legal.detailsBack"));
    expect(navigateMock).toHaveBeenCalledWith("/", { replace: true });
  });

  it("falls back to '/' when the 'from' path points back to the agreement page itself", () => {
    renderPage("/legal/user-agreement");
    fireEvent.click(screen.getByText("legal.detailsBack"));
    expect(navigateMock).toHaveBeenCalledWith("/", { replace: true });
  });
});
