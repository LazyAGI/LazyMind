import { createUpstreamCatalogIndex, type UpstreamCatalogFile } from "./upstreamCatalogIndex";

export { createUpstreamCatalogIndex, modelModalities } from "./upstreamCatalogIndex";
export type { ModelModality, UpstreamModelRecord } from "./upstreamCatalogIndex";

// Builds generate this snapshot before bundling. A clean checkout can still run
// tests and type checks before that build step, with no catalog marks to show.
const snapshots = import.meta.glob<UpstreamCatalogFile>("./upstreamModelCatalog.json", {
  eager: true,
  import: "default",
});
const index = createUpstreamCatalogIndex(snapshots["./upstreamModelCatalog.json"]);

export const lookupUpstreamModel = index.lookupModel;

export function upstreamCatalogGeneratedAt() {
  return index.generatedAt;
}
