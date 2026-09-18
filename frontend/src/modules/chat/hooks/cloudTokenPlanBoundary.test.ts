import {readFileSync} from "node:fs";
import {resolve} from "node:path";

import {describe, expect, it} from "vitest";

const frontendRoot = resolve(process.cwd());

describe("Desktop Cloud Token Plan boundary", () => {
  it("consumes only minimal readiness state and rechecks after Cloud session changes", () => {
    const guard = readFileSync(resolve(frontendRoot, "src/modules/chat/hooks/useChatModelProviderGuard.ts"), "utf8");
    expect(guard).toContain("cloud_plan_required");
    expect(guard).toContain("cloud_plan_url");
    expect(guard).toContain("LAZYMIND_CLOUD_SESSION_CHANGED_EVENT");
    expect(guard).toMatch(/addEventListener\(["']focus["']/);
    expect(guard).not.toMatch(/periodic_quota|remaining|next_refresh_at|plan_id|plan_version/);
  });

  it("shows a trusted Cloud activation action instead of Plan quota details", () => {
    const page = readFileSync(resolve(frontendRoot, "src/modules/chat/pages/newChat/index.tsx"), "utf8");
    expect(page).toContain("cloudPlanRequired");
    expect(page).toContain("openCloudTokenPlan");
    expect(page).not.toMatch(/periodic_quota|remaining|next_refresh_at|plan_id|plan_version/);
  });

  it("publishes a Cloud-session change event without storing entitlement state", () => {
    const session = readFileSync(resolve(frontendRoot, "src/runtime/cloud/session.ts"), "utf8");
    const layout = readFileSync(resolve(frontendRoot, "src/layouts/MainLayout.tsx"), "utf8");
    expect(session).toContain("LAZYMIND_CLOUD_SESSION_CHANGED_EVENT");
    expect(layout).toContain("LAZYMIND_CLOUD_SESSION_CHANGED_EVENT");
    expect(session).not.toMatch(/localStorage|sessionStorage|indexedDB/i);
    expect(layout).not.toMatch(/(?:localStorage|sessionStorage)\.(?:getItem|setItem)\([^)]*cloud/i);
  });
});
