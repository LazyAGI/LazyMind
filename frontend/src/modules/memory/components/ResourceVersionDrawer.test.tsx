import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import ResourceVersionDrawer from "./ResourceVersionDrawer";

vi.mock("@/components/request", () => ({
  getLocalizedErrorMessage: (error: unknown) => String((error as Error)?.message || error),
}));

const t = ((key: string, options?: Record<string, unknown>) =>
  options ? `${key}:${JSON.stringify(options)}` : key) as any;

const listSkillRevisions = vi.fn();
const getSkillRevisionFile = vi.fn();
const getSkillRevisionTree = vi.fn();
const compareSkillRevisionTreeDiff = vi.fn();
const compareSkillRevisionFileDiff = vi.fn();
const rollbackSkill = vi.fn();

vi.mock("../skillApi", () => ({
  listSkillRevisions: (...args: unknown[]) => listSkillRevisions(...args),
  getSkillRevisionFile: (...args: unknown[]) => getSkillRevisionFile(...args),
  getSkillRevisionTree: (...args: unknown[]) => getSkillRevisionTree(...args),
  compareSkillRevisionTreeDiff: (...args: unknown[]) => compareSkillRevisionTreeDiff(...args),
  compareSkillRevisionFileDiff: (...args: unknown[]) => compareSkillRevisionFileDiff(...args),
  rollbackSkill: (...args: unknown[]) => rollbackSkill(...args),
  RollbackConflictError: class RollbackConflictError extends Error {
    readonly isConflict = true;
  },
}));

const listPersonalResourceRevisions = vi.fn();
const getPersonalResourceRevision = vi.fn();
const rollbackPersonalResource = vi.fn();

vi.mock("../preferenceApi", () => ({
  listPersonalResourceRevisions: (...args: unknown[]) => listPersonalResourceRevisions(...args),
  getPersonalResourceRevision: (...args: unknown[]) => getPersonalResourceRevision(...args),
  rollbackPersonalResource: (...args: unknown[]) => rollbackPersonalResource(...args),
  RollbackConflictError: class RollbackConflictError extends Error {
    readonly isConflict = true;
  },
}));

describe("ResourceVersionDrawer", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("loads and lists personal resource revisions, then shows the head revision detail", async () => {
    listPersonalResourceRevisions.mockResolvedValue([
      {
        revisionId: "rev-2",
        revisionNo: 2,
        changeSource: "direct_save",
        createdAt: "2024-01-02",
        isHead: true,
      },
      {
        revisionId: "rev-1",
        revisionNo: 1,
        changeSource: "create",
        createdAt: "2024-01-01",
        isHead: false,
      },
    ]);
    getPersonalResourceRevision.mockImplementation(async (_type, revisionId) => ({
      revision: {} as never,
      content: revisionId === "rev-2" ? "new content" : "old content",
    }));

    render(
      <ResourceVersionDrawer
        open
        resourceId="res-1"
        resourceName="My Preference"
        resourceType="preference"
        t={t}
        onClose={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(document.querySelector(".memory-version-list-item.is-active")).not.toBeNull();
    });
    expect(
      document.querySelector(".memory-version-list-item.is-active"),
    ).toHaveTextContent("v2");
    expect(screen.getByText("v1")).toBeInTheDocument();
    expect(screen.getByText("My Preference")).toBeInTheDocument();

    await waitFor(() => {
      expect(getPersonalResourceRevision).toHaveBeenCalledWith("user_preference", "rev-2");
    });
  });

  it("shows an error message with retry when the revision list fails to load", async () => {
    listSkillRevisions.mockRejectedValue(new Error("list failed"));

    render(
      <ResourceVersionDrawer
        open
        resourceId="skill-1"
        resourceName="My Skill"
        resourceType="skill"
        t={t}
        onClose={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(screen.getByText("list failed")).toBeInTheDocument();
    });
  });

  it("shows an empty state when there are no revisions", async () => {
    listPersonalResourceRevisions.mockResolvedValue([]);

    render(
      <ResourceVersionDrawer
        open
        resourceId="res-1"
        resourceName="My Preference"
        resourceType="preference"
        t={t}
        onClose={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(screen.getByText("admin.memoryVersionEmpty")).toBeInTheDocument();
    });
  });

  it("does not fetch revisions when the drawer is closed", () => {
    render(
      <ResourceVersionDrawer
        open={false}
        resourceId="res-1"
        resourceName="My Preference"
        resourceType="preference"
        t={t}
        onClose={vi.fn()}
      />,
    );
    expect(listPersonalResourceRevisions).not.toHaveBeenCalled();
  });
});
