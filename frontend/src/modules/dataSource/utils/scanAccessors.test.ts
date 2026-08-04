import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("@/components/auth", () => ({
  AgentAppsAuth: {
    getUserInfo: vi.fn(),
  },
}));

vi.mock("@/components/request", () => ({
  localizeErrorCode: (code?: string, fallback = "") =>
    code ? `localized:${code}` : fallback,
}));

import { AgentAppsAuth } from "@/components/auth";
import {
  buildScanBindingTargetLabels,
  createScanRequestId,
  getBindingLastError,
  getBindingSchedule,
  getDocumentDisplayName,
  getDocumentLastUpdatedAt,
  getDocumentPath,
  getFeishuBindingFormTarget,
  getFirstScanBinding,
  getScanBindingAgentId,
  getScanBindingConnector,
  getScanBindingDisplayName,
  getScanBindingId,
  getScanBindingTarget,
  getScanBindingTreeKey,
  getScanSourceConfigVersion,
  getScanSourceDatasetId,
  getScanSourceId,
  getScanSourceName,
  getScanSourceUpdatedAt,
  getScanTenantId,
  getScanTreeNodePath,
  inferSourceKind,
} from "./scanAccessors";

describe("getScanTenantId", () => {
  afterEach(() => {
    vi.mocked(AgentAppsAuth.getUserInfo).mockReset();
  });

  it("prefers tenantId from stored user info", () => {
    vi.mocked(AgentAppsAuth.getUserInfo).mockReturnValue({ tenantId: "t-1" } as never);
    expect(getScanTenantId()).toBe("t-1");
  });

  it("falls back through tenant_id/tenantKey/tenant_key and finally the default", () => {
    vi.mocked(AgentAppsAuth.getUserInfo).mockReturnValue({ tenant_key: "t-2" } as never);
    expect(getScanTenantId()).toBe("t-2");
    vi.mocked(AgentAppsAuth.getUserInfo).mockReturnValue(null);
    expect(getScanTenantId()).toBe("tenant-demo");
  });
});

describe("createScanRequestId", () => {
  it("prefixes a UUID-based id", () => {
    const id = createScanRequestId("scan");
    expect(id.startsWith("scan-")).toBe(true);
    expect(id.length).toBeGreaterThan("scan-".length);
  });
});

describe("simple accessor getters", () => {
  it("getScanSourceId/Name/DatasetId prefer snake_case then camelCase", () => {
    expect(getScanSourceId({ source_id: "s1" } as never)).toBe("s1");
    expect(getScanSourceId({ id: "s2" } as never)).toBe("s2");
    expect(getScanSourceId(null)).toBe("");
    expect(getScanSourceName({ name: " My Source " } as never)).toBe("My Source");
    expect(getScanSourceDatasetId({ datasetId: "ds1" } as never)).toBe("ds1");
  });

  it("getScanSourceUpdatedAt falls back through updated/created", () => {
    expect(getScanSourceUpdatedAt({ created_at: "c1" } as never)).toBe("c1");
    expect(getScanSourceUpdatedAt({ updated_at: "u1", created_at: "c1" } as never)).toBe(
      "u1",
    );
  });

  it("getScanSourceConfigVersion parses numbers safely", () => {
    expect(getScanSourceConfigVersion({ config_version: 3 } as never)).toBe(3);
    expect(getScanSourceConfigVersion({} as never)).toBe(0);
    expect(getScanSourceConfigVersion({ config_version: "nope" } as never)).toBe(0);
  });

  it("getFirstScanBinding returns the first binding or null", () => {
    expect(getFirstScanBinding([{ binding_id: "b1" } as never])).toEqual({
      binding_id: "b1",
    });
    expect(getFirstScanBinding([])).toBeNull();
    expect(getFirstScanBinding(undefined)).toBeNull();
  });

  it("binding accessors read snake_case then camelCase", () => {
    expect(getScanBindingId({ bindingId: "b1" } as never)).toBe("b1");
    expect(getScanBindingTarget({ target_ref: "t1" } as never)).toBe("t1");
    expect(getScanBindingConnector({ connectorType: "feishu" } as never)).toBe("feishu");
    expect(getScanBindingAgentId({ agent_id: "a1" } as never)).toBe("a1");
    expect(getScanBindingTreeKey({ tree_key: "k1" } as never)).toBe("k1");
  });

  it("getFeishuBindingFormTarget prefers treeKey over targetRef", () => {
    expect(
      getFeishuBindingFormTarget({ tree_key: "k1", target_ref: "t1" } as never),
    ).toBe("k1");
    expect(getFeishuBindingFormTarget({ target_ref: "t1" } as never)).toBe("t1");
  });

  it("getScanBindingDisplayName reads core_parent_document_name", () => {
    expect(
      getScanBindingDisplayName({ core_parent_document_name: "Doc" } as never),
    ).toBe("Doc");
    expect(getScanBindingDisplayName(undefined)).toBe("");
  });

  it("getDocumentDisplayName/getDocumentPath/getDocumentLastUpdatedAt fall back sensibly", () => {
    expect(getDocumentDisplayName({} as never)).toBe("-");
    expect(getDocumentDisplayName({ name: "file.txt" } as never)).toBe("file.txt");
    expect(getDocumentPath({ object_key: "a/b.txt" } as never)).toBe("a/b.txt");
    expect(getDocumentLastUpdatedAt({ created_at: "c1" } as never)).toBe("c1");
  });

  it("getScanTreeNodePath reads target_ref/node_ref/object_key/key in order", () => {
    expect(getScanTreeNodePath({ target_ref: "t1", key: "k1" } as never)).toBe("t1");
    expect(getScanTreeNodePath({ key: "k1" } as never)).toBe("k1");
    expect(getScanTreeNodePath(null)).toBe("");
  });
});

describe("buildScanBindingTargetLabels", () => {
  it("builds a label map keyed by targetRef and treeKey", () => {
    const labels = buildScanBindingTargetLabels([
      {
        core_parent_document_name: "Doc A",
        target_ref: "t1",
        tree_key: "k1",
      } as never,
    ]);
    expect(labels).toEqual({ t1: "Doc A", k1: "Doc A" });
  });

  it("uses the fallback binding when the list is empty", () => {
    const labels = buildScanBindingTargetLabels([], {
      core_parent_document_name: "Doc B",
      target_ref: "t2",
    } as never);
    expect(labels).toEqual({ t2: "Doc B" });
  });

  it("skips bindings without a display name", () => {
    expect(buildScanBindingTargetLabels([{ target_ref: "t3" } as never])).toEqual({});
  });
});

describe("inferSourceKind", () => {
  it("detects feishu via connector type, target type, or source options", () => {
    expect(inferSourceKind(undefined, { connector_type: "feishu-drive" } as never)).toBe(
      "feishu",
    );
    expect(inferSourceKind(undefined, { target_type: "wiki" } as never)).toBe("feishu");
    expect(
      inferSourceKind({ source_options: { source_type: "feishu" } } as never, null),
    ).toBe("feishu");
  });

  it("detects notion via connector/source type or page/database target type", () => {
    expect(inferSourceKind(undefined, { connector_type: "notion" } as never)).toBe(
      "notion",
    );
    expect(inferSourceKind(undefined, { target_type: "database" } as never)).toBe(
      "notion",
    );
  });

  it("defaults to local when nothing matches", () => {
    expect(inferSourceKind(undefined, undefined)).toBe("local");
  });
});

describe("getBindingSchedule", () => {
  it("prefers the legacy schedule_expr field", () => {
    expect(getBindingSchedule({ schedule_expr: "daily@08:00" } as never)).toBe(
      "daily@08:00",
    );
  });

  it("builds a weekly expression from schedule_policy rules", () => {
    const binding = {
      schedule_policy: { rules: [{ time: "09:00:00", days: ["mon", "wed"] }] },
    } as never;
    expect(getBindingSchedule(binding)).toBe("weekly:1,3@09:00:00");
  });

  it("expands the everyday/workday/non_workday shortcuts", () => {
    const binding = {
      schedule_policy: { rules: [{ time: "09:00:00", days: ["everyday"] }] },
    } as never;
    expect(getBindingSchedule(binding)).toBe("weekly:1,2,3,4,5,6,7@09:00:00");
  });

  it("returns an empty string when there is no usable schedule data", () => {
    expect(getBindingSchedule(undefined)).toBe("");
    expect(getBindingSchedule({ schedule_policy: {} } as never)).toBe("");
  });
});

describe("getBindingLastError", () => {
  it("localizes a string error code", () => {
    expect(getBindingLastError({ last_error: "2000123" } as never)).toBe(
      "localized:2000123",
    );
  });

  it("localizes an object error via its code or error_code", () => {
    expect(getBindingLastError({ lastError: { code: "2000123" } } as never)).toBe(
      "localized:2000123",
    );
    expect(
      getBindingLastError({ lastError: { error_code: "2000456" } } as never),
    ).toBe("localized:2000456");
  });

  it("returns an empty string when there is no error", () => {
    expect(getBindingLastError(undefined)).toBe("");
  });
});
