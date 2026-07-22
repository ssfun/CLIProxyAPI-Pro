#!/usr/bin/env python3
import hashlib
import json
import shutil
import sys
from pathlib import Path

CUSTOMIZATION_DIR = Path(__file__).resolve().parent
OVERLAY_DIR = CUSTOMIZATION_DIR / 'overlay'
LOCALES_FILE = CUSTOMIZATION_DIR / 'monitoring-locales.json'
OVERLAY_REPLACEMENTS_FILE = CUSTOMIZATION_DIR / 'overlay-replacements.json'

QUOTA_LOCALE_KEYS = {
    'en.json': {
        'cached_at': 'Updated',
        'just_now': 'Just now',
        'minutes_ago': '{{count}} minute ago',
        'minutes_ago_plural': '{{count}} minutes ago',
        'hours_ago': '{{count}} hour ago',
        'hours_ago_plural': '{{count}} hours ago',
        'days_ago': '{{count}} day ago',
        'days_ago_plural': '{{count}} days ago',
        'search_label': 'Search quota credentials',
        'search_placeholder': 'Search config name, auth_index, type, provider, note, or plan. Use * as a wildcard',
        'no_search_results': 'No matching quota credentials',
        'no_search_results_desc': 'No quota credential matches the current search.',
    },
    'ru.json': {
        'cached_at': 'Обновлено',
        'just_now': 'Только что',
        'minutes_ago': '{{count}} минуту назад',
        'minutes_ago_plural': '{{count}} минут назад',
        'hours_ago': '{{count}} час назад',
        'hours_ago_plural': '{{count}} часов назад',
        'days_ago': '{{count}} день назад',
        'days_ago_plural': '{{count}} дней назад',
        'search_label': 'Поиск конфигураций квот',
        'search_placeholder': 'Поиск по имени, auth_index, типу, провайдеру, заметке или тарифу; поддерживается *',
        'no_search_results': 'Подходящие конфигурации квот не найдены',
        'no_search_results_desc': 'Текущему запросу не соответствует ни одна конфигурация квот.',
    },
    'zh-CN.json': {
        'cached_at': '更新于',
        'just_now': '刚刚',
        'minutes_ago': '{{count}} 分钟前',
        'hours_ago': '{{count}} 小时前',
        'days_ago': '{{count}} 天前',
        'search_label': '搜索配额配置文件',
        'search_placeholder': '搜索配置文件名称、auth_index、类型、提供商、备注或套餐，支持 * 通配',
        'no_search_results': '没有匹配的配额配置文件',
        'no_search_results_desc': '当前搜索条件下没有可显示的配额配置文件。',
    },
    'zh-TW.json': {
        'cached_at': '更新於',
        'just_now': '剛剛',
        'minutes_ago': '{{count}} 分鐘前',
        'hours_ago': '{{count}} 小時前',
        'days_ago': '{{count}} 天前',
        'search_label': '搜尋配額設定檔',
        'search_placeholder': '搜尋設定檔名稱、auth_index、類型、供應商、備註或套餐，支援 * 萬用字元',
        'no_search_results': '沒有符合的配額設定檔',
        'no_search_results_desc': '目前搜尋條件下沒有可顯示的配額設定檔。',
    },
}

GEMINI_CLI_LOCALE_KEYS = {
    'en.json': {
        'auth_filter': 'GeminiCLI',
        'quota': {
            'title': 'Gemini CLI Quota',
            'empty_title': 'No Gemini CLI Auth Files',
            'empty_desc': 'Upload a Gemini CLI credential to view remaining quota.',
            'idle': 'Click here to refresh quota',
            'loading': 'Loading quota...',
            'load_failed': 'Failed to load quota: {{message}}',
            'missing_auth_index': 'Auth file missing auth_index',
            'missing_project_id': 'Gemini CLI credential missing project ID',
            'empty_buckets': 'No quota data available',
            'remaining_amount': 'Remaining {{count}}',
            'tier_label': 'Tier',
            'tier_free': 'Gemini Code Assist Free',
            'tier_legacy': 'Gemini Code Assist Legacy',
            'tier_standard': 'Gemini Code Assist Standard',
            'tier_pro': 'Google AI Pro',
            'tier_ultra': 'Google AI Ultra',
            'credit_label': 'Google One AI Credits',
            'credit_amount': '{{count}} credits',
        },
    },
    'ru.json': {
        'auth_filter': 'GeminiCLI',
        'quota': {
            'title': 'Квота Gemini CLI',
            'empty_title': 'Файлы авторизации Gemini CLI отсутствуют',
            'empty_desc': 'Загрузите учётные данные Gemini CLI, чтобы увидеть оставшуюся квоту.',
            'idle': 'Не загружено. Нажмите "Обновить квоту".',
            'loading': 'Загрузка квоты...',
            'load_failed': 'Не удалось загрузить квоту: {{message}}',
            'missing_auth_index': 'В файле авторизации отсутствует auth_index',
            'missing_project_id': 'В учётных данных Gemini CLI отсутствует идентификатор проекта',
            'empty_buckets': 'Данные по квоте отсутствуют',
            'remaining_amount': 'Осталось {{count}}',
            'tier_label': 'Уровень',
            'tier_free': 'Gemini Code Assist Free',
            'tier_legacy': 'Gemini Code Assist Legacy',
            'tier_standard': 'Gemini Code Assist Standard',
            'tier_pro': 'Google AI Pro',
            'tier_ultra': 'Google AI Ultra',
            'credit_label': 'Google One AI кредиты',
            'credit_amount': '{{count}} кредитов',
        },
    },
    'zh-CN.json': {
        'auth_filter': 'GeminiCLI',
        'quota': {
            'title': 'Gemini CLI 额度',
            'empty_title': '暂无 Gemini CLI 认证',
            'empty_desc': '上传 Gemini CLI 认证文件后即可查看额度。',
            'idle': '点击此处刷新额度',
            'loading': '正在加载额度...',
            'load_failed': '额度获取失败：{{message}}',
            'missing_auth_index': '认证文件缺少 auth_index',
            'missing_project_id': 'Gemini CLI 凭证缺少 Project ID',
            'empty_buckets': '暂无额度数据',
            'remaining_amount': '剩余 {{count}}',
            'tier_label': '层级',
            'tier_free': 'Gemini Code Assist 免费版',
            'tier_legacy': 'Gemini Code Assist Legacy',
            'tier_standard': 'Gemini Code Assist Standard',
            'tier_pro': 'Google AI Pro',
            'tier_ultra': 'Google AI Ultra',
            'credit_label': 'Google One AI 积分',
            'credit_amount': '{{count}} 积分',
        },
    },
    'zh-TW.json': {
        'auth_filter': 'GeminiCLI',
        'quota': {
            'title': 'Gemini CLI 配額',
            'empty_title': '暫無 Gemini CLI 驗證',
            'empty_desc': '上傳 Gemini CLI 驗證檔案後即可查看配額。',
            'idle': '點擊此處重新整理配額',
            'loading': '正在載入配額...',
            'load_failed': '配額取得失敗：{{message}}',
            'missing_auth_index': '驗證檔案缺少 auth_index',
            'missing_project_id': 'Gemini CLI 憑證缺少 Project ID',
            'empty_buckets': '暫無配額資料',
            'remaining_amount': '剩餘 {{count}}',
            'tier_label': '層級',
            'tier_free': 'Gemini Code Assist 免費版',
            'tier_legacy': 'Gemini Code Assist Legacy',
            'tier_standard': 'Gemini Code Assist Standard',
            'tier_pro': 'Google AI Pro',
            'tier_ultra': 'Google AI Ultra',
            'credit_label': 'Google One AI 點數',
            'credit_amount': '{{count}} 點數',
        },
    },
}

AUTH_FILES_SEARCH_PLACEHOLDER_KEYS = {
    'en.json': 'Filter by name, auth_index, type, provider, note, or plan. Use * as a wildcard',
    'ru.json': 'Фильтр по имени, auth_index, типу, провайдеру, заметке или тарифу, поддерживается wildcard *',
    'zh-CN.json': '输入名称、auth_index、类型、提供方、备注或套餐关键字，支持 * 通配',
    'zh-TW.json': '輸入名稱、auth_index、類型、供應方、備註或套餐關鍵字，支援 * 萬用字元',
}

AUTH_FILES_PLAN_SORT_LABEL_KEYS = {
    'en.json': 'Plan: High to Low',
    'ru.json': 'Тариф: по убыванию',
    'zh-CN.json': '套餐从高到低',
    'zh-TW.json': '套餐由高到低',
}

AUTH_FILES_QUOTA_SORT_LABEL_KEYS = {
    'en.json': 'Available Quota: High to Low',
    'ru.json': 'Доступная квота: по убыванию',
    'zh-CN.json': '可用额度从高到低',
    'zh-TW.json': '可用額度由高到低',
}

AUTH_FILES_SELECTED_COUNT_LABEL_KEYS = {
    'en.json': 'Scheduled',
    'ru.json': 'Назначено',
    'zh-CN.json': '调度',
    'zh-TW.json': '調度',
}

CLAUDE_MODEL_ID_CLOAK_LOCALE_KEYS = {
    'en.json': {
        'title': 'Anthropic Client Compatibility',
        'description': 'Control how non-Claude model IDs are exposed to Anthropic-compatible clients.',
        'label': 'Anthropic model ID compatibility mode',
        'hint': 'Only changes non-Claude IDs returned by /v1/models. Auto cloaks IDs for identified Claude Desktop clients, while Claude Code and other Anthropic clients keep the original IDs.',
        'auto': 'Auto (Claude Desktop only)',
        'always': 'Always cloak IDs',
        'never': 'Keep original IDs',
    },
    'ru.json': {
        'title': 'Совместимость клиентов Anthropic',
        'description': 'Управление отображением идентификаторов моделей не-Claude для Anthropic-совместимых клиентов.',
        'label': 'Режим совместимости идентификаторов моделей Anthropic',
        'hint': 'Изменяет только идентификаторы моделей не-Claude в ответе /v1/models. Автоматический режим маскирует их только для распознанного Claude Desktop; Claude Code и другие клиенты Anthropic получают исходные идентификаторы.',
        'auto': 'Авто (только Claude Desktop)',
        'always': 'Всегда маскировать',
        'never': 'Сохранять исходные ID',
    },
    'zh-CN.json': {
        'title': 'Anthropic 客户端兼容性',
        'description': '控制非 Claude 模型 ID 向 Anthropic 兼容客户端的展示方式。',
        'label': 'Anthropic 模型 ID 兼容模式',
        'hint': '仅影响 /v1/models 返回的非 Claude 模型 ID。自动模式仅对识别出的 Claude Desktop 进行伪装，Claude Code 和其他 Anthropic 客户端保留原始 ID。',
        'auto': '自动（仅 Claude Desktop）',
        'always': '始终伪装 ID',
        'never': '保留原始 ID',
    },
    'zh-TW.json': {
        'title': 'Anthropic 用戶端相容性',
        'description': '控制非 Claude 模型 ID 對 Anthropic 相容用戶端的顯示方式。',
        'label': 'Anthropic 模型 ID 相容模式',
        'hint': '僅影響 /v1/models 回傳的非 Claude 模型 ID。自動模式只對識別出的 Claude Desktop 進行偽裝，Claude Code 和其他 Anthropic 用戶端保留原始 ID。',
        'auto': '自動（僅 Claude Desktop）',
        'always': '一律偽裝 ID',
        'never': '保留原始 ID',
    },
}

def load_overlay_replacement_manifest(path: Path) -> dict[str, set[str]]:
    payload = json.loads(path.read_text())
    if payload.get('schemaVersion') != 1 or not isinstance(payload.get('replacements'), list):
        raise RuntimeError(f'Invalid overlay replacement manifest: {path}')

    upstream_hashes: dict[str, set[str]] = {}
    for entry in payload['replacements']:
        if not isinstance(entry, dict):
            raise RuntimeError(f'Invalid overlay replacement entry: {entry!r}')
        relative_path = entry.get('path')
        upstream = entry.get('upstreamSha256')
        if (
            not isinstance(relative_path, str)
            or not relative_path
            or Path(relative_path).is_absolute()
            or '..' in Path(relative_path).parts
            or relative_path in upstream_hashes
            or not isinstance(upstream, list)
            or not upstream
            or not all(isinstance(item, str) and len(item) == 64 for item in upstream)
        ):
            raise RuntimeError(f'Invalid overlay replacement entry: {entry!r}')
        upstream_hashes[relative_path] = set(upstream)
    return upstream_hashes


OVERLAY_REPLACEMENT_HASHES = load_overlay_replacement_manifest(OVERLAY_REPLACEMENTS_FILE)


_writes = {}


def read(path: Path) -> str:
    if path in _writes:
        return _writes[path]
    return path.read_text()


def write(path: Path, text: str) -> None:
    _writes[path] = text


def flush_writes() -> None:
    for path, text in _writes.items():
        path.write_text(text)


def replace_once(path: Path, old: str, new: str) -> None:
    text = read(path)
    if new in text:
        return
    match_count = text.count(old)
    if match_count != 1:
        raise RuntimeError(f'Expected one pattern in {path}, found {match_count}: {old[:120]!r}')
    write(path, text.replace(old, new, 1))


def replace_once_if_present(path: Path, old: str, new: str) -> None:
    text = read(path)
    if new in text:
        return
    match_count = text.count(old)
    if match_count == 0:
        return
    if match_count != 1:
        raise RuntimeError(f'Expected at most one pattern in {path}, found {match_count}: {old[:120]!r}')
    write(path, text.replace(old, new, 1))


def replace_all(path: Path, old: str, new: str) -> None:
    text = read(path)
    if old not in text:
        return
    write(path, text.replace(old, new))


def replace_once_in_quota_config(path: Path, store_setter: str, old: str, new: str) -> None:
    text = read(path)
    marker = f"  storeSetter: '{store_setter}',"
    marker_start = text.find(marker)
    if marker_start == -1:
        raise RuntimeError(f'Pattern not found in {path}: {marker!r}')

    success_start = text.find('  buildSuccessState:', marker_start)
    error_start = text.find('  buildErrorState:', success_start)
    if success_start == -1 or error_start == -1:
        raise RuntimeError(f'Pattern not found in {path}: buildSuccessState block for {store_setter}')

    block = text[success_start:error_start]
    if new in block:
        return
    if old not in block:
        raise RuntimeError(f'Pattern not found in {path}: {old[:120]!r}')

    updated = block.replace(old, new, 1)
    write(path, f'{text[:success_start]}{updated}{text[error_start:]}')


def ensure_cached_at_in_quota_success_state(path: Path, store_setter: str) -> None:
    text = read(path)
    marker = f"  storeSetter: '{store_setter}',"
    marker_start = text.find(marker)
    if marker_start == -1:
        raise RuntimeError(f'Pattern not found in {path}: {marker!r}')

    success_start = text.find('  buildSuccessState:', marker_start)
    error_start = text.find('  buildErrorState:', success_start)
    if success_start == -1 or error_start == -1:
        raise RuntimeError(f'Pattern not found in {path}: buildSuccessState block for {store_setter}')

    block = text[success_start:error_start]
    if 'cachedAt:' in block:
        return

    multiline_end = '\n  }),'
    if multiline_end in block:
        updated = block.replace(multiline_end, '\n    cachedAt: Date.now(),\n  }),', 1)
    else:
        inline_end = '}),'
        inline_end_start = block.rfind(inline_end)
        if inline_end_start == -1:
            raise RuntimeError(f'Pattern not found in {path}: buildSuccessState return end for {store_setter}')
        updated = f'{block[:inline_end_start].rstrip()}, cachedAt: Date.now() {block[inline_end_start:]}'

    write(path, f'{text[:success_start]}{updated}{text[error_start:]}')


def insert_once(path: Path, marker: str, insertion: str, present: str) -> None:
    text = read(path)
    if present in text:
        return
    match_count = text.count(marker)
    if match_count != 1:
        raise RuntimeError(f'Expected one marker in {path}, found {match_count}: {marker[:120]!r}')
    write(path, text.replace(marker, insertion, 1))


def validate_overlay_collisions(target: Path) -> None:
    for src in OVERLAY_DIR.rglob('*'):
        if src.is_dir():
            continue
        rel = src.relative_to(OVERLAY_DIR)
        dst = target / rel
        if not dst.is_file():
            continue
        source_digest = hashlib.sha256(src.read_bytes()).hexdigest()
        target_digest = hashlib.sha256(dst.read_bytes()).hexdigest()
        if target_digest == source_digest:
            continue
        allowed_hashes = OVERLAY_REPLACEMENT_HASHES.get(rel.as_posix())
        if allowed_hashes is None:
            raise RuntimeError(f'Unexpected overlay collision with upstream file: {dst}')
        if target_digest not in allowed_hashes:
            raise RuntimeError(f'Upstream overlay replacement changed: {dst} ({target_digest})')


def copy_overlay(target: Path) -> None:
    validate_overlay_collisions(target)
    for src in OVERLAY_DIR.rglob('*'):
        rel = src.relative_to(OVERLAY_DIR)
        dst = target / rel
        if src.is_dir():
            dst.mkdir(parents=True, exist_ok=True)
        else:
            dst.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(src, dst)


def patch_modal_focus_restore(target: Path) -> None:
    path = target / 'src/components/ui/Modal.tsx'
    replace_once(
        path,
        "  useEffect(() => {\n"
        "    if (open || isVisible) return;\n"
        "    previouslyFocusedRef.current?.focus();\n"
        "    previouslyFocusedRef.current = null;\n"
        "  }, [isVisible, open]);\n",
        "  useEffect(() => {\n"
        "    if (open || isVisible) return;\n"
        "    const previouslyFocused = previouslyFocusedRef.current;\n"
        "    if (previouslyFocused?.isConnected) {\n"
        "      previouslyFocused.focus({ preventScroll: true });\n"
        "    }\n"
        "    previouslyFocusedRef.current = null;\n"
        "  }, [isVisible, open]);\n",
    )


def patch_modal_scroll_lock(target: Path) -> None:
    path = target / 'src/components/ui/scrollLock.ts'
    text = read(path)
    replacement_marker = "  locksDocumentScroll: false,"
    if replacement_marker in text:
        return

    start_marker = 'const snapshot = {'
    end_marker = 'export const FOCUSABLE_SELECTOR'
    start = text.find(start_marker)
    end = text.find(end_marker, start)
    if start == -1 or end == -1:
        raise RuntimeError(f'Pattern not found in {path}: scroll lock implementation')

    current = text[start:end]
    upstream_markers = (
        "body.style.position = 'fixed';",
        "body.style.width = '100%';",
        'contentEl.scrollTo(',
        'window.scrollTo(',
    )
    previous_patch_markers = (
        "  bodyOverflow: '',",
        "  htmlOverflow: '',",
        "    body.style.overflow = 'hidden';",
        "    html.style.overflow = 'hidden';",
    )
    if not all(marker in current for marker in upstream_markers) and not all(
        marker in current for marker in previous_patch_markers
    ):
        raise RuntimeError(f'Pattern not found in {path}: supported scroll lock implementation')

    replacement = (
        "const snapshot = {\n"
        "  scrollX: 0,\n"
        "  scrollY: 0,\n"
        "  locksDocumentScroll: false,\n"
        "  bodyPosition: '',\n"
        "  bodyTop: '',\n"
        "  bodyLeft: '',\n"
        "  bodyRight: '',\n"
        "  bodyWidth: '',\n"
        "  bodyOverflow: '',\n"
        "  htmlOverflow: '',\n"
        "};\n\n"
        "export function lockScroll(): void {\n"
        "  if (typeof document === 'undefined') return;\n"
        "  if (activeLockCount === 0) {\n"
        "    const body = document.body;\n"
        "    const html = document.documentElement;\n\n"
        "    const scrollingElement = document.scrollingElement;\n"
        "    snapshot.scrollX = window.scrollX || window.pageXOffset || 0;\n"
        "    snapshot.scrollY = window.scrollY || window.pageYOffset || scrollingElement?.scrollTop || 0;\n"
        "    snapshot.locksDocumentScroll = Boolean(\n"
        "      scrollingElement && scrollingElement.scrollHeight > scrollingElement.clientHeight + 1\n"
        "    );\n"
        "    snapshot.bodyPosition = body.style.position;\n"
        "    snapshot.bodyTop = body.style.top;\n"
        "    snapshot.bodyLeft = body.style.left;\n"
        "    snapshot.bodyRight = body.style.right;\n"
        "    snapshot.bodyWidth = body.style.width;\n"
        "    snapshot.bodyOverflow = body.style.overflow;\n"
        "    snapshot.htmlOverflow = html.style.overflow;\n\n"
        "    body.classList.add(MODAL_LOCK_CLASS);\n"
        "    html.classList.add(MODAL_LOCK_CLASS);\n"
        "    if (snapshot.locksDocumentScroll) {\n"
        "      body.style.position = 'fixed';\n"
        "      body.style.top = `-${snapshot.scrollY}px`;\n"
        "      body.style.left = '0';\n"
        "      body.style.right = '0';\n"
        "      body.style.width = '100%';\n"
        "    }\n"
        "    body.style.overflow = 'hidden';\n"
        "    html.style.overflow = 'hidden';\n"
        "  }\n"
        "  activeLockCount += 1;\n"
        "}\n\n"
        "export function unlockScroll(): void {\n"
        "  if (typeof document === 'undefined') return;\n"
        "  activeLockCount = Math.max(0, activeLockCount - 1);\n"
        "  if (activeLockCount === 0) {\n"
        "    const body = document.body;\n"
        "    const html = document.documentElement;\n"
        "    const scrollX = snapshot.scrollX;\n"
        "    const scrollY = snapshot.scrollY;\n"
        "    const restoreDocumentScroll = snapshot.locksDocumentScroll;\n\n"
        "    body.classList.remove(MODAL_LOCK_CLASS);\n"
        "    html.classList.remove(MODAL_LOCK_CLASS);\n"
        "    body.style.position = snapshot.bodyPosition;\n"
        "    body.style.top = snapshot.bodyTop;\n"
        "    body.style.left = snapshot.bodyLeft;\n"
        "    body.style.right = snapshot.bodyRight;\n"
        "    body.style.width = snapshot.bodyWidth;\n"
        "    body.style.overflow = snapshot.bodyOverflow;\n"
        "    html.style.overflow = snapshot.htmlOverflow;\n"
        "\n"
        "    if (restoreDocumentScroll) {\n"
        "      window.scrollTo({ top: scrollY, left: scrollX, behavior: 'auto' });\n"
        "    }\n"
        "    snapshot.scrollX = 0;\n"
        "    snapshot.scrollY = 0;\n"
        "    snapshot.locksDocumentScroll = false;\n"
        "  }\n"
        "}\n\n"
    )
    write(path, f'{text[:start]}{replacement}{text[end:]}')


def patch_modal_content_scrollbar_layout(target: Path) -> None:
    path = target / 'src/styles/global.scss'
    text = read(path)
    content_lock = "body.modal-open .content {\n  overflow: hidden;\n}\n\n"
    if content_lock in text:
        write(path, text.replace(content_lock, '', 1))
        return
    if 'body.modal-open .content' in text:
        raise RuntimeError(f'Pattern not found in {path}: modal content scroll lock')


def patch_api_client_connection_isolation(target: Path) -> None:
    client = target / 'src/services/api/client.ts'
    replace_once(
        client,
        "  private runtimeKind: ServerRuntimeKind = 'unknown';\n",
        "  private runtimeKind: ServerRuntimeKind = 'unknown';\n"
        "  private connectionGeneration: number = 0;\n"
        "  private connectionAbortController = new AbortController();\n",
    )
    replace_once(
        client,
        "    if (connectionChanged) {\n"
        "      this.runtimeKind = 'unknown';\n"
        "    }\n",
        "    if (connectionChanged) {\n"
        "      this.connectionAbortController.abort();\n"
        "      this.connectionAbortController = new AbortController();\n"
        "      this.connectionGeneration += 1;\n"
        "      this.runtimeKind = 'unknown';\n"
        "    }\n",
    )
    insert_once(
        client,
        "  /**\n   * 设置请求/响应拦截器\n   */\n",
        "  private combineRequestSignal(requestSignal: AxiosRequestConfig['signal']): AbortSignal {\n"
        "    const connectionSignal = this.connectionAbortController.signal;\n"
        "    if (!requestSignal) return connectionSignal;\n"
        "    const callerSignal = requestSignal as AbortSignal;\n"
        "    if (callerSignal === connectionSignal) return connectionSignal;\n"
        "    if (typeof AbortSignal.any === 'function') {\n"
        "      return AbortSignal.any([callerSignal, connectionSignal]);\n"
        "    }\n"
        "    const controller = new AbortController();\n"
        "    const abort = () => controller.abort();\n"
        "    if (callerSignal.aborted || connectionSignal.aborted) {\n"
        "      abort();\n"
        "    } else {\n"
        "      callerSignal.addEventListener('abort', abort, { once: true });\n"
        "      connectionSignal.addEventListener('abort', abort, { once: true });\n"
        "    }\n"
        "    return controller.signal;\n"
        "  }\n\n"
        "  private isStaleConnection(config: AxiosRequestConfig | undefined): boolean {\n"
        "    const generation = (config as AxiosRequestConfig & { __connectionGeneration?: number } | undefined)\n"
        "      ?.__connectionGeneration;\n"
        "    return typeof generation === 'number' && generation !== this.connectionGeneration;\n"
        "  }\n\n"
        "  private staleConnectionError(): Error {\n"
        "    return new axios.CanceledError('Connection changed while the request was in flight');\n"
        "  }\n\n"
        "  /**\n   * 设置请求/响应拦截器\n   */\n",
        'private isStaleConnection(config: AxiosRequestConfig | undefined)',
    )
    replace_once(
        client,
        "      (config) => {\n"
        "        // 设置 baseURL\n"
        "        config.baseURL = this.apiBase;\n",
        "      (config) => {\n"
        "        (config as AxiosRequestConfig & { __connectionGeneration?: number })\n"
        "          .__connectionGeneration = this.connectionGeneration;\n"
        "        config.signal = this.combineRequestSignal(config.signal);\n"
        "        // 设置 baseURL\n"
        "        config.baseURL = this.apiBase;\n",
    )
    replace_once(
        client,
        "      (response) => {\n"
        "        const headers = response.headers as Record<string, string | undefined>;\n",
        "      (response) => {\n"
        "        if (this.isStaleConnection(response.config)) {\n"
        "          throw this.staleConnectionError();\n"
        "        }\n"
        "        const headers = response.headers as Record<string, string | undefined>;\n",
    )
    replace_once(
        client,
        "        return response;\n"
        "      },\n"
        "      (error) => Promise.reject(this.handleError(error))\n"
        "    );\n",
        "        return response;\n"
        "      },\n"
        "      (error) => {\n"
        "        if (axios.isAxiosError(error) && this.isStaleConnection(error.config)) {\n"
        "          return Promise.reject(this.staleConnectionError());\n"
        "        }\n"
        "        return Promise.reject(this.handleError(error));\n"
        "      }\n"
        "    );\n",
    )

    auth_store = target / 'src/stores/useAuthStore.ts'
    replace_once(
        auth_store,
        "        useQuotaStore.getState().clearQuotaCache();\n"
        "        set({\n"
        "          isAuthenticated: false,\n",
        "        useQuotaStore.getState().clearQuotaCache();\n"
        "        apiClient.setConfig({ apiBase: '', managementKey: '' });\n"
        "        set({\n"
        "          isAuthenticated: false,\n",
    )


def patch_routes(target: Path) -> None:
    path = target / 'src/router/MainRoutes.tsx'
    replace_once(
        path,
        "import { QuotaPage } from '@/pages/QuotaPage';\n",
        "import { QuotaPage } from '@/pages/QuotaPage';\nimport { MonitoringCenterPage } from '@/pages/MonitoringCenterPage';\nimport { AccountInspectionPage } from '@/pages/AccountInspectionPage';\nimport { RoutingPolicyPage } from '@/pages/RoutingPolicyPage';\n",
    )
    replace_once(
        path,
        "  { path: '/quota', element: <QuotaPage /> },\n",
        "  { path: '/quota', element: <QuotaPage /> },\n  { path: '/monitoring', element: <MonitoringCenterPage /> },\n  { path: '/account-inspection', element: <AccountInspectionPage /> },\n  { path: '/routing', element: <RoutingPolicyPage /> },\n",
    )


def patch_layout(target: Path) -> None:
    path = target / 'src/components/layout/MainLayout.tsx'
    insert_once(
        path,
        "import {\n  IconSidebar",
        "import { QuotaPersistenceBootstrap } from '@/extensions/quota/QuotaPersistenceBootstrap';\nimport {\n  IconSidebar",
        "QuotaPersistenceBootstrap",
    )
    insert_once(
        path,
        "  IconSidebarProviders,\n",
        "  IconSidebarAccountInspection,\n  IconSidebarMonitor,\n  IconSidebarRouting,\n  IconSidebarProviders,\n",
        "  IconSidebarAccountInspection,\n",
    )
    replace_once(
        path,
        "  oauth: <IconSidebarOauth size={18} />,\n  quota: <IconSidebarQuota size={18} />,\n",
        "  oauth: <IconSidebarOauth size={18} />,\n  quota: <IconSidebarQuota size={18} />,\n  monitoring: <IconSidebarMonitor size={18} />,\n  accountInspection: <IconSidebarAccountInspection size={18} />,\n  routing: <IconSidebarRouting size={18} />,\n",
    )
    text = read(path)
    if "path: '/monitoring'" not in text:
        flat_quota_item = "    { path: '/quota', label: t('nav.quota_management'), icon: sidebarIcons.quota },\n"
        grouped_quota_item = (
            "        {\n"
            "          path: '/quota',\n"
            "          labelKey: 'nav.quota_management',\n"
            "          metaKey: 'nav_meta.quota_management',\n"
            "          icon: sidebarIcons.quota,\n"
            "        },\n"
        )
        if flat_quota_item in text:
            write(
                path,
                text.replace(
                    flat_quota_item,
                    flat_quota_item
                    + "    { path: '/monitoring', label: t('nav.monitoring_center'), icon: sidebarIcons.monitoring },\n"
                    + "    { path: '/account-inspection', label: t('nav.account_inspection'), icon: sidebarIcons.accountInspection },\n",
                    1,
                ),
            )
        elif grouped_quota_item in text:
            write(
                path,
                text.replace(
                    grouped_quota_item,
                    grouped_quota_item
                    + "        {\n"
                    + "          path: '/monitoring',\n"
                    + "          labelKey: 'nav.monitoring_center',\n"
                    + "          metaKey: 'nav_meta.monitoring_center',\n"
                    + "          icon: sidebarIcons.monitoring,\n"
                    + "        },\n"
                    + "        {\n"
                    + "          path: '/account-inspection',\n"
                    + "          labelKey: 'nav.account_inspection',\n"
                    + "          metaKey: 'nav_meta.account_inspection',\n"
                    + "          icon: sidebarIcons.accountInspection,\n"
                    + "        },\n",
                    1,
                ),
            )
        else:
            raise RuntimeError(f'Pattern not found in {path}: quota navigation item')
    replace_once_if_present(
        path,
        "        {\n"
        "          path: '/account-inspection',\n"
        "          labelKey: 'nav.account_inspection',\n"
        "          metaKey: 'nav_meta.account_inspection',\n"
        "          icon: sidebarIcons.monitoring,\n"
        "        },\n",
        "        {\n"
        "          path: '/account-inspection',\n"
        "          labelKey: 'nav.account_inspection',\n"
        "          metaKey: 'nav_meta.account_inspection',\n"
        "          icon: sidebarIcons.accountInspection,\n"
        "        },\n",
    )
    replace_once_if_present(
        path,
        "    { path: '/account-inspection', label: t('nav.account_inspection'), icon: sidebarIcons.monitoring },\n",
        "    { path: '/account-inspection', label: t('nav.account_inspection'), icon: sidebarIcons.accountInspection },\n",
    )
    flat_routing_item = (
        "    { path: '/routing', label: t('nav.routing_policy'), icon: sidebarIcons.routing },\n"
    )
    flat_account_inspection_item = (
        "    { path: '/account-inspection', label: t('nav.account_inspection'), icon: sidebarIcons.accountInspection },\n"
    )
    grouped_routing_item = (
        "        {\n"
        "          path: '/routing',\n"
        "          labelKey: 'nav.routing_policy',\n"
        "          metaKey: 'nav_meta.routing_policy',\n"
        "          icon: sidebarIcons.routing,\n"
        "        },\n"
    )
    grouped_account_inspection_item = (
        "        {\n"
        "          path: '/account-inspection',\n"
        "          labelKey: 'nav.account_inspection',\n"
        "          metaKey: 'nav_meta.account_inspection',\n"
        "          icon: sidebarIcons.accountInspection,\n"
        "        },\n"
    )
    text = read(path).replace(flat_routing_item, '').replace(grouped_routing_item, '')
    if flat_account_inspection_item in text:
        text = text.replace(
            flat_account_inspection_item,
            flat_account_inspection_item + flat_routing_item,
            1,
        )
    elif grouped_account_inspection_item in text:
        text = text.replace(
            grouped_account_inspection_item,
            grouped_account_inspection_item + grouped_routing_item,
            1,
        )
    else:
        raise RuntimeError(f'Pattern not found in {path}: account inspection navigation item')
    write(path, text)
    replace_once(
        path,
        "            <PageTransition\n",
        "            <QuotaPersistenceBootstrap />\n            <PageTransition\n",
    )

def patch_icons(target: Path) -> None:
    path = target / 'src/components/ui/icons.tsx'
    text = read(path)

    if "baseSvgProps" in text:
        svg_props = "baseSvgProps"
    elif "sidebarSvgProps" in text:
        svg_props = "sidebarSvgProps"
    else:
        raise RuntimeError(f'Pattern not found in {path}: svg props constant')

    monitor_icon = (
        "export function IconSidebarMonitor({ size = 20, ...props }: IconProps) {\n"
        "  return (\n"
        f"    <svg {{...{svg_props}}} width={{size}} height={{size}} {{...props}}>\n"
        "      <path d=\"M3 12h3l2.2-4.5 4.2 9 2.4-5h6.2\" />\n"
        "      <path d=\"M4 19h16\" />\n"
        "      <path d=\"M4 5h16\" fill=\"currentColor\" fillOpacity=\"0.08\" />\n"
        "    </svg>\n"
        "  );\n"
        "}\n\n"
    )
    account_inspection_icon = (
        "export function IconSidebarAccountInspection({ size = 20, ...props }: IconProps) {\n"
        "  return (\n"
        f"    <svg {{...{svg_props}}} width={{size}} height={{size}} {{...props}}>\n"
        "      <rect x=\"5\" y=\"3\" width=\"11\" height=\"16\" rx=\"2\" />\n"
        "      <path d=\"M9 7h3\" />\n"
        "      <path d=\"m8.5 11 1.4 1.4 2.6-2.8\" />\n"
        "      <circle cx=\"16.5\" cy=\"16.5\" r=\"3\" />\n"
        "      <path d=\"m19 19 2 2\" />\n"
        "      <path d=\"M8 3.5h5\" fill=\"currentColor\" fillOpacity=\"0.08\" />\n"
        "    </svg>\n"
        "  );\n"
        "}\n\n"
    )
    routing_icon = (
        "export function IconSidebarRouting({ size = 20, ...props }: IconProps) {\n"
        "  return (\n"
        f"    <svg {{...{svg_props}}} width={{size}} height={{size}} {{...props}}>\n"
        "      <circle cx=\"6\" cy=\"6\" r=\"2\" />\n"
        "      <circle cx=\"18\" cy=\"6\" r=\"2\" />\n"
        "      <circle cx=\"12\" cy=\"18\" r=\"2\" />\n"
        "      <path d=\"M8 6h8\" />\n"
        "      <path d=\"m7.5 7.5 3.2 7.2\" />\n"
        "      <path d=\"m16.5 7.5-3.2 7.2\" />\n"
        "    </svg>\n"
        "  );\n"
        "}\n\n"
    )
    icons_to_insert = ""
    if "export function IconSidebarMonitor" not in text:
        icons_to_insert += monitor_icon
    if "export function IconSidebarAccountInspection" not in text:
        icons_to_insert += account_inspection_icon
    if "export function IconSidebarRouting" not in text:
        icons_to_insert += routing_icon
    if not icons_to_insert:
        return
    for marker in (
        "export function IconSidebarLogs({ size = 20, ...props }: IconProps) {\n",
        "export const IconSidebarLogs = ",
        "export function IconSidebarSystem({ size = 20, ...props }: IconProps) {\n",
    ):
        if marker in text:
            write(path, text.replace(marker, icons_to_insert + marker, 1))
            return

    write(path, text.rstrip() + "\n\n" + icons_to_insert)


def patch_quota_types(target: Path) -> None:
    path = target / 'src/types/quota.ts'
    insert_once(
        path,
        "// API payload types\n",
        "// API payload types\nexport interface GeminiCliQuotaBucket {\n  modelId?: string;\n  model_id?: string;\n  tokenType?: string;\n  token_type?: string;\n  remainingFraction?: number | string;\n  remaining_fraction?: number | string;\n  remainingAmount?: number | string;\n  remaining_amount?: number | string;\n  resetTime?: string;\n  reset_time?: string;\n}\n\nexport interface GeminiCliQuotaPayload {\n  buckets?: GeminiCliQuotaBucket[];\n}\n\nexport interface GeminiCliCredits {\n  creditType?: string;\n  credit_type?: string;\n  creditAmount?: string | number;\n  credit_amount?: string | number;\n}\n\nexport interface GeminiCliUserTier {\n  id?: string;\n  name?: string;\n  description?: string;\n  availableCredits?: GeminiCliCredits[];\n  available_credits?: GeminiCliCredits[];\n}\n\nexport interface GeminiCliCodeAssistPayload {\n  currentTier?: GeminiCliUserTier | null;\n  current_tier?: GeminiCliUserTier | null;\n  paidTier?: GeminiCliUserTier | null;\n  paid_tier?: GeminiCliUserTier | null;\n}\n\nexport interface GeminiCliParsedBucket {\n  modelId: string;\n  tokenType: string | null;\n  remainingFraction: number | null;\n  remainingAmount: number | null;\n  resetTime: string | undefined;\n}\n\n",
        "export interface GeminiCliQuotaBucket",
    )
    insert_once(
        path,
        "export interface CodexQuotaWindow",
        "export interface GeminiCliQuotaBucketState {\n  id: string;\n  label: string;\n  remainingFraction: number | null;\n  remainingAmount: number | null;\n  resetTime: string | undefined;\n  tokenType: string | null;\n  modelIds?: string[];\n}\n\nexport interface GeminiCliQuotaState {\n  status: 'idle' | 'loading' | 'success' | 'error';\n  buckets: GeminiCliQuotaBucketState[];\n  projectId?: string;\n  project_id?: string;\n  tierLabel?: string | null;\n  tierId?: string | null;\n  creditBalance?: number | null;\n  error?: string;\n  errorStatus?: number;\n  cachedAt?: number;\n}\n\nexport interface CodexQuotaWindow",
        "export interface GeminiCliQuotaState",
    )
    for old, new in [
        (
            "  errorStatus?: number;\n}\n\n// Quota state types",
            "  errorStatus?: number;\n  cachedAt?: number;\n}\n\n// Quota state types",
        ),
        (
            "  errorStatus?: number;\n}\n\nexport interface CodexQuotaWindow",
            "  errorStatus?: number;\n  cachedAt?: number;\n}\n\nexport interface CodexQuotaWindow",
        ),
        (
            "  errorStatus?: number;\n}\n\n// Kimi API payload types",
            "  errorStatus?: number;\n  cachedAt?: number;\n}\n\n// Kimi API payload types",
        ),
        (
            "export interface KimiQuotaState {\n  status: 'idle' | 'loading' | 'success' | 'error';\n  rows: KimiQuotaRow[];\n  error?: string;\n  errorStatus?: number;\n}",
            "export interface KimiQuotaState {\n  status: 'idle' | 'loading' | 'success' | 'error';\n  rows: KimiQuotaRow[];\n  error?: string;\n  errorStatus?: number;\n  cachedAt?: number;\n}",
        ),
        (
            "export interface XaiQuotaState {\n  status: 'idle' | 'loading' | 'success' | 'error';\n  billing: XaiBillingSummary | null;\n  error?: string;\n  errorStatus?: number;\n}",
            "export interface XaiQuotaState {\n  status: 'idle' | 'loading' | 'success' | 'error';\n  billing: XaiBillingSummary | null;\n  error?: string;\n  errorStatus?: number;\n  cachedAt?: number;\n}",
        ),
    ]:
        replace_once(path, old, new)


def patch_quota_configs(target: Path) -> None:
    path = target / 'src/components/quota/quotaConfigs.ts'
    replace_once(
        path,
        "  CodexUsagePayload,\n  KimiQuotaRow,",
        "  CodexUsagePayload,\n  GeminiCliQuotaState,\n  KimiQuotaRow,",
    )
    replace_once(
        path,
        "type QuotaType = 'antigravity' | 'claude' | 'codex' | 'kimi' | 'xai';",
        "type QuotaType = 'antigravity' | 'claude' | 'codex' | 'gemini-cli' | 'kimi' | 'xai';",
    )
    replace_once(
        path,
        "  codexQuota: Record<string, CodexQuotaState>;\n  kimiQuota: Record<string, KimiQuotaState>;",
        "  codexQuota: Record<string, CodexQuotaState>;\n  geminiCliQuota: Record<string, GeminiCliQuotaState>;\n  kimiQuota: Record<string, KimiQuotaState>;",
    )
    replace_once(
        path,
        "  setCodexQuota: (updater: QuotaUpdater<Record<string, CodexQuotaState>>) => void;\n  setKimiQuota: (updater: QuotaUpdater<Record<string, KimiQuotaState>>) => void;",
        "  setCodexQuota: (updater: QuotaUpdater<Record<string, CodexQuotaState>>) => void;\n  setGeminiCliQuota: (updater: QuotaUpdater<Record<string, GeminiCliQuotaState>>) => void;\n  setKimiQuota: (updater: QuotaUpdater<Record<string, KimiQuotaState>>) => void;",
    )
    for store_setter in [
        'setClaudeQuota',
        'setAntigravityQuota',
        'setCodexQuota',
        'setKimiQuota',
        'setXaiQuota',
    ]:
        ensure_cached_at_in_quota_success_state(path, store_setter)
    for old, new in [
        (
            "  const groups = quota.groups ?? [];\n",
            "  const groups = Array.isArray(quota.groups) ? quota.groups : [];\n",
        ),
        (
            "        ...group.buckets.map((bucket) => {\n",
            "        ...(Array.isArray(group.buckets) ? group.buckets : []).map((bucket) => {\n",
        ),
        (
            "  const clampedUsed =\n",
            "  const productUsageItems = Array.isArray(billing.productUsage) ? billing.productUsage : [];\n\n  const clampedUsed =\n",
        ),
        (
            "    (weeklyUsed !== null || Boolean(billing.periodEnd) || billing.productUsage.length > 0);\n",
            "    (weeklyUsed !== null || Boolean(billing.periodEnd) || productUsageItems.length > 0);\n",
        ),
        (
            "    ...billing.productUsage.map((item) => {\n",
            "    ...productUsageItems.map((item) => {\n",
        ),
    ]:
        replace_once(path, old, new)


def patch_quota_page(target: Path) -> None:
    path = target / 'src/pages/QuotaPage.tsx'
    insert_once(
        path,
        "import { useAuthStore } from '@/stores';\n",
        "import { GEMINI_CLI_CONFIG } from '@/extensions/quota/geminiCliQuotaConfig';\nimport { useAuthStore } from '@/stores';\n",
        "GEMINI_CLI_CONFIG",
    )
    insert_once(
        path,
        "      <QuotaSection\n        config={KIMI_CONFIG}\n",
        "      <QuotaSection\n        config={GEMINI_CLI_CONFIG}\n        files={files}\n        loading={loading}\n        disabled={disableControls}\n      />\n      <QuotaSection\n        config={KIMI_CONFIG}\n",
        "config={GEMINI_CLI_CONFIG}",
    )
    replace_all(
        path,
        "import { FEATURES } from '@/config/features';\nimport { quotaPersistenceMiddleware } from '@/extensions/quota/persistenceMiddleware';\n",
        "",
    )
    replace_once(
        path,
        "import { useAuthStore } from '@/stores';\n",
        "import { quotaPersistenceMiddleware } from '@/extensions/quota/persistenceMiddleware';\nimport { useAuthStore } from '@/stores';\n",
    )
    replace_once(
        path,
        "  useEffect(() => {\n    loadFiles();\n  }, [loadFiles]);\n",
        "  useEffect(() => {\n    loadFiles();\n    void quotaPersistenceMiddleware.ensureFresh();\n  }, [loadFiles]);\n",
    )
    replace_all(
        path,
        "\n  useEffect(() => {\n    if (!FEATURES.QUOTA_PERSISTENCE) return;\n    quotaPersistenceMiddleware.start();\n    return () => quotaPersistenceMiddleware.stop();\n  }, []);\n",
        "",
    )
    replace_all(
        path,
        "\n  // Initialize persistence middleware\n  useEffect(() => {\n    if (FEATURES.QUOTA_PERSISTENCE) {\n      quotaPersistenceMiddleware.start();\n      return () => quotaPersistenceMiddleware.stop();\n    }\n  }, []);\n",
        "",
    )


def patch_quota_page_search(target: Path) -> None:
    page_path = target / 'src/pages/QuotaPage.tsx'
    replace_once(
        page_path,
        "import { useCallback, useEffect, useState } from 'react';\n",
        "import { useCallback, useEffect, useMemo, useState } from 'react';\n",
    )
    insert_once(
        page_path,
        "import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';\n",
        "import { EmptyState } from '@/components/ui/EmptyState';\n"
        "import { Input } from '@/components/ui/Input';\n"
        "import { IconSearch } from '@/components/ui/icons';\n"
        "import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';\n",
        "quota_management.search_label",
    )
    insert_once(
        page_path,
        "export function QuotaPage() {\n",
        "const QUOTA_SEARCH_FIELD_KEYS = [\n"
        "  'name',\n"
        "  'auth_index',\n"
        "  'authIndex',\n"
        "  'auth-index',\n"
        "  'type',\n"
        "  'provider',\n"
        "  'note',\n"
        "  'remark',\n"
        "  'remarks',\n"
        "  'description',\n"
        "  'plan',\n"
        "  'plan_type',\n"
        "  'planType',\n"
        "  'package',\n"
        "  'package_name',\n"
        "  'packageName',\n"
        "  'subscription',\n"
        "  'subscription_plan',\n"
        "  'subscriptionPlan',\n"
        "  'tier',\n"
        "  'tier_id',\n"
        "  'tierId',\n"
        "  'tier_label',\n"
        "  'tierLabel',\n"
        "  'product',\n"
        "  'product_name',\n"
        "  'productName',\n"
        "  'quota_plan',\n"
        "  'quotaPlan',\n"
        "] as const;\n"
        "\n"
        "const QUOTA_NESTED_SEARCH_KEY_PATTERN =\n"
        "  /(note|remark|description|desc|plan|package|subscription|tier|product|quota)/i;\n"
        "\n"
        "const escapeQuotaSearchSegment = (value: string): string =>\n"
        "  value.replace(/[.*+?^${}()|[\\]\\\\]/g, '\\\\$&');\n"
        "\n"
        "const buildQuotaWildcardSearch = (value: string): RegExp | null => {\n"
        "  if (!value.includes('*')) return null;\n"
        "  const pattern = value.split('*').map(escapeQuotaSearchSegment).join('.*');\n"
        "  return new RegExp(pattern, 'i');\n"
        "};\n"
        "\n"
        "const collectQuotaSearchValues = (value: unknown, depth = 0): string[] => {\n"
        "  if (value == null) return [];\n"
        "  if (typeof value === 'string') return value.trim() ? [value] : [];\n"
        "  if (typeof value === 'number' || typeof value === 'boolean') return [String(value)];\n"
        "  if (depth >= 2) return [];\n"
        "  if (Array.isArray(value)) {\n"
        "    return value.flatMap((item) => collectQuotaSearchValues(item, depth + 1));\n"
        "  }\n"
        "  if (typeof value !== 'object') return [];\n"
        "\n"
        "  return Object.entries(value as Record<string, unknown>).flatMap(([key, nestedValue]) =>\n"
        "    QUOTA_NESTED_SEARCH_KEY_PATTERN.test(key)\n"
        "      ? collectQuotaSearchValues(nestedValue, depth + 1)\n"
        "      : []\n"
        "  );\n"
        "};\n"
        "\n"
        "const buildQuotaSearchValues = (item: AuthFileItem): string[] =>\n"
        "  QUOTA_SEARCH_FIELD_KEYS.flatMap((key) => collectQuotaSearchValues(item[key]));\n"
        "\n"
        "export function QuotaPage() {\n",
        "QUOTA_SEARCH_FIELD_KEYS",
    )
    replace_once(
        page_path,
        "  const [error, setError] = useState('');\n\n  const disableControls",
        "  const [error, setError] = useState('');\n"
        "  const [search, setSearch] = useState('');\n"
        "\n"
        "  const normalizedSearch = search.trim();\n"
        "  const wildcardSearch = useMemo(\n"
        "    () => buildQuotaWildcardSearch(normalizedSearch),\n"
        "    [normalizedSearch]\n"
        "  );\n"
        "  const searchFileNames = useMemo(() => {\n"
        "    if (!normalizedSearch) return null;\n"
        "    const normalizedTerm = normalizedSearch.toLowerCase();\n"
        "    return new Set(\n"
        "      files\n"
        "        .filter((item) =>\n"
        "          buildQuotaSearchValues(item).some((value) =>\n"
        "            wildcardSearch\n"
        "              ? wildcardSearch.test(value)\n"
        "              : value.toLowerCase().includes(normalizedTerm)\n"
        "          )\n"
        "        )\n"
        "        .map((item) => item.name)\n"
        "    );\n"
        "  }, [files, normalizedSearch, wildcardSearch]);\n"
        "  const hasQuotaSearchResults = useMemo(() => {\n"
        "    if (!searchFileNames) return true;\n"
        "    const filters = [\n"
        "      CLAUDE_CONFIG.filterFn,\n"
        "      ANTIGRAVITY_CONFIG.filterFn,\n"
        "      CODEX_CONFIG.filterFn,\n"
        "      GEMINI_CLI_CONFIG.filterFn,\n"
        "      XAI_CONFIG.filterFn,\n"
        "      KIMI_CONFIG.filterFn,\n"
        "    ];\n"
        "    return files.some(\n"
        "      (file) => searchFileNames.has(file.name) && filters.some((filterFn) => filterFn(file))\n"
        "    );\n"
        "  }, [files, searchFileNames]);\n"
        "\n"
        "  const disableControls",
    )
    insert_once(
        page_path,
        "      {error && <div className={styles.errorBox}>{error}</div>}\n",
        "      <div className={styles.searchBar}>\n"
        "        <Input\n"
        "          className={styles.searchInput}\n"
        "          type=\"search\"\n"
        "          value={search}\n"
        "          onChange={(event) => setSearch(event.target.value)}\n"
        "          placeholder={t('quota_management.search_placeholder')}\n"
        "          aria-label={t('quota_management.search_label')}\n"
        "          rightElement={<IconSearch className={styles.searchIcon} size={18} />}\n"
        "        />\n"
        "      </div>\n"
        "\n"
        "      {error && <div className={styles.errorBox}>{error}</div>}\n"
        "\n"
        "      {normalizedSearch && !hasQuotaSearchResults && (\n"
        "        <EmptyState\n"
        "          title={t('quota_management.no_search_results')}\n"
        "          description={t('quota_management.no_search_results_desc')}\n"
        "        />\n"
        "      )}\n",
        "quota_management.no_search_results",
    )
    replace_all(
        page_path,
        "        disabled={disableControls}\n      />",
        "        disabled={disableControls}\n"
        "        searchFileNames={searchFileNames}\n"
        "        hideWhenEmpty={Boolean(normalizedSearch)}\n"
        "      />",
    )

    section_path = target / 'src/components/quota/QuotaSection.tsx'
    replace_once(
        section_path,
        "  disabled: boolean;\n}",
        "  disabled: boolean;\n"
        "  searchFileNames?: ReadonlySet<string> | null;\n"
        "  hideWhenEmpty?: boolean;\n"
        "}",
    )
    replace_once(
        section_path,
        "  loading,\n  disabled,\n}: QuotaSectionProps<TState, TData>)",
        "  loading,\n"
        "  disabled,\n"
        "  searchFileNames = null,\n"
        "  hideWhenEmpty = false,\n"
        "}: QuotaSectionProps<TState, TData>)",
    )
    replace_once(
        section_path,
        "  const filteredFiles = useMemo(\n"
        "    () => files.filter((file) => config.filterFn(file)),\n"
        "    [files, config]\n"
        "  );\n",
        "  const providerFiles = useMemo(\n"
        "    () => files.filter((file) => config.filterFn(file)),\n"
        "    [files, config]\n"
        "  );\n"
        "  const filteredFiles = useMemo(\n"
        "    () =>\n"
        "      searchFileNames\n"
        "        ? providerFiles.filter((file) => searchFileNames.has(file.name))\n"
        "        : providerFiles,\n"
        "    [providerFiles, searchFileNames]\n"
        "  );\n",
    )
    replace_once(
        section_path,
        "    if (filteredFiles.length === 0) {\n"
        "      setQuota({});\n"
        "      return;\n"
        "    }\n"
        "    setQuota((prev) => {\n"
        "      const nextState: Record<string, TState> = {};\n"
        "      filteredFiles.forEach((file) => {\n",
        "    if (providerFiles.length === 0) {\n"
        "      setQuota({});\n"
        "      return;\n"
        "    }\n"
        "    setQuota((prev) => {\n"
        "      const nextState: Record<string, TState> = {};\n"
        "      providerFiles.forEach((file) => {\n",
    )
    replace_once(
        section_path,
        "  }, [filteredFiles, loading, setQuota]);\n",
        "  }, [loading, providerFiles, setQuota]);\n",
    )
    insert_once(
        section_path,
        "  return (\n    <Card\n",
        "  if (hideWhenEmpty && filteredFiles.length === 0) return null;\n\n"
        "  return (\n    <Card\n",
        "hideWhenEmpty && filteredFiles.length",
    )

    styles_path = target / 'src/pages/QuotaPage.module.scss'
    insert_once(
        styles_path,
        ".errorBox {\n",
        ".searchBar {\n"
        "  width: min(100%, 560px);\n"
        "\n"
        "  :global(.form-group) {\n"
        "    margin: 0;\n"
        "  }\n"
        "}\n"
        "\n"
        ".searchInput {\n"
        "  width: 100%;\n"
        "  min-height: 42px;\n"
        "  padding-right: 40px;\n"
        "}\n"
        "\n"
        ".searchIcon {\n"
        "  display: block;\n"
        "  color: var(--text-tertiary);\n"
        "  pointer-events: none;\n"
        "}\n"
        "\n"
        ".errorBox {\n",
        ".searchBar",
    )


def patch_quota_card(target: Path) -> None:
    path = target / 'src/components/quota/QuotaCard.tsx'
    replace_once(
        path,
        "import { TYPE_COLORS } from '@/utils/quota';\n",
        "import { QuotaCachedTime } from '@/extensions/quota/QuotaCardExtras';\nimport { TYPE_COLORS } from '@/utils/quota';\n",
    )
    replace_once(path, "  errorStatus?: number;\n}", "  errorStatus?: number;\n  cachedAt?: number;\n}")
    replace_once(
        path,
        "        ) : quota ? (\n          renderQuotaItems(quota, t, { styles, QuotaProgressBar })\n        ) : (",
        "        ) : quota ? (\n          <>\n            {renderQuotaItems(quota, t, { styles, QuotaProgressBar })}\n            <QuotaCachedTime quotaStatus={quotaStatus} cachedAt={quota.cachedAt} />\n          </>\n        ) : (",
    )


def patch_quota_store(target: Path) -> None:
    path = target / 'src/stores/useQuotaStore.ts'
    replace_once(
        path,
        "  CodexQuotaState,\n  KimiQuotaState,",
        "  CodexQuotaState,\n  GeminiCliQuotaState,\n  KimiQuotaState,",
    )
    replace_once(
        path,
        "  codexQuota: Record<string, CodexQuotaState>;\n  kimiQuota: Record<string, KimiQuotaState>;",
        "  codexQuota: Record<string, CodexQuotaState>;\n  geminiCliQuota: Record<string, GeminiCliQuotaState>;\n  kimiQuota: Record<string, KimiQuotaState>;",
    )
    replace_once(
        path,
        "  setCodexQuota: (updater: QuotaUpdater<Record<string, CodexQuotaState>>) => void;\n  setKimiQuota: (updater: QuotaUpdater<Record<string, KimiQuotaState>>) => void;",
        "  setCodexQuota: (updater: QuotaUpdater<Record<string, CodexQuotaState>>) => void;\n  setGeminiCliQuota: (updater: QuotaUpdater<Record<string, GeminiCliQuotaState>>) => void;\n  setKimiQuota: (updater: QuotaUpdater<Record<string, KimiQuotaState>>) => void;",
    )
    replace_once(
        path,
        "  codexQuota: {},\n  kimiQuota: {},",
        "  codexQuota: {},\n  geminiCliQuota: {},\n  kimiQuota: {},",
    )
    replace_once(
        path,
        "  setCodexQuota: (updater) =>\n    set((state) => ({\n      codexQuota: resolveUpdater(updater, state.codexQuota),\n    })),\n  setKimiQuota: (updater) =>",
        "  setCodexQuota: (updater) =>\n    set((state) => ({\n      codexQuota: resolveUpdater(updater, state.codexQuota),\n    })),\n  setGeminiCliQuota: (updater) =>\n    set((state) => ({\n      geminiCliQuota: resolveUpdater(updater, state.geminiCliQuota),\n    })),\n  setKimiQuota: (updater) =>",
    )
    replace_once(
        path,
        "      codexQuota: {},\n      kimiQuota: {},",
        "      codexQuota: {},\n      geminiCliQuota: {},\n      kimiQuota: {},",
    )


def patch_quota_constants(target: Path) -> None:
    path = target / 'src/utils/quota/constants.ts'
    insert_once(
        path,
        "  aistudio: {\n",
        "  'gemini-cli': {\n    light: { bg: '#e0e8ff', text: '#1e4fa3' },\n    dark: { bg: '#1c3f73', text: '#a8c7ff' },\n  },\n  aistudio: {\n",
        "'gemini-cli':",
    )


def patch_antigravity_quota_builders(target: Path) -> None:
    path = target / 'src/utils/quota/builders.ts'
    insert_once(
        path,
        "\nfunction getAntigravityWindowOrder(bucket: AntigravityQuotaBucket): number {\n",
        "\nfunction getCanonicalAntigravityGroupId(label: string, description?: string): string {\n  const normalizedLabel = toStableId(label, '');\n  const normalizedDescription = description ? toStableId(description, '') : '';\n  const combined = `${normalizedLabel}-${normalizedDescription}`;\n  if (combined.includes('claude') && (combined.includes('gpt') || combined.includes('gpt-oss') || combined.includes('openai'))) {\n    return 'claude-gpt';\n  }\n  if (combined.includes('gemini')) {\n    return 'gemini';\n  }\n  return normalizedLabel;\n}\n\nfunction getAntigravityWindowOrder(bucket: AntigravityQuotaBucket): number {\n",
        "getCanonicalAntigravityGroupId",
    )
    replace_once(
        path,
        "      const groupId = toStableId(label, `quota-group-${groupIndex + 1}`);\n      const buckets = Array.isArray(group.buckets) ? group.buckets : [];\n",
        "      const description = normalizeStringValue(group.description) ?? undefined;\n      const groupId = getCanonicalAntigravityGroupId(label, description) || `quota-group-${groupIndex + 1}`;\n      const buckets = Array.isArray(group.buckets) ? group.buckets : [];\n",
    )
    replace_once(
        path,
        "        description: normalizeStringValue(group.description) ?? undefined,\n",
        "        description,\n",
    )
    replace_once(
        path,
        "    productUsage: primary.productUsage.length > 0 ? primary.productUsage : fallback.productUsage,\n",
        "    productUsage: Array.isArray(primary.productUsage) && primary.productUsage.length > 0\n      ? primary.productUsage\n      : Array.isArray(fallback.productUsage)\n        ? fallback.productUsage\n        : [],\n",
    )


def patch_quota_styles(target: Path) -> None:
    path = target / 'src/pages/QuotaPage.module.scss'
    replace_once(
        path,
        ".codexGrid,\n.kimiGrid,",
        ".codexGrid,\n.geminiCliGrid,\n.kimiGrid,",
    )
    replace_once_if_present(
        path,
        ".codexControls,\n.kimiControls,",
        ".codexControls,\n.geminiCliControls,\n.kimiControls,",
    )
    replace_once_if_present(
        path,
        ".codexControl,\n.kimiControl,",
        ".codexControl,\n.geminiCliControl,\n.kimiControl,",
    )
    insert_once(
        path,
        ".kimiCard {\n",
        ".geminiCliCard {\n  background-image: linear-gradient(180deg, rgba(224, 232, 255, 0.2), rgba(224, 232, 255, 0));\n}\n\n.kimiCard {\n",
        ".geminiCliCard",
    )


def patch_account_inspection_page(target: Path) -> None:
    path = target / 'src/pages/AccountInspectionPage.tsx'
    replace_once_if_present(
        path,
        "  const used = normalizeNumberValue(quota.billing.usedPercent ?? quota.billing.used_percent);\n"
        "  return used !== null && used >= usedPercentThreshold;\n",
        "  const used =\n"
        "    normalizeNumberValue(quota.billing.usagePercent ?? quota.billing.usage_percent)\n"
        "    ?? normalizeNumberValue(quota.billing.usedPercent ?? quota.billing.used_percent)\n"
        "    ?? maxAntigravityGroupUsedPercent(Array.isArray(quota.billing.productUsage) ? quota.billing.productUsage : []);\n"
        "  return used !== null && used >= usedPercentThreshold;\n",
    )


def patch_auth_files_page_search(target: Path) -> None:
    path = target / 'src/pages/AuthFilesPage.tsx'
    replace_once(
        path,
        "import { useAuthStore, useNotificationStore, useThemeStore } from '@/stores';\n",
        "import { useAuthStore, useNotificationStore, useThemeStore, useQuotaStore } from '@/stores';\n",
    )
    insert_once(
        path,
        "const buildWildcardSearch = (value: string): RegExp | null => {\n"
        "  if (!value.includes('*')) return null;\n"
        "  const pattern = value.split('*').map(escapeWildcardSearchSegment).join('.*');\n"
        "  return new RegExp(pattern, 'i');\n"
        "};\n",
        "const buildWildcardSearch = (value: string): RegExp | null => {\n"
        "  if (!value.includes('*')) return null;\n"
        "  const pattern = value.split('*').map(escapeWildcardSearchSegment).join('.*');\n"
        "  return new RegExp(pattern, 'i');\n"
        "};\n"
        "\n"
        "const AUTH_FILE_SEARCH_FIELD_KEYS = [\n"
        "  'name',\n"
        "  'auth_index',\n"
        "  'authIndex',\n"
        "  'auth-index',\n"
        "  'type',\n"
        "  'provider',\n"
        "  'note',\n"
        "  'remark',\n"
        "  'remarks',\n"
        "  'description',\n"
        "  'plan',\n"
        "  'plan_type',\n"
        "  'planType',\n"
        "  'package',\n"
        "  'package_name',\n"
        "  'packageName',\n"
        "  'subscription',\n"
        "  'subscription_plan',\n"
        "  'subscriptionPlan',\n"
        "  'tier',\n"
        "  'tier_id',\n"
        "  'tierId',\n"
        "  'tier_label',\n"
        "  'tierLabel',\n"
        "  'product',\n"
        "  'product_name',\n"
        "  'productName',\n"
        "  'quota_plan',\n"
        "  'quotaPlan',\n"
        "] as const;\n"
        "\n"
        "const PREMIUM_CODEX_SEARCH_PLAN_TYPES = new Set(['pro', 'prolite', 'pro-lite', 'pro_lite']);\n"
        "const XAI_SUPERGROK_LIMIT_CENTS = 15_000;\n"
        "const XAI_SUPERGROK_HEAVY_LIMIT_CENTS = 150_000;\n"
        "\n"
        "type AuthFileSearchTranslate = (key: string) => string;\n"
        "type AuthFileSearchQuotaStore = Pick<\n"
        "  ReturnType<typeof useQuotaStore.getState>,\n"
        "  'antigravityQuota' | 'claudeQuota' | 'codexQuota' | 'geminiCliQuota' | 'kimiQuota' | 'xaiQuota'\n"
        ">;\n"
        "\n"
        "const AUTH_FILE_NESTED_SEARCH_KEY_PATTERN =\n"
        "  /(note|remark|description|desc|plan|package|subscription|tier|product|quota)/i;\n"
        "\n"
        "const addAuthFileSearchValue = (values: string[], value: unknown) => {\n"
        "  if (value == null) return;\n"
        "  if (typeof value === 'string') {\n"
        "    const trimmed = value.trim();\n"
        "    if (trimmed) values.push(trimmed);\n"
        "    return;\n"
        "  }\n"
        "  if (typeof value === 'number' || typeof value === 'boolean') {\n"
        "    values.push(String(value));\n"
        "  }\n"
        "};\n"
        "\n"
        "const toAuthFileSearchRecord = (value: unknown): Record<string, unknown> | null =>\n"
        "  value && typeof value === 'object' && !Array.isArray(value)\n"
        "    ? (value as Record<string, unknown>)\n"
        "    : null;\n"
        "\n"
        "const normalizeAuthFileSearchPlan = (value: unknown): string =>\n"
        "  typeof value === 'string' ? value.trim().toLowerCase().replace(/_/g, '-') : '';\n"
        "\n"
        "const addCodexPlanSearchValues = (\n"
        "  values: string[],\n"
        "  planType: unknown,\n"
        "  t: AuthFileSearchTranslate\n"
        ") => {\n"
        "  const normalized = normalizeAuthFileSearchPlan(planType);\n"
        "  if (!normalized) return;\n"
        "  values.push(normalized, normalized.replace(/-/g, ' '));\n"
        "  if (normalized === 'pro') values.push(t('codex_quota.plan_pro'));\n"
        "  else if (PREMIUM_CODEX_SEARCH_PLAN_TYPES.has(normalized)) values.push(t('codex_quota.plan_prolite'));\n"
        "  else if (normalized === 'plus') values.push(t('codex_quota.plan_plus'));\n"
        "  else if (normalized === 'team') values.push(t('codex_quota.plan_team'));\n"
        "  else if (normalized === 'free') values.push(t('codex_quota.plan_free'));\n"
        "};\n"
        "\n"
        "const addClaudePlanSearchValues = (\n"
        "  values: string[],\n"
        "  planType: unknown,\n"
        "  t: AuthFileSearchTranslate\n"
        ") => {\n"
        "  const raw = typeof planType === 'string' ? planType.trim() : '';\n"
        "  if (!raw) return;\n"
        "  values.push(raw, raw.replace(/^plan[_-]/i, '').replace(/[_-]/g, ' '));\n"
        "  values.push(t(`claude_quota.${raw}`));\n"
        "};\n"
        "\n"
        "const addAntigravityPlanSearchValues = (\n"
        "  values: string[],\n"
        "  subscription: unknown,\n"
        "  t: AuthFileSearchTranslate\n"
        ") => {\n"
        "  const record = toAuthFileSearchRecord(subscription);\n"
        "  if (!record) return;\n"
        "  const plan = normalizeAuthFileSearchPlan(record.plan);\n"
        "  addAuthFileSearchValue(values, record.plan);\n"
        "  addAuthFileSearchValue(values, record.tierName);\n"
        "  addAuthFileSearchValue(values, record.tierId);\n"
        "  if (plan === 'free') values.push(t('antigravity_subscription.plan_free'));\n"
        "  else if (plan === 'pro') values.push(t('antigravity_subscription.plan_pro'));\n"
        "  else if (plan === 'ultra') values.push(t('antigravity_subscription.plan_ultra'));\n"
        "  else if (plan === 'ultra-lite') values.push(t('antigravity_subscription.plan_ultra_lite'));\n"
        "};\n"
        "\n"
        "const normalizeAuthFileSearchCents = (value: unknown): number | null => {\n"
        "  const source = toAuthFileSearchRecord(value)?.val ?? value;\n"
        "  if (typeof source === 'number' && Number.isFinite(source)) return source;\n"
        "  if (typeof source !== 'string') return null;\n"
        "  const parsed = Number(source.trim());\n"
        "  return Number.isFinite(parsed) ? parsed : null;\n"
        "};\n"
        "\n"
        "const addXaiPlanSearchValues = (\n"
        "  values: string[],\n"
        "  billing: unknown,\n"
        "  t: AuthFileSearchTranslate\n"
        ") => {\n"
        "  const record = toAuthFileSearchRecord(billing);\n"
        "  if (!record) return;\n"
        "  const monthlyLimitCents = normalizeAuthFileSearchCents(record.monthlyLimitCents);\n"
        "  if (monthlyLimitCents === XAI_SUPERGROK_LIMIT_CENTS) values.push(t('xai_quota.plan_supergrok'), 'supergrok');\n"
        "  if (monthlyLimitCents === XAI_SUPERGROK_HEAVY_LIMIT_CENTS) values.push(t('xai_quota.plan_supergrok_heavy'), 'supergrok heavy');\n"
        "};\n"
        "\n"
        "const buildAuthFileQuotaSearchValues = (\n"
        "  item: Record<string, unknown>,\n"
        "  quotaStore: AuthFileSearchQuotaStore,\n"
        "  t: AuthFileSearchTranslate\n"
        "): string[] => {\n"
        "  const name = typeof item.name === 'string' ? item.name : '';\n"
        "  if (!name) return [];\n"
        "  const values: string[] = [];\n"
        "  addAntigravityPlanSearchValues(values, quotaStore.antigravityQuota[name]?.subscription, t);\n"
        "  addClaudePlanSearchValues(values, quotaStore.claudeQuota[name]?.planType, t);\n"
        "  addCodexPlanSearchValues(values, quotaStore.codexQuota[name]?.planType, t);\n"
        "  addAuthFileSearchValue(values, quotaStore.geminiCliQuota[name]?.tierLabel);\n"
        "  addAuthFileSearchValue(values, quotaStore.geminiCliQuota[name]?.tierId);\n"
        "  addAuthFileSearchValue(values, quotaStore.geminiCliQuota[name]?.creditBalance);\n"
        "  addXaiPlanSearchValues(values, quotaStore.xaiQuota[name]?.billing, t);\n"
        "  return values;\n"
        "};\n"
        "\n"
        "const collectAuthFileSearchValues = (value: unknown, depth = 0): string[] => {\n"
        "  if (value == null) return [];\n"
        "  if (typeof value === 'string') return value.trim() ? [value] : [];\n"
        "  if (typeof value === 'number' || typeof value === 'boolean') return [String(value)];\n"
        "  if (depth >= 2) return [];\n"
        "  if (Array.isArray(value)) {\n"
        "    return value.flatMap((item) => collectAuthFileSearchValues(item, depth + 1));\n"
        "  }\n"
        "  if (typeof value !== 'object') return [];\n"
        "\n"
        "  return Object.entries(value as Record<string, unknown>).flatMap(([key, nestedValue]) =>\n"
        "    AUTH_FILE_NESTED_SEARCH_KEY_PATTERN.test(key)\n"
        "      ? collectAuthFileSearchValues(nestedValue, depth + 1)\n"
        "      : []\n"
        "  );\n"
        "};\n"
        "\n"
        "const buildAuthFileSearchValues = (\n"
        "  item: Record<string, unknown>,\n"
        "  quotaStore: AuthFileSearchQuotaStore,\n"
        "  t: AuthFileSearchTranslate\n"
        "): string[] => [\n"
        "  ...AUTH_FILE_SEARCH_FIELD_KEYS.flatMap((key) => collectAuthFileSearchValues(item[key])),\n"
        "  ...buildAuthFileQuotaSearchValues(item, quotaStore, t),\n"
        "];\n",
        "AUTH_FILE_SEARCH_FIELD_KEYS",
    )
    insert_once(
        path,
        "  const statusBarCache = useAuthFilesStatusBarCache(files);\n",
        "  const statusBarCache = useAuthFilesStatusBarCache(files);\n"
        "\n"
        "  const antigravityQuota = useQuotaStore((state) => state.antigravityQuota);\n"
        "  const claudeQuota = useQuotaStore((state) => state.claudeQuota);\n"
        "  const codexQuota = useQuotaStore((state) => state.codexQuota);\n"
        "  const geminiCliQuota = useQuotaStore((state) => state.geminiCliQuota);\n"
        "  const kimiQuota = useQuotaStore((state) => state.kimiQuota);\n"
        "  const xaiQuota = useQuotaStore((state) => state.xaiQuota);\n"
        "  const quotaSearchStore = useMemo(\n"
        "    () => ({ antigravityQuota, claudeQuota, codexQuota, geminiCliQuota, kimiQuota, xaiQuota }),\n"
        "    [antigravityQuota, claudeQuota, codexQuota, geminiCliQuota, kimiQuota, xaiQuota]\n"
        "  );\n",
        "quotaSearchStore",
    )
    replace_once(
        path,
        "        [item.name, item.type, item.provider].some((value) => {\n"
        "          const content = (value || '').toString();\n"
        "          return wildcardSearch\n"
        "            ? wildcardSearch.test(content)\n"
        "            : content.toLowerCase().includes(normalizedTerm);\n"
        "        });\n",
        "        buildAuthFileSearchValues(item, quotaSearchStore, t).some((value) => {\n"
        "          const content = value.toString();\n"
        "          return wildcardSearch\n"
        "            ? wildcardSearch.test(content)\n"
        "            : content.toLowerCase().includes(normalizedTerm);\n"
        "        });\n",
    )
    replace_once(
        path,
        "  }, [filesMatchingStatusFilters, normalizedFilter, normalizedSearch, wildcardSearch]);\n",
        "  }, [filesMatchingStatusFilters, normalizedFilter, normalizedSearch, quotaSearchStore, t, wildcardSearch]);\n",
    )


def patch_auth_files_page_sorting(target: Path) -> None:
    page_path = target / 'src/pages/AuthFilesPage.tsx'
    ui_state_path = target / 'src/features/authFiles/uiState.ts'

    replace_once(
        ui_state_path,
        "export const AUTH_FILES_SORT_MODES = ['default', 'az', 'priority'] as const;\n",
        "export const AUTH_FILES_SORT_MODES = ['default', 'az', 'priority', 'plan', 'quota'] as const;\n",
    )

    insert_once(
        page_path,
        "import { useAuthStore, useNotificationStore, useThemeStore, useQuotaStore } from '@/stores';\n",
        "import {\n"
        "  compareAuthFilesByPlanDescending,\n"
        "  isAuthFilePlanSortProvider,\n"
        "} from '@/features/authFiles/planSort';\n"
        "import {\n"
        "  compareAuthFilesByAvailableQuotaDescending,\n"
        "  isAuthFileQuotaSortProvider,\n"
        "} from '@/features/authFiles/quotaSort';\n"
        "import { useAuthStore, useNotificationStore, useThemeStore, useQuotaStore } from '@/stores';\n",
        "from '@/features/authFiles/quotaSort'",
    )

    insert_once(
        page_path,
        "  const enabledOnly = statusFilterMode === 'enabled';\n",
        "  const enabledOnly = statusFilterMode === 'enabled';\n"
        "  const planSortAvailable = isAuthFilePlanSortProvider(normalizedFilter);\n"
        "  const quotaSortAvailable = isAuthFileQuotaSortProvider(normalizedFilter);\n"
        "  const selectedSortModeAvailable =\n"
        "    (sortMode !== 'plan' || planSortAvailable)\n"
        "    && (sortMode !== 'quota' || quotaSortAvailable);\n"
        "  const effectiveSortMode: AuthFilesSortMode =\n"
        "    selectedSortModeAvailable ? sortMode : 'default';\n",
        'effectiveSortMode',
    )

    insert_once(
        page_path,
        "  const handleStatusFilterModeChange = useCallback((nextMode: AuthFilesStatusFilterMode) => {\n",
        "  useEffect(() => {\n"
        "    if (selectedSortModeAvailable) return;\n"
        "    setSortMode('default');\n"
        "    setPage(1);\n"
        "  }, [selectedSortModeAvailable]);\n"
        "\n"
        "  const handleStatusFilterModeChange = useCallback((nextMode: AuthFilesStatusFilterMode) => {\n",
        "if (selectedSortModeAvailable) return;",
    )

    replace_once(
        page_path,
        "  const sortOptions = useMemo(\n"
        "    () => [\n"
        "      { value: 'default', label: t('auth_files.sort_default') },\n"
        "      { value: 'az', label: t('auth_files.sort_az') },\n"
        "      { value: 'priority', label: t('auth_files.sort_priority') },\n"
        "    ],\n"
        "    [t]\n"
        "  );\n",
        "  const sortOptions = useMemo(() => {\n"
        "    const options: Array<{ value: AuthFilesSortMode; label: string }> = [\n"
        "      { value: 'default', label: t('auth_files.sort_default') },\n"
        "      { value: 'az', label: t('auth_files.sort_az') },\n"
        "      { value: 'priority', label: t('auth_files.sort_priority') },\n"
        "    ];\n"
        "    if (planSortAvailable) {\n"
        "      options.push({ value: 'plan', label: t('auth_files.sort_plan_desc') });\n"
        "    }\n"
        "    if (quotaSortAvailable) {\n"
        "      options.push({ value: 'quota', label: t('auth_files.sort_quota_desc') });\n"
        "    }\n"
        "    return options;\n"
        "  }, [planSortAvailable, quotaSortAvailable, t]);\n",
    )
    replace_once(
        page_path,
        "  const sorted = useMemo(() => {\n"
        "    const copy = [...filtered];\n"
        "    if (sortMode === 'default') {\n"
        "      copy.sort((a, b) => {\n"
        "        const providerA = normalizeProviderKey(String(a.provider ?? a.type ?? 'unknown'));\n"
        "        const providerB = normalizeProviderKey(String(b.provider ?? b.type ?? 'unknown'));\n"
        "        const providerCompare = providerA.localeCompare(providerB);\n"
        "        if (providerCompare !== 0) return providerCompare;\n"
        "        return a.name.localeCompare(b.name);\n"
        "      });\n"
        "    } else if (sortMode === 'az') {\n"
        "      copy.sort((a, b) => a.name.localeCompare(b.name));\n"
        "    } else if (sortMode === 'priority') {\n"
        "      copy.sort((a, b) => {\n"
        "        const pa = parsePriorityValue(a.priority) ?? 0;\n"
        "        const pb = parsePriorityValue(b.priority) ?? 0;\n"
        "        return pb - pa; // 高优先级排前面\n"
        "      });\n"
        "    }\n"
        "    return copy;\n"
        "  }, [filtered, sortMode]);\n",
        "  const sorted = useMemo(() => {\n"
        "    const copy = [...filtered];\n"
        "    if (effectiveSortMode === 'default') {\n"
        "      copy.sort((a, b) => {\n"
        "        const providerA = normalizeProviderKey(String(a.provider ?? a.type ?? 'unknown'));\n"
        "        const providerB = normalizeProviderKey(String(b.provider ?? b.type ?? 'unknown'));\n"
        "        const providerCompare = providerA.localeCompare(providerB);\n"
        "        if (providerCompare !== 0) return providerCompare;\n"
        "        return a.name.localeCompare(b.name);\n"
        "      });\n"
        "    } else if (effectiveSortMode === 'az') {\n"
        "      copy.sort((a, b) => a.name.localeCompare(b.name));\n"
        "    } else if (effectiveSortMode === 'priority') {\n"
        "      copy.sort((a, b) => {\n"
        "        const pa = parsePriorityValue(a.priority) ?? 0;\n"
        "        const pb = parsePriorityValue(b.priority) ?? 0;\n"
        "        return pb - pa; // 高优先级排前面\n"
        "      });\n"
        "    } else if (effectiveSortMode === 'plan') {\n"
        "      copy.sort((a, b) => compareAuthFilesByPlanDescending(a, b, quotaSearchStore));\n"
        "    } else if (effectiveSortMode === 'quota') {\n"
        "      copy.sort((a, b) => compareAuthFilesByAvailableQuotaDescending(a, b, quotaSearchStore));\n"
        "    }\n"
        "    return copy;\n"
        "  }, [effectiveSortMode, filtered, quotaSearchStore]);\n",
    )

    replace_once(
        page_path,
        "                      value={sortMode}\n"
        "                      options={sortOptions}\n"
        "                      onChange={handleSortModeChange}\n",
        "                      value={effectiveSortMode}\n"
        "                      options={sortOptions}\n"
        "                      onChange={handleSortModeChange}\n",
    )


def patch_auth_files_gemini_quota(target: Path) -> None:
    constants_path = target / 'src/features/authFiles/constants.ts'
    quota_section_path = target / 'src/features/authFiles/components/AuthFileQuotaSection.tsx'
    card_path = target / 'src/features/authFiles/components/AuthFileCard.tsx'
    styles_path = target / 'src/pages/AuthFilesPage.module.scss'

    replace_once(
        constants_path,
        "export type QuotaProviderType = 'antigravity' | 'claude' | 'codex' | 'kimi' | 'xai';",
        "export type QuotaProviderType = 'antigravity' | 'claude' | 'codex' | 'gemini-cli' | 'kimi' | 'xai';",
    )
    replace_once(
        constants_path,
        "export const QUOTA_PROVIDER_TYPES = new Set<QuotaProviderType>([\n"
        "  'antigravity',\n"
        "  'claude',\n"
        "  'codex',\n"
        "  'kimi',\n"
        "  'xai',\n"
        "]);",
        "export const QUOTA_PROVIDER_TYPES = new Set<QuotaProviderType>([\n"
        "  'antigravity',\n"
        "  'claude',\n"
        "  'codex',\n"
        "  'gemini-cli',\n"
        "  'kimi',\n"
        "  'xai',\n"
        "]);",
    )

    insert_once(
        quota_section_path,
        "} from '@/components/quota';\n",
        "} from '@/components/quota';\n"
        "import { GEMINI_CLI_CONFIG } from '@/extensions/quota/geminiCliQuotaConfig';\n",
        "GEMINI_CLI_CONFIG } from '@/extensions/quota/geminiCliQuotaConfig'",
    )
    replace_once(
        quota_section_path,
        "  if (type === 'codex') return CODEX_CONFIG;\n  if (type === 'kimi') return KIMI_CONFIG;",
        "  if (type === 'codex') return CODEX_CONFIG;\n"
        "  if (type === 'gemini-cli') return GEMINI_CLI_CONFIG;\n"
        "  if (type === 'kimi') return KIMI_CONFIG;",
    )
    replace_once(
        quota_section_path,
        "    if (quotaType === 'codex') return state.codexQuota[file.name] as QuotaState;\n"
        "    if (quotaType === 'kimi') return state.kimiQuota[file.name] as QuotaState;",
        "    if (quotaType === 'codex') return state.codexQuota[file.name] as QuotaState;\n"
        "    if (quotaType === 'gemini-cli') return state.geminiCliQuota[file.name] as QuotaState;\n"
        "    if (quotaType === 'kimi') return state.kimiQuota[file.name] as QuotaState;",
    )
    replace_once(
        quota_section_path,
        "    if (quotaType === 'codex') return state.setCodexQuota as unknown as (updater: unknown) => void;\n"
        "    if (quotaType === 'kimi') return state.setKimiQuota as unknown as (updater: unknown) => void;",
        "    if (quotaType === 'codex') return state.setCodexQuota as unknown as (updater: unknown) => void;\n"
        "    if (quotaType === 'gemini-cli')\n"
        "      return state.setGeminiCliQuota as unknown as (updater: unknown) => void;\n"
        "    if (quotaType === 'kimi') return state.setKimiQuota as unknown as (updater: unknown) => void;",
    )

    replace_once(
        card_path,
        "        : quotaType === 'codex'\n"
        "          ? styles.codexCard\n"
        "          : quotaType === 'kimi'",
        "        : quotaType === 'codex'\n"
        "          ? styles.codexCard\n"
        "          : quotaType === 'gemini-cli'\n"
        "            ? styles.geminiCliCard\n"
        "            : quotaType === 'kimi'",
    )
    insert_once(
        styles_path,
        ".kimiCard {\n",
        ".geminiCliCard {\n"
        "  background-image: linear-gradient(180deg, rgba(224, 232, 255, 0.08), transparent);\n"
        "}\n\n"
        ".kimiCard {\n",
        '.geminiCliCard {',
    )


def patch_auth_files_runtime_state(target: Path) -> None:
    type_path = target / 'src/types/authFile.ts'
    card_path = target / 'src/features/authFiles/components/AuthFileCard.tsx'
    page_path = target / 'src/pages/AuthFilesPage.tsx'

    insert_once(
        type_path,
        "  success?: unknown;\n",
        "  selected?: unknown;\n  success?: unknown;\n",
        "selected?: unknown;",
    )
    replace_once(
        card_path,
        "  const fileStats = {\n    success: normalizeUsageTotal(file.success),\n    failure: normalizeUsageTotal(file.failed),\n  };\n",
        "  const fileStats = {\n    selected: normalizeUsageTotal(file.selected),\n    success: normalizeUsageTotal(file.success),\n    failure: normalizeUsageTotal(file.failed),\n  };\n",
    )
    insert_once(
        card_path,
        "            <div className={`${styles.cardStats} ${compact ? styles.cardStatsCompact : ''}`}>\n",
        "            <div className={`${styles.cardStats} ${compact ? styles.cardStatsCompact : ''}`}>\n"
        "              <div className={styles.statPill}>\n"
        "                <span className={styles.statLabel}>{t('auth_files.selected_count')}</span>\n"
        "                <span className={styles.statValue}>{fileStats.selected}</span>\n"
        "              </div>\n",
        "t('auth_files.selected_count')",
    )

    insert_once(
        page_path,
        "import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';\n",
        "import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';\n"
        "import { quotaPersistenceMiddleware } from '@/extensions/quota/persistenceMiddleware';\n",
        "quotaPersistenceMiddleware } from '@/extensions/quota/persistenceMiddleware'",
    )
    replace_once(
        page_path,
        "  const handleHeaderRefresh = useCallback(async () => {\n"
        "    await Promise.all([loadFiles(), loadExcluded(), loadModelAlias()]);\n"
        "  }, [loadFiles, loadExcluded, loadModelAlias]);\n",
        "  const handleHeaderRefresh = useCallback(async () => {\n"
        "    await Promise.all([\n"
        "      loadFiles(),\n"
        "      loadExcluded(),\n"
        "      loadModelAlias(),\n"
        "      quotaPersistenceMiddleware.ensureFresh(),\n"
        "    ]);\n"
        "  }, [loadFiles, loadExcluded, loadModelAlias]);\n",
    )
    insert_once(
        page_path,
        "  const existingTypes = useMemo(() => {\n",
        "  useEffect(() => {\n"
        "    if (!isCurrentLayer) return;\n"
        "    void quotaPersistenceMiddleware.ensureFresh();\n"
        "  }, [files, isCurrentLayer]);\n\n"
        "  const existingTypes = useMemo(() => {\n",
        "}, [files, isCurrentLayer]);",
    )


def patch_runtime_detection(target: Path) -> None:
    version_path = target / 'src/services/api/version.ts'
    if "apiClient.get('/nodes')" not in read(version_path):
        return

    client_path = target / 'src/services/api/client.ts'
    insert_once(
        client_path,
        "  private managementKey: string = '';\n",
        "  private managementKey: string = '';\n  private runtimeKind: ServerRuntimeKind = 'unknown';\n",
        "private runtimeKind: ServerRuntimeKind",
    )
    replace_once(
        client_path,
        "    this.apiBase = computeApiUrl(config.apiBase);\n"
        "    this.managementKey = config.managementKey;\n"
        "\n"
        "    if (config.timeout) {\n",
        "    const nextApiBase = computeApiUrl(config.apiBase);\n"
        "    const connectionChanged =\n"
        "      this.apiBase !== nextApiBase || this.managementKey !== config.managementKey;\n"
        "    this.apiBase = nextApiBase;\n"
        "    this.managementKey = config.managementKey;\n"
        "    if (connectionChanged) {\n"
        "      this.runtimeKind = 'unknown';\n"
        "    }\n"
        "\n"
        "    if (config.timeout) {\n",
    )
    insert_once(
        client_path,
        "  private readHeader(headers: Record<string, unknown> | undefined, keys: string[]): string | null {\n",
        "  getRuntimeKind(): ServerRuntimeKind {\n"
        "    return this.runtimeKind;\n"
        "  }\n"
        "\n"
        "  private readHeader(headers: Record<string, unknown> | undefined, keys: string[]): string | null {\n",
        "getRuntimeKind(): ServerRuntimeKind",
    )
    replace_once(
        client_path,
        "        const runtimeKind: ServerRuntimeKind | null =\n"
        "          homeVersion || homeBuildDate ? 'home' : cpaVersion || cpaBuildDate ? 'cpa' : null;\n"
        "\n"
        "        // 触发版本更新事件（后续通过 store 处理）\n",
        "        const runtimeKind: ServerRuntimeKind | null =\n"
        "          homeVersion || homeBuildDate ? 'home' : cpaVersion || cpaBuildDate ? 'cpa' : null;\n"
        "        if (runtimeKind) {\n"
        "          this.runtimeKind = runtimeKind;\n"
        "        }\n"
        "\n"
        "        // 触发版本更新事件（后续通过 store 处理）\n",
    )

    replace_all(
        version_path,
        "import { isRecord } from '@/utils/helpers';\n",
        "",
    )
    replace_once(
        version_path,
        "  async detectRuntimeKind(): Promise<ServerRuntimeKind> {\n"
        "    try {\n"
        "      const data = await apiClient.get('/nodes');\n"
        "      return isRecord(data) && Array.isArray(data.nodes) ? 'home' : 'unknown';\n"
        "    } catch (error: unknown) {\n"
        "      const status = isRecord(error) ? error.status : undefined;\n"
        "      if (status === 404 || status === 405) {\n"
        "        return 'cpa';\n"
        "      }\n"
        "      return 'unknown';\n"
        "    }\n"
        "  },\n",
        "  async detectRuntimeKind(): Promise<ServerRuntimeKind> {\n"
        "    const runtimeKind = apiClient.getRuntimeKind();\n"
        "    return runtimeKind === 'unknown' ? 'cpa' : runtimeKind;\n"
        "  },\n",
    )


def patch_supporting_api_and_types(target: Path) -> None:
    config_path = target / 'src/types/config.ts'
    replace_once(
        config_path,
        "export interface Config {\n  debug?: boolean;\n",
        "export interface AuthPoolCleanConfig {\n  baseUrl?: string;\n  token?: string;\n  targetType?: string;\n  workers?: number;\n  deleteWorkers?: number;\n  timeout?: number;\n  retries?: number;\n  usedPercentThreshold?: number;\n  sampleSize?: number;\n}\n\nexport interface Config {\n  debug?: boolean;\n",
    )
    replace_once(
        config_path,
        "  quotaExceeded?: QuotaExceededConfig;\n  requestLog?: boolean;\n",
        "  quotaExceeded?: QuotaExceededConfig;\n  clean?: AuthPoolCleanConfig;\n  usageStatisticsEnabled?: boolean;\n  requestLog?: boolean;\n",
    )
    replace_once(
        config_path,
        "  | 'quota-exceeded'\n  | 'request-log'\n",
        "  | 'quota-exceeded'\n  | 'usage-statistics-enabled'\n  | 'request-log'\n",
    )

    auth_file_type_path = target / 'src/types/authFile.ts'
    replace_once(
        auth_file_type_path,
        "export interface AuthFileItem {\n  name: string;\n",
        "export interface AuthFileLastError {\n  code?: string;\n  message?: string;\n  retryable?: boolean;\n  http_status?: number;\n  httpStatus?: number;\n}\n\nexport interface AuthFileItem {\n  name: string;\n",
    )
    replace_once(
        auth_file_type_path,
        "  statusMessage?: string;\n  lastRefresh?: string | number;\n",
        "  statusMessage?: string;\n  lastError?: AuthFileLastError | null;\n  'last_error'?: AuthFileLastError | null;\n  lastRefresh?: string | number;\n",
    )

    auth_file_constants_path = target / 'src/features/authFiles/constants.ts'
    replace_once(
        auth_file_constants_path,
        "export const getAuthFileStatusMessage = (file: AuthFileItem): string => {\n  const raw = file['status_message'] ?? file.statusMessage;\n  if (typeof raw === 'string') return raw.trim();\n  if (raw == null) return '';\n  return String(raw).trim();\n};\n",
        "const normalizeAuthFileMessageValue = (value: unknown): string => {\n  if (typeof value === 'string') return value.trim();\n  if (value == null) return '';\n  return String(value).trim();\n};\n\nconst getAuthFileLastErrorMessage = (file: AuthFileItem): string => {\n  const raw = file['last_error'] ?? file.lastError;\n  if (!raw || typeof raw !== 'object') return '';\n  return normalizeAuthFileMessageValue((raw as { message?: unknown }).message);\n};\n\nexport const getAuthFileStatusMessage = (file: AuthFileItem): string => {\n  const statusMessage = normalizeAuthFileMessageValue(file['status_message'] ?? file.statusMessage);\n  return statusMessage || getAuthFileLastErrorMessage(file);\n};\n",
    )

    auth_files_path = target / 'src/services/api/authFiles.ts'
    replace_once(
        auth_files_path,
        "type AuthFileStatusResponse = { status: string; disabled: boolean };\n",
        "type AuthFileStatusResponse = { status: string; disabled: boolean };\ntype AuthFilePatchPayload = { name: string; disabled?: boolean; [key: string]: unknown };\n",
    )
    insert_once(
        auth_files_path,
        "export const authFilesApi = {\n",
        "const AUTH_FILES_LIST_CACHE_TTL_MS = 2000;\nlet authFilesListCache: { expiresAt: number; response: AuthFilesResponse } | null = null;\nlet authFilesListRequest: Promise<AuthFilesResponse> | null = null;\nlet authFilesListVersion = 0;\n\nconst cloneAuthFilesResponse = (response: AuthFilesResponse): AuthFilesResponse => ({\n  ...response,\n  files: Array.isArray(response.files) ? [...response.files] : [],\n});\n\nconst invalidateAuthFilesListCache = () => {\n  authFilesListVersion += 1;\n  authFilesListCache = null;\n  authFilesListRequest = null;\n};\n\nconst fetchAuthFilesList = async (): Promise<AuthFilesResponse> => {\n  const now = Date.now();\n  if (authFilesListCache && authFilesListCache.expiresAt > now) {\n    return cloneAuthFilesResponse(authFilesListCache.response);\n  }\n  if (!authFilesListRequest) {\n    const requestVersion = authFilesListVersion;\n    authFilesListRequest = apiClient.get<AuthFilesResponse>('/auth-files')\n      .then(dedupeAuthFilesResponse)\n      .then((response) => {\n        if (requestVersion === authFilesListVersion) {\n          authFilesListCache = {\n            expiresAt: Date.now() + AUTH_FILES_LIST_CACHE_TTL_MS,\n            response: cloneAuthFilesResponse(response),\n          };\n        }\n        return response;\n      })\n      .finally(() => {\n        if (requestVersion === authFilesListVersion) {\n          authFilesListRequest = null;\n        }\n      });\n  }\n  return cloneAuthFilesResponse(await authFilesListRequest);\n};\n\nexport const authFilesApi = {\n",
        "AUTH_FILES_LIST_CACHE_TTL_MS",
    )
    replace_once(
        auth_files_path,
        "  list: async () => dedupeAuthFilesResponse(await apiClient.get<AuthFilesResponse>('/auth-files')),\n\n  setStatus: (name: string, disabled: boolean) =>\n    apiClient.patch<AuthFileStatusResponse>('/auth-files/status', { name, disabled }),\n\n",
        "  list: fetchAuthFilesList,\n\n  patchFile: async (payload: AuthFilePatchPayload) => {\n    const response = await apiClient.patch<AuthFileStatusResponse>('/auth-files', payload);\n    invalidateAuthFilesListCache();\n    return response;\n  },\n\n  setStatus: async (name: string, disabled: boolean) => {\n    const response = await apiClient.patch<AuthFileStatusResponse>('/auth-files/status', { name, disabled });\n    invalidateAuthFilesListCache();\n    return response;\n  },\n",
    )
    replace_once(
        auth_files_path,
        "  patchFields: (name: string, fields: AuthFileFieldsPatch) =>\n    apiClient.patch('/auth-files/fields', { name, ...fields }),\n\n",
        "  setStatusWithFallback: async (name: string, disabled: boolean) => {\n    try {\n      return await authFilesApi.patchFile({ name, disabled });\n    } catch {\n      return authFilesApi.setStatus(name, disabled);\n    }\n  },\n\n  patchFields: async (name: string, fields: AuthFileFieldsPatch) => {\n    const response = await apiClient.patch('/auth-files/fields', { name, ...fields });\n    invalidateAuthFilesListCache();\n    return response;\n  },\n\n",
    )
    replace_once(
        auth_files_path,
        "    const payload = await apiClient.postForm<AuthFileBatchUploadResponse>('/auth-files', formData);\n    return normalizeBatchUploadResponse(payload, requestedNames);\n",
        "    const payload = await apiClient.postForm<AuthFileBatchUploadResponse>('/auth-files', formData);\n    invalidateAuthFilesListCache();\n    return normalizeBatchUploadResponse(payload, requestedNames);\n",
    )
    replace_once(
        auth_files_path,
        "    const payload = await apiClient.delete<AuthFileBatchDeleteResponse>('/auth-files', {\n      data: { names: requestedNames },\n    });\n    return normalizeBatchDeleteResponse(payload, requestedNames);\n",
        "    const payload = await apiClient.delete<AuthFileBatchDeleteResponse>('/auth-files', {\n      data: { names: requestedNames },\n    });\n    invalidateAuthFilesListCache();\n    return normalizeBatchDeleteResponse(payload, requestedNames);\n",
    )
    replace_once(
        auth_files_path,
        "  deleteAll: () => apiClient.delete('/auth-files', { params: { all: true } }),\n",
        "  deleteAll: async () => {\n    const response = await apiClient.delete('/auth-files', { params: { all: true } });\n    invalidateAuthFilesListCache();\n    return response;\n  },\n",
    )

    api_index_path = target / 'src/services/api/index.ts'
    replace_once(
        api_index_path,
        "export * from './apiCall';\n",
        "export * from './apiCall';\nexport * from './accountInspection';\nexport * from './routingPolicy';\n",
    )

    format_path = target / 'src/utils/format.ts'
    insert_once(
        format_path,
        "/**\n * 格式化文件大小\n */",
        "const API_KEY_MASK_REGEX =\n  /(sk-[A-Za-z0-9-_]{6,}|sk-ant-[A-Za-z0-9-_]{6,}|AIza[0-9A-Za-z-_]{8,}|AI[a-zA-Z0-9_-]{6,}|hf_[A-Za-z0-9]{6,}|pk_[A-Za-z0-9]{6,}|rk_[A-Za-z0-9]{6,})/g;\n\nexport function maskSensitiveText(value: string): string {\n  const trimmed = String(value || '').trim();\n  if (!trimmed) {\n    return '';\n  }\n\n  return trimmed.replace(API_KEY_MASK_REGEX, (match) => maskApiKey(match));\n}\n\n/**\n * 格式化文件大小\n */",
        "export function maskSensitiveText(value: string): string",
    )

    select_path = target / 'src/components/ui/Select.tsx'
    if 'triggerClassName?: string;' not in read(select_path):
        replace_once(
            select_path,
            "  placeholder?: string;\n  className?: string;\n  disabled?: boolean;\n",
            "  placeholder?: string;\n  className?: string;\n  triggerClassName?: string;\n  dropdownClassName?: string;\n  disabled?: boolean;\n",
        )
    if 'triggerClassName,' not in read(select_path):
        replace_once(
            select_path,
            "  placeholder,\n  className,\n  disabled = false,\n",
            "  placeholder,\n  className,\n  triggerClassName,\n  dropdownClassName,\n  disabled = false,\n",
        )
    if 'dropdownClassName].filter(Boolean).join' not in read(select_path):
        text = read(select_path)
        dropdown_class_replacements = [
            (
                "            className={styles.dropdown}\n",
                "            className={[styles.dropdown, dropdownClassName].filter(Boolean).join(' ')}\n",
            ),
            (
                "        className={styles.dropdown}\n",
                "        className={[styles.dropdown, dropdownClassName].filter(Boolean).join(' ')}\n",
            ),
        ]
        for old, new in dropdown_class_replacements:
            if old in text:
                write(select_path, text.replace(old, new, 1))
                break
        else:
            raise RuntimeError(f'Pattern not found in {select_path}: Select dropdown className')
    if 'triggerClassName].filter(Boolean).join' not in read(select_path):
        text = read(select_path)
        old_simple = "          className={styles.trigger}\n"
        old_sized = "          className={`${styles.trigger} ${size === 'sm' ? styles.triggerSm : ''}`.trim()}\n"
        if old_simple in text:
            write(
                select_path,
                text.replace(
                    old_simple,
                    "          className={[styles.trigger, triggerClassName].filter(Boolean).join(' ')}\n",
                    1,
                ),
            )
        elif old_sized in text:
            write(
                select_path,
                text.replace(
                    old_sized,
                    "          className={[styles.trigger, size === 'sm' ? styles.triggerSm : '', triggerClassName].filter(Boolean).join(' ')}\n",
                    1,
                ),
            )
        else:
            raise RuntimeError(f'Pattern not found in {select_path}: Select trigger className')


def patch_claude_model_id_cloak_setting(target: Path) -> None:
    visual_types = target / 'src/types/visualConfig.ts'
    replace_once(
        visual_types,
        "export type DisableImageGenerationMode = 'false' | 'true' | 'chat' | 'passthrough';\n",
        "export type DisableImageGenerationMode = 'false' | 'true' | 'chat' | 'passthrough';\n"
        "export type ClaudeModelIDCloakMode = 'auto' | 'always' | 'never';\n",
    )
    replace_once(
        visual_types,
        "  forceModelPrefix: boolean;\n  passthroughHeaders: boolean;\n",
        "  forceModelPrefix: boolean;\n  claudeModelIDCloakMode: ClaudeModelIDCloakMode;\n  passthroughHeaders: boolean;\n",
    )
    replace_once(
        visual_types,
        "  forceModelPrefix: false,\n  passthroughHeaders: false,\n",
        "  forceModelPrefix: false,\n  claudeModelIDCloakMode: 'auto',\n  passthroughHeaders: false,\n",
    )

    visual_hook = target / 'src/hooks/useVisualConfig.ts'
    replace_once(
        visual_hook,
        "  DisableImageGenerationMode,\n",
        "  ClaudeModelIDCloakMode,\n  DisableImageGenerationMode,\n",
    )
    replace_once(
        visual_hook,
        "export function parseDisableImageGenerationMode(raw: unknown): DisableImageGenerationMode {\n",
        "export function parseClaudeModelIDCloakMode(raw: unknown): ClaudeModelIDCloakMode {\n"
        "  if (typeof raw === 'string') {\n"
        "    const normalized = raw.trim().toLowerCase();\n"
        "    if (normalized === 'always' || normalized === 'never') return normalized;\n"
        "  }\n"
        "  return 'auto';\n"
        "}\n\n"
        "export function parseDisableImageGenerationMode(raw: unknown): DisableImageGenerationMode {\n",
    )
    replace_once(
        visual_hook,
        "      'forceModelPrefix',\n      'requestRetry',\n",
        "      'forceModelPrefix',\n      'claudeModelIDCloakMode',\n      'requestRetry',\n",
    )
    replace_once(
        visual_hook,
        "        forceModelPrefix: Boolean(parsed['force-model-prefix']),\n        passthroughHeaders: Boolean(parsed['passthrough-headers']),\n",
        "        forceModelPrefix: Boolean(parsed['force-model-prefix']),\n"
        "        claudeModelIDCloakMode: parseClaudeModelIDCloakMode(\n"
        "          parsed['claude-model-id-cloak-mode']\n"
        "        ),\n"
        "        passthroughHeaders: Boolean(parsed['passthrough-headers']),\n",
    )
    replace_once(
        visual_hook,
        "        if (dirtyFields.has('forceModelPrefix')) {\n"
        "          setBooleanInDoc(doc, ['force-model-prefix'], values.forceModelPrefix);\n"
        "        }\n"
        "        if (dirtyFields.has('passthroughHeaders')) {\n",
        "        if (dirtyFields.has('forceModelPrefix')) {\n"
        "          setBooleanInDoc(doc, ['force-model-prefix'], values.forceModelPrefix);\n"
        "        }\n"
        "        if (dirtyFields.has('claudeModelIDCloakMode')) {\n"
        "          setStringInDoc(\n"
        "            doc,\n"
        "            ['claude-model-id-cloak-mode'],\n"
        "            values.claudeModelIDCloakMode\n"
        "          );\n"
        "        }\n"
        "        if (dirtyFields.has('passthroughHeaders')) {\n",
    )

    visual_editor = target / 'src/components/config/VisualConfigEditor.tsx'
    replace_once(
        visual_editor,
        "  const disableImageGenerationHintId = `${disableImageGenerationLabelId}-hint`;\n"
        "  const keepaliveInputId = useId();\n",
        "  const disableImageGenerationHintId = `${disableImageGenerationLabelId}-hint`;\n"
        "  const claudeModelIDCloakModeLabelId = useId();\n"
        "  const claudeModelIDCloakModeHintId = `${claudeModelIDCloakModeLabelId}-hint`;\n"
        "  const keepaliveInputId = useId();\n",
    )
    replace_once(
        visual_editor,
        "  const countErrors = useCallback(\n",
        "  const claudeModelIDCloakModeOptions = useMemo(\n"
        "    () => [\n"
        "      {\n"
        "        value: 'auto',\n"
        "        label: t('config_management.visual.sections.advanced.claude_model_id_cloak_auto'),\n"
        "      },\n"
        "      {\n"
        "        value: 'always',\n"
        "        label: t('config_management.visual.sections.advanced.claude_model_id_cloak_always'),\n"
        "      },\n"
        "      {\n"
        "        value: 'never',\n"
        "        label: t('config_management.visual.sections.advanced.claude_model_id_cloak_never'),\n"
        "      },\n"
        "    ],\n"
        "    [t]\n"
        "  );\n\n"
        "  const countErrors = useCallback(\n",
    )
    replace_once(
        visual_editor,
        "                <Collapsible\n"
        "                  label={t('config_management.visual.sections.headers.title')}\n",
        "                <Collapsible\n"
        "                  label={t(\n"
        "                    'config_management.visual.sections.advanced.claude_model_id_cloak_title'\n"
        "                  )}\n"
        "                  hint={t(\n"
        "                    'config_management.visual.sections.advanced.claude_model_id_cloak_description'\n"
        "                  )}\n"
        "                  defaultOpen={false}\n"
        "                >\n"
        "                  <SectionGrid>\n"
        "                    <FieldAnchor fieldId=\"claudeModelIDCloakMode\">\n"
        "                      <FieldShell\n"
        "                        label={t(\n"
        "                          'config_management.visual.sections.advanced.claude_model_id_cloak_label'\n"
        "                        )}\n"
        "                        labelId={claudeModelIDCloakModeLabelId}\n"
        "                        hint={t(\n"
        "                          'config_management.visual.sections.advanced.claude_model_id_cloak_hint'\n"
        "                        )}\n"
        "                        hintId={claudeModelIDCloakModeHintId}\n"
        "                      >\n"
        "                        <Select\n"
        "                          value={values.claudeModelIDCloakMode}\n"
        "                          options={claudeModelIDCloakModeOptions}\n"
        "                          id={`${claudeModelIDCloakModeLabelId}-select`}\n"
        "                          disabled={disabled}\n"
        "                          ariaLabelledBy={claudeModelIDCloakModeLabelId}\n"
        "                          ariaDescribedBy={claudeModelIDCloakModeHintId}\n"
        "                          onChange={(nextValue) =>\n"
        "                            onChange({\n"
        "                              claudeModelIDCloakMode:\n"
        "                                nextValue as VisualConfigValues['claudeModelIDCloakMode'],\n"
        "                            })\n"
        "                          }\n"
        "                        />\n"
        "                      </FieldShell>\n"
        "                    </FieldAnchor>\n"
        "                  </SectionGrid>\n"
        "                </Collapsible>\n\n"
        "                <Collapsible\n"
        "                  label={t('config_management.visual.sections.headers.title')}\n",
    )

    search_index = target / 'src/components/config/configSearchIndex.ts'
    replace_once(
        search_index,
        "  // Claude header defaults — qualifierKey disambiguates the shared \"User-Agent\" label.\n",
        "  {\n"
        "    fieldId: 'claudeModelIDCloakMode',\n"
        "    sectionId: 'advanced',\n"
        "    labelKey: L('sections.advanced.claude_model_id_cloak_label'),\n"
        "    hintKey: L('sections.advanced.claude_model_id_cloak_hint'),\n"
        "    yamlKeys: ['claude-model-id-cloak-mode'],\n"
        "    keywords: ['anthropic', 'claude desktop', 'claude code', 'model id', 'cloak'],\n"
        "  },\n"
        "  // Claude header defaults — qualifierKey disambiguates the shared \"User-Agent\" label.\n",
    )


def patch_locales(target: Path) -> None:
    monitoring = json.loads(LOCALES_FILE.read_text())
    locales_dir = target / 'src/i18n/locales'
    for locale_path in sorted(locales_dir.glob('*.json')):
        data = json.loads(locale_path.read_text())
        additions = monitoring.get(locale_path.name, {})
        data.setdefault('nav', {}).update(additions.get('nav', {}))
        nav_additions = additions.get('nav', {})
        data.setdefault('nav_meta', {}).update(
            additions.get(
                'nav_meta',
                {
                    'monitoring_center': nav_additions.get('monitoring_center', 'Request Monitoring'),
                    'account_inspection': nav_additions.get('account_inspection', 'Account Inspection'),
                    'routing_policy': nav_additions.get('routing_policy', 'Routing Policy'),
                },
            )
        )
        data['monitoring'] = additions.get('monitoring', data.get('monitoring', {}))
        data['usage_stats'] = additions.get('usage_stats', data.get('usage_stats', {}))
        data['routing_policy'] = additions.get('routing_policy', data.get('routing_policy', {}))
        data.setdefault('quota_management', {}).update(QUOTA_LOCALE_KEYS.get(locale_path.name, {}))
        gemini_cli_locale = GEMINI_CLI_LOCALE_KEYS.get(locale_path.name, GEMINI_CLI_LOCALE_KEYS['en.json'])
        data.setdefault('auth_files', {})['filter_gemini-cli'] = gemini_cli_locale['auth_filter']
        data.setdefault('auth_files', {})['search_placeholder'] = AUTH_FILES_SEARCH_PLACEHOLDER_KEYS.get(
            locale_path.name,
            AUTH_FILES_SEARCH_PLACEHOLDER_KEYS['en.json'],
        )
        data.setdefault('auth_files', {})['sort_plan_desc'] = AUTH_FILES_PLAN_SORT_LABEL_KEYS.get(
            locale_path.name,
            AUTH_FILES_PLAN_SORT_LABEL_KEYS['en.json'],
        )
        data.setdefault('auth_files', {})['sort_quota_desc'] = AUTH_FILES_QUOTA_SORT_LABEL_KEYS.get(
            locale_path.name,
            AUTH_FILES_QUOTA_SORT_LABEL_KEYS['en.json'],
        )
        data.setdefault('auth_files', {})['selected_count'] = AUTH_FILES_SELECTED_COUNT_LABEL_KEYS.get(
            locale_path.name,
            AUTH_FILES_SELECTED_COUNT_LABEL_KEYS['en.json'],
        )
        data.setdefault('gemini_cli_quota', {}).update(gemini_cli_locale['quota'])
        cloak_locale = CLAUDE_MODEL_ID_CLOAK_LOCALE_KEYS.get(
            locale_path.name,
            CLAUDE_MODEL_ID_CLOAK_LOCALE_KEYS['en.json'],
        )
        advanced_locale = (
            data.setdefault('config_management', {})
            .setdefault('visual', {})
            .setdefault('sections', {})
            .setdefault('advanced', {})
        )
        advanced_locale.update(
            {
                'claude_model_id_cloak_title': cloak_locale['title'],
                'claude_model_id_cloak_description': cloak_locale['description'],
                'claude_model_id_cloak_label': cloak_locale['label'],
                'claude_model_id_cloak_hint': cloak_locale['hint'],
                'claude_model_id_cloak_auto': cloak_locale['auto'],
                'claude_model_id_cloak_always': cloak_locale['always'],
                'claude_model_id_cloak_never': cloak_locale['never'],
            }
        )
        locale_path.write_text(json.dumps(data, ensure_ascii=False, indent=2) + '\n')


def main() -> None:
    if len(sys.argv) > 2:
        raise SystemExit('Usage: apply_customizations.py [target_dir]')
    target = Path(sys.argv[1] if len(sys.argv) == 2 else '.').resolve()
    if not (target / 'src').is_dir() or not (target / 'package.json').is_file():
        raise SystemExit(f'Target directory does not look like the upstream project: {target}')
    if not OVERLAY_DIR.is_dir():
        raise SystemExit(f'Overlay directory not found: {OVERLAY_DIR}')

    copy_overlay(target)
    patch_modal_focus_restore(target)
    patch_modal_scroll_lock(target)
    patch_modal_content_scrollbar_layout(target)
    patch_routes(target)
    patch_layout(target)
    patch_icons(target)
    patch_quota_types(target)
    patch_quota_store(target)
    patch_quota_constants(target)
    patch_quota_configs(target)
    patch_antigravity_quota_builders(target)
    patch_quota_page(target)
    patch_quota_page_search(target)
    patch_quota_card(target)
    patch_quota_styles(target)
    patch_account_inspection_page(target)
    patch_auth_files_page_search(target)
    patch_auth_files_page_sorting(target)
    patch_auth_files_gemini_quota(target)
    patch_auth_files_runtime_state(target)
    patch_runtime_detection(target)
    patch_api_client_connection_isolation(target)
    patch_supporting_api_and_types(target)
    patch_claude_model_id_cloak_setting(target)
    patch_locales(target)
    flush_writes()
    print(f'OK: CPA-Management customization applied to {target}')


if __name__ == '__main__':
    main()
