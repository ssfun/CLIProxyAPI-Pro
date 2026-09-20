import { modelAuditItems } from './modelAudit';
import type { TFunction } from 'i18next';
import { maskSensitiveText } from '@/utils/format';
import type { MonitoringEventRow } from './hooks/useMonitoringData';

export type RealtimeLogRow = MonitoringEventRow & {
  requestCount: number;
  successRate: number;
  streamKey: string;
  diagnosticText: string;
  errorCategoryKey: string;
  errorSummary: string;
  recentPattern: boolean[];
  recentSuccessCount: number;
  recentFailureCount: number;
};

export const buildRealtimeMetaText = (row: MonitoringEventRow) => {
  const parts = [`${row.endpointMethod} ${row.endpointPath}`.trim()];
  const text = parts.filter(Boolean).join(' · ');
  return maskSensitiveText(text || '-');
};

const buildRealtimeDiagnosticText = (row: MonitoringEventRow) => {
  const parts: string[] = [];
  if (row.statusCode !== null && row.statusCode >= 400) {
    parts.push(`HTTP ${row.statusCode}`);
  }
  if (row.errorCode) parts.push(row.errorCode);
  if (row.retryAfter) parts.push(`Retry ${row.retryAfter}`);
  if (row.attemptIndex !== null) parts.push(`Attempt ${row.attemptIndex}`);
  if (row.accountingQuality) parts.push(`Accounting ${row.accountingQuality}`);
  return maskSensitiveText(parts.join(' · '));
};

export const formatRealtimeTokenBreakdown = (
  breakdown: MonitoringEventRow['tokenBreakdown']
) => breakdown
  ? [
      `total=${breakdown.total_tokens}`,
      `input=${breakdown.input.total_tokens}`,
      `uncached=${breakdown.input.uncached_tokens}`,
      `cache-read=${breakdown.input.cache_read_tokens}`,
      `cache-write=${breakdown.input.cache_write_tokens}`,
      `output=${breakdown.output.total_tokens}`,
      `reasoning=${breakdown.output.reasoning_tokens}`,
      `unclassified=${breakdown.unclassified_tokens}`,
    ].join(', ')
  : '';

const buildRealtimeStatusCodeText = (
  row: Pick<MonitoringEventRow, 'statusCode' | 'errorCode'>
) => {
  if (row.statusCode !== null && row.statusCode >= 400) return String(row.statusCode);
  return row.errorCode ? maskSensitiveText(row.errorCode) : '';
};

export const buildRealtimeStatusLabel = (
  row: Pick<MonitoringEventRow, 'failed' | 'statusCode' | 'errorCode'>,
  label: string
) => {
  if (!row.failed) return label;
  const codeText = buildRealtimeStatusCodeText(row);
  return codeText ? `${label} · ${codeText}` : label;
};

export const compactRealtimeErrorMessage = (message: string, maxLength = 220) => {
  const masked = maskSensitiveText(message.replace(/\s+/g, ' ').trim());
  return masked.length > maxLength ? `${masked.slice(0, maxLength - 1)}...` : masked;
};

export const resolveRealtimeErrorCategoryKey = (row: MonitoringEventRow) => {
  const code = row.errorCode.toLowerCase();
  const message = row.errorMessage.toLowerCase();
  const status = row.statusCode;
  const combined = `${code} ${message}`;

  if (status === 401 || status === 403 || /\b(auth|unauthorized|forbidden|invalid[_ -]?key|permission)\b/.test(combined)) {
    return 'monitoring.error_category_auth';
  }
  if (status === 429 || /\b(rate[_ -]?limit|too many requests|quota|insufficient_quota)\b/.test(combined)) {
    return 'monitoring.error_category_rate_limit';
  }
  if (status === 400 || /\b(bad[_ -]?request|invalid[_ -]?request|validation)\b/.test(combined)) {
    return 'monitoring.error_category_bad_request';
  }
  if (status === 404 || /\b(model.*not.*found|not[_ -]?found|404)\b/.test(combined)) {
    return 'monitoring.error_category_not_found';
  }
  if (/\b(timeout|deadline|context canceled|connection reset|econnreset|network)\b/.test(combined)) {
    return 'monitoring.error_category_network';
  }
  if (status !== null && status >= 500) {
    return 'monitoring.error_category_upstream';
  }
  return row.failed ? 'monitoring.error_category_unknown' : 'monitoring.error_category_none';
};

const REALTIME_ERROR_TEXT_FALLBACKS = {
  en: {
    request_details: 'Request Details',
    request_details_click_hint: 'Click to view request details',
    error_details: 'Error Details',
    error_details_click_hint: 'Click to view error details',
    error_details_modal_desc: 'Only fields directly related to the failed request are shown here.',
    error_category: 'Error Category',
    error_category_none: 'No Error',
    error_category_auth: 'Auth / Permission',
    error_category_rate_limit: 'Rate Limit / Quota',
    error_category_bad_request: 'Bad Request',
    error_category_not_found: 'Not Found',
    error_category_network: 'Network / Timeout',
    error_category_upstream: 'Upstream Error',
    error_category_unknown: 'Unknown Error',
    http_status: 'HTTP Status',
    error_code: 'Error Code',
    error_message: 'Error Message',
    upstream_request_id: 'Upstream Request ID',
    retry_after: 'Retry After',
    copy_diagnostic: 'Copy Diagnostic',
    copy_diagnostic_success: 'Diagnostic copied',
    copy_diagnostic_failed: 'Unable to copy diagnostic',
    request_status: 'Request Status',
    filter_provider: 'Provider',
    column_model: 'Model',
    client_ip: 'Direct Client IP',
    x_forwarded_for: 'Forwarded-For Chain',
    user_agent: 'User Agent',
    attempt_index: 'Attempt Index',
    accounting_version: 'Accounting Version',
    accounting_quality: 'Accounting Quality',
    token_breakdown: 'Token Breakdown',
    request_context: 'Request Context',
    usage_context: 'Usage & Routing',
  },
  ru: {
    request_details: 'Детали запроса',
    request_details_click_hint: 'Нажмите, чтобы посмотреть детали запроса',
    error_details: 'Детали ошибки',
    error_details_click_hint: 'Нажмите, чтобы посмотреть детали ошибки',
    error_details_modal_desc: 'Здесь показаны только поля, напрямую связанные с ошибкой запроса.',
    error_category: 'Категория ошибки',
    error_category_none: 'Нет ошибки',
    error_category_auth: 'Авторизация / права',
    error_category_rate_limit: 'Лимит / квота',
    error_category_bad_request: 'Некорректный запрос',
    error_category_not_found: 'Не найдено',
    error_category_network: 'Сеть / тайм-аут',
    error_category_upstream: 'Ошибка upstream',
    error_category_unknown: 'Неизвестная ошибка',
    http_status: 'HTTP статус',
    error_code: 'Код ошибки',
    error_message: 'Сообщение ошибки',
    upstream_request_id: 'Upstream request ID',
    retry_after: 'Повторить после',
    copy_diagnostic: 'Скопировать диагностику',
    copy_diagnostic_success: 'Диагностика скопирована',
    copy_diagnostic_failed: 'Не удалось скопировать диагностику',
    request_status: 'Статус запроса',
    filter_provider: 'Провайдер',
    column_model: 'Модель',
    client_ip: 'Прямой IP клиента',
    x_forwarded_for: 'Цепочка Forwarded-For',
    user_agent: 'User Agent',
    attempt_index: 'Номер попытки',
    accounting_version: 'Версия учета',
    accounting_quality: 'Качество учета',
    token_breakdown: 'Разбивка токенов',
    request_context: 'Контекст запроса',
    usage_context: 'Использование и маршрутизация',
  },
  zhCN: {
    request_details: '请求详情',
    request_details_click_hint: '点击查看请求详情',
    error_details: '错误详情',
    error_details_click_hint: '点击查看错误详情',
    error_details_modal_desc: '这里只显示和本次请求失败直接相关的字段。',
    error_category: '错误类别',
    error_category_none: '无错误',
    error_category_auth: '鉴权 / 权限',
    error_category_rate_limit: '限流 / 配额',
    error_category_bad_request: '请求参数错误',
    error_category_not_found: '资源不存在',
    error_category_network: '网络 / 超时',
    error_category_upstream: '上游错误',
    error_category_unknown: '未知错误',
    http_status: 'HTTP 状态',
    error_code: '错误码',
    error_message: '错误信息',
    upstream_request_id: '上游请求 ID',
    retry_after: '重试等待',
    copy_diagnostic: '复制诊断',
    copy_diagnostic_success: '诊断信息已复制',
    copy_diagnostic_failed: '无法复制诊断信息',
    request_status: '请求状态',
    filter_provider: '提供商',
    column_model: '模型',
    client_ip: '直连客户端 IP',
    x_forwarded_for: '转发 IP 链',
    user_agent: '用户代理',
    attempt_index: '重试序号',
    accounting_version: '计费口径版本',
    accounting_quality: '计费数据质量',
    token_breakdown: 'Token 明细',
    request_context: '请求上下文',
    usage_context: '用量与路由',
  },
  zhTW: {
    request_details: '請求詳情',
    request_details_click_hint: '點擊查看請求詳情',
    error_details: '錯誤詳情',
    error_details_click_hint: '點擊查看錯誤詳情',
    error_details_modal_desc: '這裡只顯示與本次請求失敗直接相關的欄位。',
    error_category: '錯誤類別',
    error_category_none: '無錯誤',
    error_category_auth: '驗證 / 權限',
    error_category_rate_limit: '限流 / 配額',
    error_category_bad_request: '請求參數錯誤',
    error_category_not_found: '資源不存在',
    error_category_network: '網路 / 逾時',
    error_category_upstream: '上游錯誤',
    error_category_unknown: '未知錯誤',
    http_status: 'HTTP 狀態',
    error_code: '錯誤碼',
    error_message: '錯誤訊息',
    upstream_request_id: '上游請求 ID',
    retry_after: '重試等待',
    copy_diagnostic: '複製診斷',
    copy_diagnostic_success: '診斷資訊已複製',
    copy_diagnostic_failed: '無法複製診斷資訊',
    request_status: '請求狀態',
    filter_provider: '提供商',
    column_model: '模型',
    client_ip: '直連用戶端 IP',
    x_forwarded_for: '轉發 IP 鏈',
    user_agent: '使用者代理',
    attempt_index: '重試序號',
    accounting_version: '計費口徑版本',
    accounting_quality: '計費資料品質',
    token_breakdown: 'Token 明細',
    request_context: '請求上下文',
    usage_context: '用量與路由',
  },
} as const;

type RealtimeErrorTextKey = keyof typeof REALTIME_ERROR_TEXT_FALLBACKS.en;

const resolveRealtimeErrorFallbackLocale = (language?: string) => {
  const normalized = language?.toLowerCase() ?? '';
  if (normalized.startsWith('zh-tw') || normalized.startsWith('zh-hk') || normalized.startsWith('zh-mo')) return 'zhTW';
  if (normalized.startsWith('zh')) return 'zhCN';
  if (normalized.startsWith('ru')) return 'ru';
  return 'en';
};

export const translateRealtimeErrorText = (
  key: RealtimeErrorTextKey,
  t: TFunction,
  language?: string
) => {
  const fallbackLocale = resolveRealtimeErrorFallbackLocale(language);
  const fallback = REALTIME_ERROR_TEXT_FALLBACKS[fallbackLocale][key] ?? REALTIME_ERROR_TEXT_FALLBACKS.en[key];
  return t(`monitoring.${key}`, { defaultValue: fallback });
};

export const translateRealtimeErrorCategory = (
  key: string,
  t: TFunction,
  language?: string
) => {
  switch (key) {
    case 'monitoring.error_category_auth':
      return translateRealtimeErrorText('error_category_auth', t, language);
    case 'monitoring.error_category_rate_limit':
      return translateRealtimeErrorText('error_category_rate_limit', t, language);
    case 'monitoring.error_category_bad_request':
      return translateRealtimeErrorText('error_category_bad_request', t, language);
    case 'monitoring.error_category_not_found':
      return translateRealtimeErrorText('error_category_not_found', t, language);
    case 'monitoring.error_category_network':
      return translateRealtimeErrorText('error_category_network', t, language);
    case 'monitoring.error_category_upstream':
      return translateRealtimeErrorText('error_category_upstream', t, language);
    case 'monitoring.error_category_none':
      return translateRealtimeErrorText('error_category_none', t, language);
    case 'monitoring.error_category_unknown':
    default:
      return translateRealtimeErrorText('error_category_unknown', t, language);
  }
};

const buildRealtimeErrorSummary = (row: MonitoringEventRow) => {
  if (!row.failed) return '';
  const parts: string[] = [];
  if (row.errorMessage) parts.push(compactRealtimeErrorMessage(row.errorMessage));
  if (!row.errorMessage && row.errorCode) parts.push(maskSensitiveText(row.errorCode));
  if (row.upstreamRequestId) parts.push(`RID ${maskSensitiveText(row.upstreamRequestId)}`);
  if (row.retryAfter) parts.push(`Retry ${maskSensitiveText(row.retryAfter)}`);
  return parts.join(' · ');
};

export const buildRealtimeDiagnosticClipboardText = (
  row: RealtimeLogRow,
  t: TFunction,
  language?: string
) => {
  const fields: Array<[string, string | number | null | undefined]> = [
    [translateRealtimeErrorText('request_status', t, language), row.failed ? t('monitoring.result_failed') : t('monitoring.result_success')],
    [translateRealtimeErrorText('error_category', t, language), translateRealtimeErrorCategory(row.errorCategoryKey, t, language)],
    [translateRealtimeErrorText('http_status', t, language), row.statusCode ?? '-'],
    [translateRealtimeErrorText('error_code', t, language), row.errorCode || '-'],
    [translateRealtimeErrorText('error_message', t, language), row.errorMessage ? compactRealtimeErrorMessage(row.errorMessage, 800) : '-'],
    [translateRealtimeErrorText('upstream_request_id', t, language), row.upstreamRequestId || '-'],
    [translateRealtimeErrorText('retry_after', t, language), row.retryAfter || '-'],
    [translateRealtimeErrorText('filter_provider', t, language), row.provider || '-'],
    [translateRealtimeErrorText('column_model', t, language), row.model || '-'],
    [translateRealtimeErrorText('client_ip', t, language), row.clientIP || '-'],
    [translateRealtimeErrorText('x_forwarded_for', t, language), row.xForwardedFor || '-'],
    [translateRealtimeErrorText('user_agent', t, language), row.userAgent || '-'],
    [translateRealtimeErrorText('attempt_index', t, language), row.attemptIndex ?? '-'],
    [translateRealtimeErrorText('accounting_version', t, language), row.accountingVersion ?? '-'],
    [translateRealtimeErrorText('accounting_quality', t, language), row.accountingQuality || '-'],
    [translateRealtimeErrorText('token_breakdown', t, language), formatRealtimeTokenBreakdown(row.tokenBreakdown) || '-'],
    [t('monitoring.cost_detail_requested_tier'), row.serviceTier || '-'],
    [t('monitoring.cost_detail_actual_tier'), row.effectiveServiceTier || '-'],
  ];
  fields.push(...modelAuditItems(row, t).map(({ label, value }): [string, string] => [label, value]));
  return fields.map(([label, value]) => `${label}: ${maskSensitiveText(String(value ?? '-'))}`).join('\n');
};

export const buildRealtimeLogPageRows = (
  rows: MonitoringEventRow[],
  page: number,
  pageSize: number
): { total: number; rows: RealtimeLogRow[] } => {
  const candidateRows = rows;
  const metricsByStream = new Map<string, { total: number; success: number; pattern: boolean[] }>();
  const normalizedPage = Math.max(1, page);
  const pageStart = (normalizedPage - 1) * pageSize;
  const pageEnd = Math.min(pageStart + pageSize, candidateRows.length);
  const enriched = new Array<RealtimeLogRow>(Math.max(pageEnd - pageStart, 0));

  for (let index = candidateRows.length - 1; index >= 0; index -= 1) {
    const row = candidateRows[index];
    const streamKey = [row.account, row.provider, row.model, row.modelAlias, row.channel].join('::');
    const previous = metricsByStream.get(streamKey) ?? { total: 0, success: 0, pattern: [] };
    const nextPattern = [...previous.pattern, !row.failed].slice(-10);
    const next = {
      total: previous.total + (row.statsIncluded ? 1 : 0),
      success: previous.success + (row.statsIncluded && !row.failed ? 1 : 0),
      pattern: nextPattern,
    };
    metricsByStream.set(streamKey, next);

    if (index >= pageStart && index < pageEnd) {
      let recentSuccessCount = 0;
      nextPattern.forEach((item) => {
        if (item) recentSuccessCount += 1;
      });
      enriched[index - pageStart] = {
        ...row,
        streamKey,
        diagnosticText: buildRealtimeDiagnosticText(row),
        errorCategoryKey: resolveRealtimeErrorCategoryKey(row),
        errorSummary: buildRealtimeErrorSummary(row),
        requestCount: next.total,
        successRate: next.total > 0 ? next.success / next.total : 1,
        recentPattern: nextPattern,
        recentSuccessCount,
        recentFailureCount: nextPattern.length - recentSuccessCount,
      };
    }
  }

  return { total: candidateRows.length, rows: enriched };
};

export const getClientPaginationRange = (
  page: number,
  pageSize: number,
  total: number,
  visibleCount: number
) => {
  const normalizedPage = Math.max(1, page);
  const totalPages = total > 0 ? Math.ceil(total / pageSize) : 0;
  const from = total > 0 && visibleCount > 0 ? (normalizedPage - 1) * pageSize + 1 : 0;
  return {
    page: normalizedPage,
    total,
    totalPages,
    from,
    to: visibleCount > 0 ? Math.min(total, from + visibleCount - 1) : 0,
    hasPrevious: normalizedPage > 1,
    hasNext: normalizedPage < totalPages,
  };
};
