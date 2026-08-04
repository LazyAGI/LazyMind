import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { I18nextProvider } from "react-i18next";
import { testI18n } from "@/test/testUtils";
import MemoryManagement from "./index";
import { useMemoryManagementOutletContext } from "./context";

vi.mock("./skillApi", () => ({
  listSkillAssetsPage: vi.fn().mockResolvedValue({
    records: [],
    total: 0,
    page: 1,
    pageSize: 6,
  }),
  listSkillCategories: vi.fn().mockResolvedValue([]),
  listSkillTags: vi.fn().mockResolvedValue([]),
  getSkillReviewSummary: vi.fn().mockResolvedValue({
    pendingCount: 0,
    runningTask: null,
  }),
  listSkillReviewTasks: vi.fn().mockResolvedValue({ records: [], total: 0 }),
  listIncomingSkillShares: vi.fn().mockResolvedValue([]),
  listOutgoingSkillShares: vi.fn().mockResolvedValue([]),
  listSkillShareTargets: vi.fn().mockResolvedValue([]),
  getSkillAssetDetail: vi.fn().mockResolvedValue(null),
  createSkillAsset: vi.fn(),
  patchSkillAsset: vi.fn(),
  removeSkillAsset: vi.fn(),
  enableBuiltinSkill: vi.fn(),
  generateSkillDraft: vi.fn(),
  previewSkillDraft: vi.fn(),
  confirmSkillDraft: vi.fn(),
  discardSkillDraft: vi.fn(),
  shareSkillAsset: vi.fn(),
  acceptSkillShare: vi.fn(),
  rejectSkillShare: vi.fn(),
  runSkillReview: vi.fn(),
  buildSkillUpdatePayload: (payload: unknown) => payload,
}));

vi.mock("./preferenceApi", () => ({
  listPreferenceAssets: vi.fn().mockResolvedValue([]),
  getPersonalizationSetting: vi.fn().mockResolvedValue(true),
  updatePersonalizationSetting: vi.fn(),
  approveEvolutionSuggestion: vi.fn(),
  batchApproveEvolutionSuggestions: vi.fn(),
  batchRejectEvolutionSuggestions: vi.fn(),
  rejectEvolutionSuggestion: vi.fn(),
  confirmManagedPreferenceDraft: vi.fn(),
  discardManagedPreferenceDraft: vi.fn(),
  generateManagedPreferenceDraft: vi.fn(),
  previewManagedPreferenceDraft: vi.fn().mockResolvedValue({
    currentContent: "",
    diff: "",
    draftContent: "",
    draftSourceVersion: 0,
    draftStatus: "none",
    draftVersion: 1,
    pendingCount: 0,
    acceptedCount: 0,
    rejectedCount: 0,
  }),
  resolveManagedPreferenceDraftKind: (resourceType?: string) =>
    resourceType?.includes("memory") ? "memory" : "user-preference",
  reviewManagedPreferenceDraftHunks: vi.fn(),
  undoManagedPreferenceDraftReview: vi.fn(),
  patchPersonalResourceMetadata: vi.fn(),
  readPersonalResourceFile: vi.fn().mockResolvedValue({
    content: "",
    draftVersion: 1,
    draftStatus: "none",
    revisionId: "rev-1",
    revisionNo: 1,
    binary: false,
  }),
  resolvePersonalResourceApiType: (resourceType?: string) =>
    resourceType?.includes("memory") ? "memory" : "user_preference",
  saveAndCommitPersonalResourceContent: vi.fn(),
}));

vi.mock("./glossaryApi", () => ({
  addGlossaryConflictToGroups: vi.fn(),
  batchRemoveGlossaryAssets: vi.fn(),
  checkGlossaryWordsExist: vi.fn(),
  createGlossaryGroupFromConflict: vi.fn(),
  createGlossaryAsset: vi.fn(),
  getGlossaryAssetDetail: vi.fn(),
  listGlossaryAssetsPage: vi.fn().mockResolvedValue({
    records: [],
    total: 0,
    nextPageToken: "",
  }),
  listGlossaryConflicts: vi.fn().mockResolvedValue([]),
  mergeGlossaryAssets: vi.fn(),
  mergeGlossaryConflictAndAddWord: vi.fn(),
  removeGlossaryConflict: vi.fn(),
  removeGlossaryAsset: vi.fn(),
  updateGlossaryAsset: vi.fn(),
}));

vi.mock("./skillPackage", () => ({
  buildSkillZipBlob: vi.fn(),
}));

vi.mock("./skillUpload", () => ({
  uploadSkillTempFile: vi.fn(),
}));

vi.mock("./components/skillPackage/skillDiffUtils", () => ({
  mapDiffEntryLines: vi.fn().mockReturnValue([]),
}));

vi.mock("@/modules/signin/utils/request", () => ({
  createGroupApi: () => ({
    listGroupsApiAuthserviceGroupGet: vi
      .fn()
      .mockResolvedValue({ data: { groups: [] } }),
  }),
  createUserApi: () => ({
    listUsersApiAuthserviceUserGet: vi
      .fn()
      .mockResolvedValue({ data: { users: [] } }),
  }),
}));

function OutletProbe() {
  const context = useMemoryManagementOutletContext();
  return (
    <div data-testid="outlet-probe">
      <span data-testid="active-tab">{context.activeTab}</span>
      <span data-testid="skill-loading">{String(context.skillLoading)}</span>
    </div>
  );
}

function renderMemoryManagement(initialRoute = "/memory-management/skills") {
  return render(
    <I18nextProvider i18n={testI18n}>
      <MemoryRouter initialEntries={[initialRoute]}>
        <Routes>
          <Route path="/memory-management" element={<MemoryManagement />}>
            <Route path=":tab" element={<OutletProbe />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </I18nextProvider>,
  );
}

describe("MemoryManagement", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders the outlet with a context and does not crash", async () => {
    renderMemoryManagement();
    await waitFor(() => {
      expect(screen.getByTestId("outlet-probe")).toBeInTheDocument();
    });
    expect(screen.getByTestId("active-tab")).toHaveTextContent("skills");
  });

  it("applies the review-mode class only for review routes", async () => {
    const { container } = renderMemoryManagement();
    await waitFor(() => {
      expect(screen.getByTestId("outlet-probe")).toBeInTheDocument();
    });
    expect(
      container.querySelector(".memory-page.is-review-mode"),
    ).not.toBeInTheDocument();
  });

  it("resolves the active tab from the experience route", async () => {
    renderMemoryManagement("/memory-management/experience");
    await waitFor(() => {
      expect(screen.getByTestId("active-tab")).toHaveTextContent("experience");
    });
  });

  it("resolves the active tab from the glossary route", async () => {
    renderMemoryManagement("/memory-management/glossary");
    await waitFor(() => {
      expect(screen.getByTestId("active-tab")).toHaveTextContent("glossary");
    });
  });
});
