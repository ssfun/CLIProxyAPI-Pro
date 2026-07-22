import {
  getRangeStartMs,
  type MonitoringAccountRow,
  type MonitoringEventRow,
  type MonitoringTimeRange,
} from './hooks/useMonitoringData';

const ACCOUNT_STATUS_BLOCK_COUNT = 20;
const ACCOUNT_STATUS_BLOCK_DURATION_MS = 10 * 60 * 1000;

export type AccountStatusBlockDetail = {
  success: number;
  failure: number;
  rate: number;
  startTime: number;
  endTime: number;
};

export type AccountStatusRange = {
  startTime: number;
  endTime: number;
};

export type AccountStatusData = {
  blockDetails: AccountStatusBlockDetail[];
  successRate: number;
  totalSuccess: number;
  totalFailure: number;
};

export const buildAccountStatusRange = (
  rows: MonitoringAccountRow[],
  range: MonitoringTimeRange,
  nowMs = Date.now()
): AccountStatusRange => {
  if (range !== 'all') {
    return {
      startTime: getRangeStartMs(range, nowMs),
      endTime: nowMs,
    };
  }

  let minTimestamp = Number.POSITIVE_INFINITY;
  rows.forEach((row) => {
    row.rows?.forEach((event) => {
      minTimestamp = Math.min(minTimestamp, event.timestampMs);
    });
  });
  return {
    startTime: Number.isFinite(minTimestamp)
      ? minTimestamp
      : nowMs - ACCOUNT_STATUS_BLOCK_COUNT * ACCOUNT_STATUS_BLOCK_DURATION_MS,
    endTime: nowMs,
  };
};

export const buildAccountStatusData = (
  rows: MonitoringEventRow[],
  range: AccountStatusRange
): AccountStatusData => {
  const duration = Math.max(range.endTime - range.startTime, ACCOUNT_STATUS_BLOCK_DURATION_MS);
  const blockDuration = duration / ACCOUNT_STATUS_BLOCK_COUNT;
  const blockDetails = Array.from({ length: ACCOUNT_STATUS_BLOCK_COUNT }, (_, index) => ({
    success: 0,
    failure: 0,
    rate: -1,
    startTime: range.startTime + index * blockDuration,
    endTime: index === ACCOUNT_STATUS_BLOCK_COUNT - 1
      ? range.endTime
      : range.startTime + (index + 1) * blockDuration,
  }));
  let totalSuccess = 0;
  let totalFailure = 0;

  rows.forEach((row) => {
    if (row.timestampMs < range.startTime || row.timestampMs > range.endTime) return;
    const index = Math.min(
      ACCOUNT_STATUS_BLOCK_COUNT - 1,
      Math.max(0, Math.floor((row.timestampMs - range.startTime) / blockDuration))
    );
    if (row.failed) {
      blockDetails[index].failure += 1;
      totalFailure += 1;
    } else {
      blockDetails[index].success += 1;
      totalSuccess += 1;
    }
  });

  blockDetails.forEach((detail) => {
    const total = detail.success + detail.failure;
    detail.rate = total > 0 ? detail.success / total : -1;
  });
  const total = totalSuccess + totalFailure;

  return {
    blockDetails,
    successRate: total > 0 ? (totalSuccess / total) * 100 : 100,
    totalSuccess,
    totalFailure,
  };
};
