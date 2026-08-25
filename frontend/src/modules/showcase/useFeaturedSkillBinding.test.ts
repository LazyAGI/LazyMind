import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { enableBuiltinSkill } from "@/modules/memory/skillApi";
import { useFeaturedSkillBinding } from "./useFeaturedSkillBinding";

vi.mock("@/modules/memory/skillApi", () => ({ enableBuiltinSkill: vi.fn() }));

const enableBuiltinSkillMock = vi.mocked(enableBuiltinSkill);

describe("useFeaturedSkillBinding", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.spyOn(console, "error").mockImplementation(() => undefined);
  });
  afterEach(() => vi.restoreAllMocks());

  it("installs and binds a featured Skill", async () => {
    enableBuiltinSkillMock.mockResolvedValue({ skillId: "skill-1", name: "Advisor" } as never);
    const { result } = renderHook(() => useFeaturedSkillBinding("bsk-advisor"));

    expect(result.current.status).toBe("preparing");
    await waitFor(() => expect(result.current.status).toBe("ready"));
    expect(result.current.mentions).toEqual([expect.objectContaining({
      type: "skill",
      resource_id: "skill-1",
      display_name: "Advisor",
    })]);
  });

  it("retries a failed install and clears when the binding is removed", async () => {
    enableBuiltinSkillMock
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce({ skillId: "skill-2", name: "Advisor" } as never);
    const { rerender, result } = renderHook(
      ({ uid }) => useFeaturedSkillBinding(uid),
      { initialProps: { uid: "bsk-advisor" as string | undefined } },
    );

    await waitFor(() => expect(result.current.status).toBe("failed"));
    act(() => result.current.retry());
    await waitFor(() => expect(result.current.status).toBe("ready"));
    expect(enableBuiltinSkillMock).toHaveBeenCalledTimes(2);

    rerender({ uid: undefined });
    await waitFor(() => expect(result.current.status).toBe("idle"));
    expect(result.current.mentions).toEqual([]);
  });
});
