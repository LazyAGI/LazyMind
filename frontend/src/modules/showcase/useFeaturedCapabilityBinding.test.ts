import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { enableBuiltinSkill } from "@/modules/memory/skillApi";
import { useFeaturedCapabilityBinding } from "./useFeaturedCapabilityBinding";

vi.mock("@/modules/memory/skillApi", () => ({ enableBuiltinSkill: vi.fn() }));

const enableBuiltinSkillMock = vi.mocked(enableBuiltinSkill);

describe("useFeaturedCapabilityBinding", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.spyOn(console, "error").mockImplementation(() => undefined);
  });
  afterEach(() => vi.restoreAllMocks());

  it("installs and binds a featured Skill", async () => {
    enableBuiltinSkillMock.mockResolvedValue({ skillId: "skill-1", name: "Advisor" } as never);
    const { result } = renderHook(() => useFeaturedCapabilityBinding({
      type: "work",
      title: "Advisor",
      builtin_skill_uid: "bsk-advisor",
    }));

    expect(result.current.status).toBe("preparing");
    await waitFor(() => expect(result.current.status).toBe("ready"));
    expect(result.current.mentions).toEqual([expect.objectContaining({
      type: "skill",
      resource_id: "skill-1",
      display_name: "Advisor",
    })]);
  });

  it("binds a featured Workflow without installing a Skill", async () => {
    const { result } = renderHook(() => useFeaturedCapabilityBinding({
      type: "workflow",
      title: "Runtime self-test",
      workflow_ref: "builtin:test-workflow",
    }));

    await waitFor(() => expect(result.current.status).toBe("ready"));
    expect(enableBuiltinSkillMock).not.toHaveBeenCalled();
    expect(result.current.mentions).toEqual([{
      mention_id: "featured-workflow:builtin:test-workflow",
      type: "workflow",
      resource_id: "builtin:test-workflow",
      display_name: "Runtime self-test",
    }]);
  });

  it("retries a failed install and clears when the binding is removed", async () => {
    enableBuiltinSkillMock
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce({ skillId: "skill-2", name: "Advisor" } as never);
    const { rerender, result } = renderHook(
      ({ uid }) => useFeaturedCapabilityBinding(uid ? {
        type: "work",
        title: "Advisor",
        builtin_skill_uid: uid,
      } : null),
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
