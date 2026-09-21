import i18n from '@/i18n';

export function documentPublicationErrorMessage(code?: string, provider?: string): string {
  const providerKey = `chat.writerIR.providers.${provider}`;
  const label = provider
    ? (i18n.exists(providerKey) ? String(i18n.t(providerKey)) : provider)
    : String(i18n.t('chat.writerIR.cloudDocumentProvider'));
  let message = 'publicationUnknown';
  if (code === 'DOCUMENT_PROVIDERS_UNAVAILABLE') message = 'publicationProvidersFailed';
  else if (code === 'PROVIDER_CREDENTIALS_UNAVAILABLE') message = 'publicationAuthorizationFailed';
  else if (code === 'PUBLICATION_IN_PROGRESS') message = 'publicationPending';
  else if (code === 'PUBLICATION_RECOVERY_CLOSED') return String(i18n.t('chat.writerIR.publicationRecovery.closed'));
  else if (code === 'DOCUMENT_CONVERSION_FAILED') message = 'publicationConversionFailed';
  else if (code === 'DOCUMENT_ACTION_UNSUPPORTED') message = 'publicationUnsupported';
  else if (['PROVIDER_SYNC_LOCAL_CONFLICT', 'PROVIDER_SYNC_LOCAL_PERSIST_FAILED'].includes(code ?? '')) {
    message = 'publicationLocalSaveFailed';
  } else if (['DOCUMENT_ACTION_INVALID', 'REVISION_CONFLICT', 'DRAFT_VERSION_CONFLICT',
    'DRAFT_VERSION_REQUIRED', 'ARTIFACT_IN_USE'].includes(code ?? '')) {
    message = 'publicationPreparationFailed';
  }
  return String(i18n.t(`chat.writerIR.${message}`, { provider: label }));
}
