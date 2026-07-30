import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { IconRefreshCw, IconShield } from '@/components/ui/icons';
import type { ProxyPoolSnapshot } from '@/services/api/proxyPool';
import styles from './ProxyPool.module.scss';

interface ProxyPoolHeaderProps {
  snapshot: ProxyPoolSnapshot | null;
  loading: boolean;
  busy: boolean;
  onRefresh: () => void;
  onTakeover: () => void;
}

export function ProxyPoolHeader({
  snapshot,
  loading,
  busy,
  onRefresh,
  onTakeover,
}: ProxyPoolHeaderProps) {
  const { t } = useTranslation();
  const active = snapshot?.takeoverActive === true;

  return (
    <header className={styles.header}>
      <div className={styles.headerIdentity}>
        <span className={`${styles.headerIcon} ${active ? styles.headerIconActive : ''}`}>
          <IconShield size={20} />
        </span>
        <div>
          <div className={styles.titleLine}>
            <h1>{t('proxy_pool.title', { defaultValue: 'Proxy Pool' })}</h1>
            <span className={active ? styles.takeoverOn : styles.takeoverOff}>
              <span />
              {active
                ? t('proxy_pool.takeover_active', { defaultValue: 'Taking over traffic' })
                : t('proxy_pool.takeover_inactive', { defaultValue: 'Not taking over' })}
            </span>
          </div>
          <p>
            {t('proxy_pool.subtitle_compact', {
              defaultValue: 'A stable local SOCKS5 endpoint backed by multiple managed proxies.',
            })}
          </p>
        </div>
      </div>
      <div className={styles.headerActions}>
        <Button
          variant="ghost"
          size="sm"
          onClick={onRefresh}
          disabled={loading || busy}
          aria-label={t('common.refresh')}
        >
          <IconRefreshCw size={16} />
          {t('common.refresh')}
        </Button>
        <Button
          variant={active ? 'danger' : 'primary'}
          size="sm"
          onClick={onTakeover}
          loading={busy}
          disabled={loading || !snapshot?.pluginDiscovered}
        >
          {active
            ? t('proxy_pool.stop_takeover', { defaultValue: 'Stop takeover' })
            : t('proxy_pool.start_takeover', { defaultValue: 'Start takeover' })}
        </Button>
      </div>
    </header>
  );
}
