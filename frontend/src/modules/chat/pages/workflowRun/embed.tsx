import { lazy } from 'react';
import { Navigate, useLocation } from 'react-router-dom';
import { AgentAppsAuth } from '@/components/auth';
import { isLocalSessionEnabled } from '@/runtime/localSession';

const WorkflowRunPage = lazy(() => import('./index'));

export default function WorkflowRunEmbedPage() {
  const location = useLocation();
  if (!isLocalSessionEnabled() && !AgentAppsAuth.getUserInfo()?.token) {
    return <Navigate to='/login' replace state={{ workflowRunReturnTo: `${location.pathname}${location.search}` }} />;
  }
  return <WorkflowRunPage embedded />;
}
