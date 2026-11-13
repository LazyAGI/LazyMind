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

  it("opens paper collections without preparing a Skill or Workflow", () => {
    const { result } = renderHook(() => useFeaturedCapabilityBinding({ type: "page", title: "论文阅读", page_action: "open_paper_library" }));
    expect(result.current.status).toBe("ready");
    expect(result.current.mentions).toEqual([]);
    expect(enableBuiltinSkillMock).not.toHaveBeenCalled();
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

  it("keeps a failed page idle but starts a new attempt after a page remount", async () => {
    enableBuiltinSkillMock.mockRejectedValue(new Error("package verification failed"));
    const capability = { type: "chat" as const, title: "Diagram", builtin_skill_uid: "bsk-diagram" };
    const first = renderHook(() => useFeaturedCapabilityBinding(capability));
    await waitFor(() => expect(first.result.current.status).toBe("failed"));
    first.rerender();
    expect(first.result.current.status).toBe("failed");
    expect(enableBuiltinSkillMock).toHaveBeenCalledTimes(1);
    first.unmount();

    enableBuiltinSkillMock.mockResolvedValueOnce({ skillId: "diagram-1", name: "Diagram" } as never);
    const refreshed = renderHook(() => useFeaturedCapabilityBinding(capability));
    await waitFor(() => expect(refreshed.result.current.status).toBe("ready"));
    expect(enableBuiltinSkillMock).toHaveBeenCalledTimes(2);
  });
});
