import { useRef } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { IconCheck, IconRefreshCw } from '@/components/ui/icons';
import { useActionBarHeightVar } from '@/hooks/useActionBarHeightVar';
import configStyles from '@/pages/ConfigPage.module.scss';

interface ProxyPoolSaveBarProps {
  visible: boolean;
  saving: boolean;
  onDiscard: () => void;
  onSave: () => void;
}

export function ProxyPoolSaveBar({ visible, saving, onDiscard, onSave }: ProxyPoolSaveBarProps) {
  const { t } = useTranslation();
  const actionBarRef = useRef<HTMLDivElement>(null);
  useActionBarHeightVar(actionBarRef, '--proxy-pool-action-bar-height', visible);

  if (!visible) return null;
  const content = (
    <div className={configStyles.floatingActionContainer} ref={actionBarRef}>
      <div className={configStyles.floatingActionList}>
        <div className={`${configStyles.floatingStatus} ${configStyles.modified}`}>
          {saving
            ? t('config_management.status_saving_short', { defaultValue: 'Saving' })
            : t('config_management.status_dirty_short', { defaultValue: 'Unsaved' })}
        </div>
        <button
          type="button"
          className={configStyles.floatingActionButton}
          onClick={onDiscard}
          disabled={saving}
          title={t('proxy_pool.discard_changes', { defaultValue: 'Discard changes' })}
          aria-label={t('proxy_pool.discard_changes', { defaultValue: 'Discard changes' })}
        >
          <IconRefreshCw size={16} />
        </button>
        <button
          type="button"
          className={configStyles.floatingActionButton}
          onClick={onSave}
          disabled={saving}
          title={t('common.save')}
          aria-label={t('common.save')}
        >
          <IconCheck size={16} />
          {!saving && <span className={configStyles.dirtyDot} aria-hidden="true" />}
        </button>
      </div>
    </div>
  );
  return typeof document === 'undefined' ? content : createPortal(content, document.body);
}
