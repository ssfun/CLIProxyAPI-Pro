import type { MonitoringApiKeyIdentity } from '../apiKeyIdentity';
import { formatMonitoringApiKeyLabel } from '../apiKeyIdentity';
import styles from '../monitoring.module.scss';

export function MonitoringApiKeyCell({ apiKey, profileSnapshot }: {
  apiKey: MonitoringApiKeyIdentity;
  profileSnapshot: string;
}) {
  return (
    <div className={`${styles.primaryCell} ${styles.realtimeApiKeyCell}`}>
      <span className={apiKey.name ? undefined : styles.monoCell} title={formatMonitoringApiKeyLabel(apiKey)}>
        {apiKey.name || apiKey.masked}
      </span>
      {apiKey.name ? <small className={styles.monoCell} title={apiKey.masked}>{apiKey.masked}</small> : null}
      {profileSnapshot ? <small title={profileSnapshot}>{profileSnapshot}</small> : null}
    </div>
  );
}
