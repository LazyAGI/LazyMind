import type { TreeNodePage } from "@/api/generated/scan-client";
import { axiosInstance } from "@/components/request";

interface LocalPathRecommendationRequest {
  agent_id?: string;
  force_refresh?: boolean;
}

export async function listLocalPathRecommendations(
  request: LocalPathRecommendationRequest,
) {
  return axiosInstance.post<TreeNodePage>(
    "/api/scan/binding-targets/tree/recommendations-list",
    request,
  );
}
