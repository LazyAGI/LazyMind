import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { useSupplierOrder } from "./useSupplierOrder";

const setUser = (userId: string, tenantId: string) => localStorage.setItem("lazymind:user", JSON.stringify({ userId, tenantId }));
const suppliers = [
  { id: "a", name: "Claude" },
  { id: "b", name: "DeepSeek" },
  { id: "c", name: "OpenAI" },
];

describe("saved supplier sidebar order", () => {
  beforeEach(() => {
    localStorage.clear();
    setUser("user-a", "tenant-a");
  });

  it("retains a drag order after the settings page remounts", () => {
    const first = renderHook(() => useSupplierOrder(suppliers));
    act(() => first.result.current.reorder(["a", "c"], "c", "a"));
    expect(first.result.current.providers.map((item) => item.id)).toEqual(["c", "b", "a"]);
    first.unmount();
    const second = renderHook(() => useSupplierOrder(suppliers));
    expect(second.result.current.providers.map((item) => item.id)).toEqual(["c", "b", "a"]);
  });

  it("isolates saved order by user and tenant and resets immediately when the account changes", () => {
    const view = renderHook(() => useSupplierOrder(suppliers));
    act(() => view.result.current.reorder(["a", "b", "c"], "c", "a"));
    act(() => {
      setUser("user-a", "tenant-b");
      window.dispatchEvent(new Event("lazymind:user-change"));
    });
    expect(view.result.current.providers.map((item) => item.id)).toEqual(["a", "b", "c"]);
    act(() => {
      setUser("user-b", "tenant-a");
      window.dispatchEvent(new Event("lazymind:user-change"));
    });
    expect(view.result.current.providers.map((item) => item.id)).toEqual(["a", "b", "c"]);
    act(() => {
      setUser("user-a", "tenant-a");
      window.dispatchEvent(new Event("lazymind:user-change"));
    });
    expect(view.result.current.providers.map((item) => item.id)).toEqual(["c", "a", "b"]);
  });
});
