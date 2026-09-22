import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useCredentialRestorePanel } from "./useCredentialRestorePanel";
import type { CredentialRestoreStatus } from "../credentialRestoreModel";

vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
afterEach(cleanup);

describe("credential restore interaction", () => {
  const status: CredentialRestoreStatus = { available: true, requiresExplicitAction: true, backupCount: 2, status: "idle" };

  it("starts only on confirmation and uses the selected trust mode", () => {
    const onStart = vi.fn();
    const { result } = renderHook(() => useCredentialRestorePanel({ status, onStart }));
    act(() => { result.current.openConfirmation(); result.current.setMode("temporary") });
    expect(onStart).not.toHaveBeenCalled();
    act(() => result.current.closeConfirmation());
    expect(onStart).not.toHaveBeenCalled();
    act(() => result.current.openConfirmation());
    act(() => result.current.confirmRestore());
    expect(onStart).toHaveBeenCalledOnce();
    expect(onStart).toHaveBeenCalledWith("temporary");
    expect(result.current.confirming).toBe(false);
  });

  it("keeps the selected trust mode for both explicit conflict resolutions", () => {
    const onStart = vi.fn();
    const { result, rerender } = renderHook(({ value }) => useCredentialRestorePanel({ status: value, onStart }), { initialProps: { value: status } });
    act(() => result.current.setMode("temporary"));
    rerender({ value: { ...status, status: "conflict" } });
    expect(onStart).not.toHaveBeenCalled();
    act(() => result.current.saveCopy());
    act(() => result.current.replaceLocal());
    expect(onStart.mock.calls).toEqual([["temporary", "save_copy"], ["temporary", "replace_local"]]);
  });
});
