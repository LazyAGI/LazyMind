import { Configuration, ConversationGroupsApi, DefaultApi, type ConversationOrganizerRun } from "@/api/generated/core-client";
import { axiosInstance, BASE_URL } from "@/components/request";

export type { ConversationGroup, ConversationOrganizerItem as OrganizerRunItem } from "@/api/generated/core-client";
export type OrganizerRun = ConversationOrganizerRun;

const client = new ConversationGroupsApi(new Configuration({ basePath: BASE_URL }), BASE_URL, axiosInstance);
export const CONVERSATION_GROUPS_CHANGED_EVENT = "lazymind:conversation-groups-changed";
export function emitConversationGroupsChanged() { window.dispatchEvent(new Event(CONVERSATION_GROUPS_CHANGED_EVENT)); }

export async function listConversationGroups(keyword?: string) { return (await client.listConversationGroups({ keyword })).data.groups; }
export async function createConversationGroup(input: { name: string; scope?: string }) { return (await client.createConversationGroup({ conversationGroupCreateRequest: input })).data.group; }
export async function getConversationGroup(groupId: string, pageToken = "", keyword = "") {
  const data = (await client.getConversationGroup({ groupId, pageSize: 50, pageToken, keyword })).data;
  return { group: data.group, conversations: data.conversations ?? [], nextPageToken: data.next_page_token };
}
export async function updateConversationGroup(groupId: string, input: { name: string; scope?: string; organizer_run_id?: string }) { return (await client.updateConversationGroup({ groupId, conversationGroupUpdateRequest: input })).data.group; }
export async function deleteConversationGroup(groupId: string) { await client.deleteConversationGroup({ groupId }); }
export async function assignConversation(groupId: string, conversationId: string) { await client.assignConversationGroup({ groupId, conversationGroupAssignRequest: { conversation_id: conversationId } }); }
export async function removeConversation(groupId: string, conversationId: string) { await client.removeConversationGroupMember({ groupId, conversationId }); }
export async function startOrganizerRun() { return (await client.startConversationOrganizer()).data.run; }
export async function getOrganizerRun(runId: string) { return (await client.getConversationOrganizer({ runId })).data.run; }
export async function getLatestOrganizerState() { return (await client.getLatestConversationOrganizer()).data; }
export async function getLatestSuccessfulOrganizerRun() {
  const latest = await getLatestOrganizerState();
  return latest.latest_successful_run_id
    ? (await client.getConversationOrganizer({ runId: latest.latest_successful_run_id })).data.run
    : null;
}
export async function runAction(runId: string, action: "cancel" | "retry" | "undo" | "confirm") {
  if (action === "confirm") return (await client.confirmConversationOrganizer({ runId })).data.run;
  if (action === "cancel") return (await client.cancelConversationOrganizer({ runId })).data.run;
  if (action === "retry") return (await client.retryConversationOrganizer({ runId })).data.run;
  return (await client.undoConversationOrganizer({ runId })).data.run;
}
export async function correctOrganizerItem(runId: string, conversationId: string, input: { group_id?: string | null; new_group?: { name: string; scope?: string } }) {
  return (await client.correctConversationOrganizerItem({ runId, conversationId, conversationOrganizerCorrectionRequest: input })).data.run;
}

export async function updateGroupPlacement(groupId: string, input: { pinned?: boolean; before_group_id?: string }) { return (await client.updateConversationGroupPlacement({ groupId, conversationGroupPlacementRequest: input })).data.groups; }

export async function renameGroupConversation(id: string, title: string, revision: number) { await new DefaultApi(new Configuration({ basePath: BASE_URL }), BASE_URL, axiosInstance).apiCoreConversationsNameTitlePatch({ name: id, apiCoreConversationsNameTitlePatchRequest: { display_name: title, title_revision: revision } }); }
