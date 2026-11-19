import AskCard from '@/modules/chat/components/AskCard';
import type { WorkflowInputRequest } from '@/modules/chat/utils/workflowControl';

/** The same question form as native chat; only the answer's transport differs. */
export default function WorkflowInputRequestCard({ request, onAnswer }: {
  request: WorkflowInputRequest;
  onAnswer: (answerText: string) => Promise<boolean>;
}) {
  return <AskCard key={`${request.artifact_id}:${request.revision}`} askPending={{
    ask_id: `${request.artifact_id}:${request.revision}`,
    title: request.title,
    questions: request.questions.map(question => ({
      ...question,
      choices: question.choices ?? question.options,
      // Workflow input checkpoints save textual answers. Uploads remain a
      // separate, explicitly scoped resource operation for the executing host.
      attachment: undefined,
    })),
  }} onSubmit={payload => onAnswer(payload.text)} />;
}
