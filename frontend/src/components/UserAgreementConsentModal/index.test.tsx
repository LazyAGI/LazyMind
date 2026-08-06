import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";
import UserAgreementConsentModal, {
  useUserAgreementConsentGate,
} from "./index";

vi.mock("./index.scss", () => ({}));
vi.mock("@/legal/LegalLanguageToggle", () => ({
  default: () => <div>language-toggle</div>,
}));
vi.mock("@/legal/consent", () => ({
  USER_AGREEMENT_VERSION: "V0.2",
  consumeUserAgreementReadFlag: vi.fn(() => false),
  persistUserAgreementAccepted: vi.fn(),
  syncUserAgreementFromServer: vi.fn(),
}));

import {
  consumeUserAgreementReadFlag,
  persistUserAgreementAccepted,
  syncUserAgreementFromServer,
} from "@/legal/consent";

const mockedConsumeFlag = consumeUserAgreementReadFlag as unknown as ReturnType<typeof vi.fn>;
const mockedPersist = persistUserAgreementAccepted as unknown as ReturnType<typeof vi.fn>;
const mockedSync = syncUserAgreementFromServer as unknown as ReturnType<typeof vi.fn>;

describe("UserAgreementConsentModal", () => {
  beforeEach(() => {
    mockedConsumeFlag.mockReturnValue(false);
    mockedPersist.mockResolvedValue(undefined);
    mockedSync.mockResolvedValue(true);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders the checkbox unchecked by default and disables the agree button", () => {
    renderWithProviders(
      <UserAgreementConsentModal open onAccepted={vi.fn()} />,
    );
    expect(screen.getByRole("checkbox")).not.toBeChecked();
    expect(screen.getByText("legal.consentAgreeAndContinue").closest("button")).toBeDisabled();
  });

  it("pre-checks the checkbox when the read flag was already consumed", () => {
    mockedConsumeFlag.mockReturnValue(true);
    renderWithProviders(
      <UserAgreementConsentModal open onAccepted={vi.fn()} />,
    );
    expect(screen.getByRole("checkbox")).toBeChecked();
  });

  it("enables the agree button once the checkbox is checked, and persists acceptance on click", async () => {
    const onAccepted = vi.fn();
    renderWithProviders(
      <UserAgreementConsentModal open onAccepted={onAccepted} />,
    );

    fireEvent.click(screen.getByRole("checkbox"));
    const agreeButton = screen.getByText("legal.consentAgreeAndContinue").closest("button")!;
    expect(agreeButton).not.toBeDisabled();

    await act(async () => {
      fireEvent.click(agreeButton);
    });

    expect(mockedPersist).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(onAccepted).toHaveBeenCalledTimes(1));
  });

  it("shows an error message and does not call onAccepted when persisting fails", async () => {
    mockedPersist.mockRejectedValue(new Error("network error"));
    vi.spyOn(console, "error").mockImplementation(() => {});
    const onAccepted = vi.fn();
    renderWithProviders(
      <UserAgreementConsentModal open onAccepted={onAccepted} />,
    );

    fireEvent.click(screen.getByRole("checkbox"));
    await act(async () => {
      fireEvent.click(screen.getByText("legal.consentAgreeAndContinue").closest("button")!);
    });

    expect(onAccepted).not.toHaveBeenCalled();
  });
});

describe("useUserAgreementConsentGate", () => {
  it("skips the sync call and marks accepted immediately when disabled", async () => {
    const { renderHook } = await import("@testing-library/react");
    const { result } = renderHook(() => useUserAgreementConsentGate(false));
    expect(result.current.loading).toBe(false);
    expect(result.current.needsConsent).toBe(false);
    expect(mockedSync).not.toHaveBeenCalled();
  });

  it("requires consent when the server sync reports not accepted", async () => {
    mockedSync.mockResolvedValue(false);
    const { renderHook } = await import("@testing-library/react");
    const { result } = renderHook(() => useUserAgreementConsentGate(true));

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.needsConsent).toBe(true);
  });

  it("markAccepted flips needsConsent to false", async () => {
    mockedSync.mockResolvedValue(false);
    const { renderHook } = await import("@testing-library/react");
    const { result } = renderHook(() => useUserAgreementConsentGate(true));
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => {
      result.current.markAccepted();
    });
    expect(result.current.needsConsent).toBe(false);
  });

  // Fails closed: a failed check must not fall through to the normal layout as
  // if consent had been granted. MainLayout blocks on `checkFailed`.
  it("reports checkFailed without requiring consent when the sync throws", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    mockedSync.mockRejectedValue(new Error("network error"));
    const { renderHook } = await import("@testing-library/react");
    const { result } = renderHook(() => useUserAgreementConsentGate(true));

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.checkFailed).toBe(true);
    expect(result.current.needsConsent).toBe(false);
  });

  it("retryCheck re-runs the sync and clears checkFailed on success", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    mockedSync.mockRejectedValueOnce(new Error("network error")).mockResolvedValue(true);
    const { renderHook } = await import("@testing-library/react");
    const { result } = renderHook(() => useUserAgreementConsentGate(true));
    await waitFor(() => expect(result.current.checkFailed).toBe(true));

    act(() => {
      result.current.retryCheck();
    });

    await waitFor(() => expect(result.current.checkFailed).toBe(false));
    expect(result.current.needsConsent).toBe(false);
  });
});
