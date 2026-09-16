import { useDroppable } from '@dnd-kit/core';
import type { ComponentProps } from 'react';

// History sorting handles use dnd-kit; group members also retain native dragging.
export default function ConversationGroupDropZone({ groupId, groupName, disabled, className = '', ...props }: ComponentProps<'div'> & { groupId: string; groupName: string; disabled: boolean }) {
  const { setNodeRef, isOver } = useDroppable({
    id: `group:${groupId}`,
    disabled,
    data: { kind: 'conversation-group', groupId, label: groupName },
  });
  return <div {...props} ref={setNodeRef} className={`${className}${isOver ? ' group-drop-target' : ''}`} />;
}
