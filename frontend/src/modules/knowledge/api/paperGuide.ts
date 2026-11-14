import { axiosInstance, BASE_URL } from "@/components/request";
import type {
  PaperGuideStatusResponse,
  PaperGuideRevisionResponse,
  PaperGuideRevisionView,
  PaperGuideEvidenceResponse,
  PaperGuideProgressRequest,
  PaperGuideGenerateRequest,
} from "@/api/generated/core-client";
export interface GuideTarget {
  dataset: string;
  document: string;
  language: string;
}
const path = (t: GuideTarget) =>
  `${BASE_URL}/api/core/datasets/${encodeURIComponent(t.dataset)}/documents/${encodeURIComponent(t.document)}/paper-guide`;
const config = (t: GuideTarget, signal?: AbortSignal) => ({
  params: { language: t.language },
  signal,
});
export const paperGuideApi = {
  async status(
    t: GuideTarget,
    options: { signal?: AbortSignal; etag?: string } = {},
  ) {
    const r = await axiosInstance.get<{ data: PaperGuideStatusResponse }>(
      path(t),
      {
        ...config(t, options.signal),
        headers: options.etag ? { "If-None-Match": options.etag } : undefined,
        validateStatus: (s) => s === 304 || (s >= 200 && s < 300),
      },
    );
    return {
      data: r.status === 304 ? undefined : r.data.data,
      etag: r.headers.etag as string | undefined,
    };
  },
  async revision(t: GuideTarget, id: string, after = 0, signal?: AbortSignal) {
    return (
      await axiosInstance.get<{ data: PaperGuideRevisionResponse }>(
        `${path(t)}/revisions/${encodeURIComponent(id)}`,
        {
          ...config(t, signal),
          params: { language: t.language, after_version: after },
        },
      )
    ).data.data;
  },
  async evidence(
    t: GuideTarget,
    id: string,
    evidence: string,
    signal?: AbortSignal,
  ) {
    return (
      await axiosInstance.get<{ data: PaperGuideEvidenceResponse }>(
        `${path(t)}/revisions/${encodeURIComponent(id)}/evidence/${encodeURIComponent(evidence)}`,
        config(t, signal),
      )
    ).data.data;
  },
  async generate(t: GuideTarget, body: PaperGuideGenerateRequest) {
    return (
      await axiosInstance.post<{ data: PaperGuideRevisionView }>(
        `${path(t)}:generate`,
        body,
      )
    ).data.data;
  },
  async cancel(t: GuideTarget, id: string) {
    await axiosInstance.post(
      `${path(t)}/runs/${encodeURIComponent(id)}:cancel`,
      {},
      config(t),
    );
  },
  async resume(t: GuideTarget, id: string) {
    await axiosInstance.post(
      `${path(t)}/runs/${encodeURIComponent(id)}:resume`,
      {},
      config(t),
    );
  },
  async progress(t: GuideTarget, body: PaperGuideProgressRequest) {
    await axiosInstance.put(`${path(t)}/progress`, body, config(t));
  },
};
