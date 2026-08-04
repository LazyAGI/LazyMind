import { describe, expect, it, vi } from "vitest";
import { useContext } from "react";
import { render } from "@testing-library/react";
import { SlotEditingContext } from "./slotEditingContext";

describe("SlotEditingContext", () => {
  it("provides no-op defaults when no provider is present", () => {
    let captured: ReturnType<typeof useContext<typeof SlotEditingContext>> | undefined;
    function Consumer() {
      captured = useContext(SlotEditingContext);
      return null;
    }
    render(<Consumer />);

    expect(() => captured!.setEditing("key", true)).not.toThrow();
    const unregister = captured!.registerFlush("key", async () => true);
    expect(typeof unregister).toBe("function");
    expect(() => unregister()).not.toThrow();
  });

  it("exposes custom values supplied through a Provider", () => {
    const setEditing = vi.fn();
    const registerFlush = vi.fn(() => () => {});
    let captured: ReturnType<typeof useContext<typeof SlotEditingContext>> | undefined;
    function Consumer() {
      captured = useContext(SlotEditingContext);
      return null;
    }
    render(
      <SlotEditingContext.Provider value={{ setEditing, registerFlush }}>
        <Consumer />
      </SlotEditingContext.Provider>,
    );

    captured!.setEditing("slot1", true);
    expect(setEditing).toHaveBeenCalledWith("slot1", true);

    const flushFn = vi.fn(async () => true);
    captured!.registerFlush("slot1", flushFn);
    expect(registerFlush).toHaveBeenCalledWith("slot1", flushFn);
  });
});
