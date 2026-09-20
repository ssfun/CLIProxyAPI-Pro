import type { MonitoringApiKeyIdentity } from '../apiKeyIdentity';
import { formatMonitoringApiKeyLabel } from '../apiKeyIdentity';
import styles from '../monitoring.module.scss';

export function MonitoringApiKeyCell({ apiKey, profileSnapshot }: {
  apiKey: MonitoringApiKeyIdentity;
  profileSnapshot: string;
}) {
  const name = apiKey.name?.trim();
  return (
    <div className={`${styles.primaryCell} ${styles.realtimeApiKeyCell}`}>
      {name ? (
        <div className={styles.realtimeApiKeyIdentity} title={formatMonitoringApiKeyLabel(apiKey)}>
          <span>{name}</span>{' '}
          <small className={styles.monoCell}>({apiKey.masked})</small>
        </div>
      ) : (
        <span className={styles.monoCell} title={apiKey.masked}>{apiKey.masked}</span>
      )}
      {profileSnapshot ? <small title={`Profile · ${profileSnapshot}`}>Profile · {profileSnapshot}</small> : null}
    </div>
  );
}
