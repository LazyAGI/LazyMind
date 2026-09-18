import { axiosInstance, BASE_URL } from "@/components/request";

import type { OfficialKnowledgeBase } from "./knowledgeSquareData";

interface CoreResponse<T> {
  data?: T;
}

interface CloudKnowledgePage {
  items: OfficialKnowledgeBase[];
  next_cursor?: string;
}

export async function getCloudKnowledgeSquare(): Promise<OfficialKnowledgeBase[]> {
  const response = await axiosInstance.get<CoreResponse<CloudKnowledgePage>>(
    `${BASE_URL}/api/core/cloud/knowledge-square`,
    { params: { page: 1, page_size: 100 } },
  );
  return response.data.data?.items ?? [];
}
