import { describe, expect, it } from "vitest";

import {
  filterOfficialKnowledgeBases,
  mergeKnowledgeMarketItems,
} from "./knowledgeSquareData";

const catalogItem = {
  category: "industry",
  created_at: "2026-01-01T00:00:00Z",
  data_source: "official",
  description: "Legal reference",
  domain: "Law",
  icon: "⚖️",
  id: "law",
  name: "Legal Knowledge",
  online_access_url: "https://example.com/query",
  sort_order: 1,
  tags: ["regulation"],
  updated_at: "2026-02-01T00:00:00Z",
  version: "v2.0.0",
};

describe("knowledgeSquareData", () => {
  it("merges catalog metadata with the current user's install", () => {
    const [item] = mergeKnowledgeMarketItems([catalogItem], [
      {
        active: true,
        dataset_id: "dataset-1",
        domain: "Law",
        icon: "⚖️",
        install_state: "parsing",
        installed_version: "v2.0.0",
        market_item_id: "law",
        name: "Legal Knowledge",
        updated_at: "2026-02-02T00:00:00Z",
      },
    ]);

    expect(item).toMatchObject({
      id: "law",
      installed: true,
      active: true,
      datasetId: "dataset-1",
      installedVersion: "v2.0.0",
      latestVersion: "v2.0.0",
      onlineAccessUrl: "https://example.com/query",
    });
  });

  it("filters by category, domain, install status and keyword", () => {
    const installed = mergeKnowledgeMarketItems([catalogItem], [
      {
        active: false,
        dataset_id: "dataset-1",
        domain: "Law",
        icon: "⚖️",
        install_state: "ready",
        installed_version: "v1.0.0",
        market_item_id: "law",
        name: "Legal Knowledge",
        installed_at: "2026-01-15T00:00:00Z",
        updated_at: "2026-02-02T00:00:00Z",
      },
    ]);

    expect(installed[0].updateAvailable).toBe(true);

    expect(
      filterOfficialKnowledgeBases({
        items: installed,
        type: "industry",
        domain: "Law",
        status: "installed",
        keyword: "regulation",
      }),
    ).toHaveLength(1);
    expect(
      filterOfficialKnowledgeBases({
        items: installed,
        type: "industry",
        domain: "",
        status: "updatable",
        keyword: "",
      }),
    ).toHaveLength(1);
    expect(
      filterOfficialKnowledgeBases({
        items: installed,
        type: "evaluation",
        domain: "",
        status: "all",
        keyword: "",
      }),
    ).toHaveLength(0);
  });

  it("does not mark an installed item as updatable when the install is newer", () => {
    const [installed] = mergeKnowledgeMarketItems([catalogItem], [
      {
        active: false,
        dataset_id: "dataset-1",
        domain: "Law",
        icon: "⚖️",
        install_state: "ready",
        installed_version: "v2.0.0",
        installed_at: "2026-02-02T00:00:00Z",
        market_item_id: "law",
        name: "Legal Knowledge",
        updated_at: "2026-02-02T00:00:00Z",
      },
    ]);

    expect(installed.updateAvailable).toBe(false);
  });

  it("infers the current version for a legacy install created after the catalog update", () => {
    const [installed] = mergeKnowledgeMarketItems([catalogItem], [
      {
        active: false,
        dataset_id: "dataset-1",
        domain: "Law",
        icon: "⚖️",
        install_state: "ready",
        installed_version: "",
        installed_at: "2026-02-02T00:00:00Z",
        market_item_id: "law",
        name: "Legal Knowledge",
        updated_at: "2026-02-02T00:00:00Z",
      },
    ]);

    expect(installed).toMatchObject({
      installedVersion: "v2.0.0",
      latestVersion: "v2.0.0",
      updateAvailable: false,
    });
  });
});
