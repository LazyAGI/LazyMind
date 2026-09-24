import { useDroppable } from '@dnd-kit/core';
import type { ComponentProps } from 'react';

// History sorting handles use dnd-kit; group members also retain native dragging.
export default function ConversationGroupDropZone({ groupId, groupName, isTaskConv = false, disabled, className = '', ...props }: ComponentProps<'div'> & { groupId: string; groupName: string; isTaskConv?: boolean; disabled: boolean }) {
  const { setNodeRef, isOver } = useDroppable({
    id: `group:${groupId}`,
    disabled,
    data: { kind: 'conversation-group', groupId, isTaskConv, label: groupName },
  });
  return <div {...props} ref={setNodeRef} className={`${className}${isOver ? ' group-drop-target' : ''}`} />;
}
