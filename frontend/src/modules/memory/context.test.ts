import { describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import { useMemoryManagementOutletContext } from "./context";

const mockOutletContext = vi.fn();

vi.mock("react-router-dom", () => ({
  useOutletContext: () => mockOutletContext(),
}));

describe("useMemoryManagementOutletContext", () => {
  it("returns the value provided by the outlet context", () => {
    mockOutletContext.mockReturnValue({ activeTab: "skills" });

    const { result } = renderHook(() => useMemoryManagementOutletContext());

    expect(result.current).toEqual({ activeTab: "skills" });
  });

  it("returns undefined when no outlet context is provided", () => {
    mockOutletContext.mockReturnValue(undefined);

    const { result } = renderHook(() => useMemoryManagementOutletContext());

    expect(result.current).toBeUndefined();
  });
});
