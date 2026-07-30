import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Modal } from '@/components/ui/Modal';
import { IconAlertTriangle, IconCheckCircle2, IconInfo } from '@/components/ui/icons';
import { parseProxyPoolImport, type ProxyPoolNodeConfig } from '@/services/api/proxyPool';
import { maskProxyCredentials } from './proxyPoolUi';
import styles from './ProxyPool.module.scss';

interface ProxyPoolImportModalProps {
  open: boolean;
  existing: ProxyPoolNodeConfig[];
  onClose: () => void;
  onImport: (nodes: ProxyPoolNodeConfig[]) => void;
}

export function ProxyPoolImportModal({
  open,
  existing,
  onClose,
  onImport,
}: ProxyPoolImportModalProps) {
  const { t } = useTranslation();
  const [text, setText] = useState('');
  const [previewed, setPreviewed] = useState(false);
  const result = useMemo(() => parseProxyPoolImport(text, existing), [existing, text]);

  const close = () => {
    setText('');
    setPreviewed(false);
    onClose();
  };
  const confirm = () => {
    if (result.nodes.length === 0) return;
    onImport(result.nodes);
    close();
  };

  return (
    <Modal
      open={open}
      onClose={close}
      width={720}
      title={t('proxy_pool.import_title', { defaultValue: 'Import proxy nodes' })}
      footer={
        <>
          <Button variant="ghost" onClick={close}>
            {t('common.cancel')}
          </Button>
          {!previewed ? (
            <Button onClick={() => setPreviewed(true)} disabled={!text.trim()}>
              {t('proxy_pool.preview_import', { defaultValue: 'Preview import' })}
            </Button>
          ) : (
            <Button onClick={confirm} disabled={result.nodes.length === 0}>
              {t('proxy_pool.confirm_import_count', {
                defaultValue: 'Import {{count}} nodes',
                count: result.nodes.length,
              })}
            </Button>
          )}
        </>
      }
    >
      <div
        className={styles.importSteps}
        aria-label={t('proxy_pool.import_progress', { defaultValue: 'Import progress' })}
      >
        <span className={styles.importStepActive}>
          <b>1</b>
          {t('proxy_pool.import_step_paste', { defaultValue: 'Paste' })}
        </span>
        <span className={previewed ? styles.importStepActive : ''}>
          <b>2</b>
          {t('proxy_pool.import_step_preview', { defaultValue: 'Preview' })}
        </span>
        <span>
          <b>3</b>
          {t('proxy_pool.import_step_confirm', { defaultValue: 'Confirm' })}
        </span>
      </div>
      <label className={styles.importInput}>
        <span>{t('proxy_pool.import_label', { defaultValue: 'Proxy list' })}</span>
        <small>
          {t('proxy_pool.import_hint', {
            defaultValue:
              'One URL per line, or: label | URL | weight. Comments and blank lines are ignored.',
          })}
        </small>
        <textarea
          value={text}
          onChange={(event) => {
            setText(event.target.value);
            setPreviewed(false);
          }}
          placeholder={'socks5://user:pass@host:1080\nTokyo primary | http://host:8080 | 3'}
          rows={8}
          autoFocus
        />
      </label>

      {previewed && (
        <div className={styles.importPreview} aria-live="polite">
          <div className={styles.importSummary}>
            <span className={styles.importAdded}>
              <IconCheckCircle2 size={17} />
              <strong>{result.nodes.length}</strong>
              {t('proxy_pool.new_nodes', { defaultValue: 'new' })}
            </span>
            <span>
              <IconInfo size={17} />
              <strong>{result.duplicateCount}</strong>
              {t('proxy_pool.duplicates', { defaultValue: 'duplicates' })}
            </span>
            <span className={result.errors.length > 0 ? styles.importInvalid : ''}>
              <IconAlertTriangle size={17} />
              <strong>{result.errors.length}</strong>
              {t('proxy_pool.invalid_lines', { defaultValue: 'invalid' })}
            </span>
          </div>
          {result.nodes.length > 0 && (
            <div className={styles.importNodeList}>
              {result.nodes.map((node) => (
                <div key={node.id}>
                  <strong>{node.label || node.id}</strong>
                  <code>{maskProxyCredentials(node.url)}</code>
                </div>
              ))}
            </div>
          )}
          {result.errors.length > 0 && (
            <div className={styles.importErrors}>
              {result.errors.map((error) => (
                <div key={`${error.line}-${error.message}`}>
                  <strong>
                    {t('proxy_pool.line_number', {
                      defaultValue: 'Line {{line}}',
                      line: error.line,
                    })}
                  </strong>
                  <span>{error.message}</span>
                </div>
              ))}
            </div>
          )}
          {result.nodes.length === 0 &&
            result.errors.length === 0 &&
            result.duplicateCount === 0 && (
              <p className={styles.importEmpty}>
                {t('proxy_pool.import_empty', {
                  defaultValue: 'Paste at least one supported HTTP, HTTPS, SOCKS5, or SOCKS5H URL.',
                })}
              </p>
            )}
        </div>
      )}
    </Modal>
  );
}
