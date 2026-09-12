import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { LoadingSpinner } from '@/components/ui/LoadingSpinner';
import { Select } from '@/components/ui/Select';
import { IconCheckCircle2, IconNetwork, IconAlertTriangle } from '@/components/ui/icons';
import { authFilesApi, type AuthFileConnectionTestResponse } from '@/services/api/authFiles';
import type { AuthFileItem } from '@/types';
import { maskSensitiveText } from '@/utils/format';
import { getErrorMessage } from '@/utils/helpers';
import { ProTaskDialog } from '@/pro/shared/ProSurface';
import styles from './AuthFileConnectionTestModal.module.scss';

type TestStatus = 'idle' | 'running' | 'success' | 'error';

const isTextModel = (model: { id: string; type?: string }) => {
  const id = model.id.trim().toLowerCase();
  const type = String(model.type ?? '').trim().toLowerCase();
  return id !== '' && !id.includes('image') && !id.includes('video') && type !== 'openai-image';
};

const normalizeAuthIndex = (value: AuthFileItem['authIndex']) => {
  if (value == null) return undefined;
  const normalized = String(value).trim();
  return normalized || undefined;
};

export function AuthFileConnectionTestModal({
  file,
  onClose,
}: {
  file: AuthFileItem | null;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const requestSequence = useRef(0);
  const abortRef = useRef<AbortController | null>(null);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [modelsError, setModelsError] = useState('');
  const [models, setModels] = useState<string[]>([]);
  const [selectedModel, setSelectedModel] = useState('');
  const [status, setStatus] = useState<TestStatus>('idle');
  const [result, setResult] = useState<AuthFileConnectionTestResponse | null>(null);
  const [displayFile, setDisplayFile] = useState(file);
  const activeFile = file ?? displayFile;

  useEffect(() => {
    if (file) setDisplayFile(file);
  }, [file]);

  useEffect(() => {
    abortRef.current?.abort();
    abortRef.current = null;
    requestSequence.current += 1;
    const sequence = requestSequence.current;
    setModels([]);
    setSelectedModel('');
    setModelsError('');
    setResult(null);
    setStatus('idle');
    if (!activeFile) return;

    setModelsLoading(true);
    void authFilesApi
      .getModelsForAuthFile(activeFile.name, normalizeAuthIndex(activeFile.authIndex), 'connection-test')
      .then((items) => {
        if (requestSequence.current !== sequence) return;
        const seen = new Set<string>();
        const nextModels = items
          .filter(isTextModel)
          .map((item) => item.id.trim())
          .filter((model) => {
            if (!model || seen.has(model)) return false;
            seen.add(model);
            return true;
          });
        setModels(nextModels);
        setSelectedModel(nextModels[0] ?? '');
      })
      .catch((error: unknown) => {
        if (requestSequence.current !== sequence) return;
        setModelsError(
          maskSensitiveText(
            getErrorMessage(error, t('auth_files.connection_test_load_models_failed'))
          )
        );
      })
      .finally(() => {
        if (requestSequence.current === sequence) setModelsLoading(false);
      });
  }, [activeFile, t]);

  const modelOptions = useMemo(
    () => models.map((model) => ({ value: model, label: model })),
    [models]
  );

  const runTest = async () => {
    if (!activeFile || !selectedModel || status === 'running') return;
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    requestSequence.current += 1;
    const sequence = requestSequence.current;
    setStatus('running');
    setResult(null);
    try {
      const response = await authFilesApi.testConnection(
        {
          name: activeFile.name,
          auth_index: normalizeAuthIndex(activeFile.authIndex),
          model: selectedModel,
        },
        controller.signal
      );
      if (requestSequence.current !== sequence) return;
      const normalizedResponse = {
        ...response,
        output: response.output ? maskSensitiveText(response.output) : undefined,
        error: response.error ? maskSensitiveText(response.error) : undefined,
      };
      setResult(normalizedResponse);
      setStatus(response.success ? 'success' : 'error');
    } catch (error: unknown) {
      if (requestSequence.current !== sequence) return;
      setResult({
        success: false,
        model: selectedModel,
        latency_ms: 0,
        error: maskSensitiveText(
          getErrorMessage(error, t('auth_files.connection_test_failed'))
        ),
      });
      setStatus('error');
    } finally {
      if (abortRef.current === controller) abortRef.current = null;
    }
  };

  const close = () => {
    abortRef.current?.abort();
    abortRef.current = null;
    requestSequence.current += 1;
    onClose();
  };

  const statusLabel =
    status === 'running'
      ? t('auth_files.connection_test_running')
      : status === 'success'
        ? t('auth_files.connection_test_success')
        : status === 'error'
          ? t('auth_files.connection_test_failed')
          : t('auth_files.connection_test_ready');

  return (
    <ProTaskDialog
      open={file !== null}
      title={t('auth_files.connection_test_title', { account: activeFile?.email || activeFile?.name || '' })}
      onClose={close}
      onAfterClose={() => setDisplayFile(null)}
      footer={
        <>
          <Button variant="secondary" onClick={close}>
            {t('common.close')}
          </Button>
          <Button
            onClick={() => void runTest()}
            disabled={modelsLoading || !selectedModel || status === 'running'}
          >
            {status === 'running' ? (
              <LoadingSpinner size={14} />
            ) : (
              <IconNetwork size={15} />
            )}
            {status === 'success' || status === 'error'
              ? t('auth_files.connection_test_retry')
              : t('auth_files.connection_test_start')}
          </Button>
        </>
      }
    >
      <div className={styles.content}>
        <p className={styles.hint}>{t('auth_files.connection_test_hint')}</p>

        <div className={styles.modelRow}>
          <span className={styles.label}>{t('auth_files.connection_test_model')}</span>
          {modelsLoading ? (
            <span className={styles.loadingModels}>
              <LoadingSpinner size={14} />
              {t('auth_files.connection_test_loading_models')}
            </span>
          ) : (
            <Select
              value={selectedModel}
              options={modelOptions}
              onChange={(value) => {
                setSelectedModel(value);
                setStatus('idle');
                setResult(null);
              }}
              placeholder={t('auth_files.connection_test_select_model')}
              ariaLabel={t('auth_files.connection_test_model')}
              disabled={modelOptions.length === 0 || status === 'running'}
            />
          )}
        </div>

        {modelsError && <div className={styles.errorBox}>{modelsError}</div>}
        {!modelsLoading && !modelsError && modelOptions.length === 0 && (
          <div className={styles.errorBox}>{t('auth_files.connection_test_no_models')}</div>
        )}

        <div className={`${styles.status} ${styles[status]}`}>
          {status === 'running' ? (
            <LoadingSpinner size={17} />
          ) : status === 'success' ? (
            <IconCheckCircle2 size={18} />
          ) : status === 'error' ? (
            <IconAlertTriangle size={18} />
          ) : (
            <IconNetwork size={18} />
          )}
          <strong>{statusLabel}</strong>
          {result && result.latency_ms > 0 && (
            <span className={styles.latency}>
              {t('auth_files.connection_test_latency', { count: result.latency_ms })}
            </span>
          )}
        </div>

        {result?.output && (
          <div className={styles.resultBlock}>
            <span className={styles.label}>{t('auth_files.connection_test_output')}</span>
            <pre>{result.output}</pre>
          </div>
        )}

        {result?.error && (
          <div className={`${styles.resultBlock} ${styles.errorResult}`}>
            <span className={styles.label}>{t('auth_files.connection_test_error_detail')}</span>
            <pre>
              {result.error}
              {result.http_status ? `\nHTTP ${result.http_status}` : ''}
              {result.error_code ? `\n${result.error_code}` : ''}
            </pre>
          </div>
        )}
      </div>
    </ProTaskDialog>
  );
}
