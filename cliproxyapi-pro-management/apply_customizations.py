#!/usr/bin/env python3
import hashlib
import json
import sys
from pathlib import Path

CUSTOMIZATION_DIR = Path(__file__).resolve().parent
OVERLAY_DIR = CUSTOMIZATION_DIR / 'overlay'
LOCALES_FILE = CUSTOMIZATION_DIR / 'monitoring-locales.json'
OVERLAY_REPLACEMENTS_FILE = CUSTOMIZATION_DIR / 'overlay-replacements.json'

MANAGEMENT_UPDATE_LOCALE_KEYS = {
    'en.json': {
        'management_check_update_button': 'Check for updates',
        'management_check_update_updated': 'Management Center updated. Reloading...',
        'management_check_update_unchanged': 'Update check completed; no update was applied.',
        'management_check_update_error': 'Failed to check Management Center update',
    },
    'ru.json': {
        'management_check_update_button': 'Проверить обновления',
        'management_check_update_updated': 'Центр управления обновлён. Перезагрузка...',
        'management_check_update_unchanged': 'Проверка завершена; обновление не применялось.',
        'management_check_update_error': 'Не удалось проверить обновление Центра управления',
    },
    'zh-CN.json': {
        'management_check_update_button': '检查更新',
        'management_check_update_updated': '管理中心已更新，正在重新加载...',
        'management_check_update_unchanged': '检查完成，本次未进行更新。',
        'management_check_update_error': '管理中心更新检查失败',
    },
    'zh-TW.json': {
        'management_check_update_button': '檢查更新',
        'management_check_update_updated': '管理中心已更新，正在重新載入...',
        'management_check_update_unchanged': '檢查完成，本次未進行更新。',
        'management_check_update_error': '管理中心更新檢查失敗',
    },
}

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

XAI_QUOTA_LOCALE_KEYS = {
    'en.json': {
        'plan_x_premium_plus': 'X Premium+',
        'plan_paid_unknown': 'Paid (unknown tier)',
        'free_quota': 'Free token quota',
        'free_quota_exhausted': 'Exhausted',
        'free_quota_window': 'Rolling 24 hours',
    },
    'ru.json': {
        'plan_x_premium_plus': 'X Premium+',
        'plan_paid_unknown': 'Платный (неизвестный уровень)',
        'free_quota': 'Бесплатная квота токенов',
        'free_quota_exhausted': 'Исчерпана',
        'free_quota_window': 'Скользящие 24 часа',
    },
    'zh-CN.json': {
        'plan_x_premium_plus': 'X Premium+',
        'plan_paid_unknown': '付费版（未知档位）',
        'free_quota': '免费 Token 额度',
        'free_quota_exhausted': '已耗尽',
        'free_quota_window': '滚动 24 小时',
    },
    'zh-TW.json': {
        'plan_x_premium_plus': 'X Premium+',
        'plan_paid_unknown': '付費版（未知級別）',
        'free_quota': '免費 Token 配額',
        'free_quota_exhausted': '已用盡',
        'free_quota_window': '滾動 24 小時',
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

PROXY_POOL_NAV_LOCALE_KEYS = {
    'en.json': {'label': 'Proxy Management', 'meta': 'Rotating upstream proxy gateway'},
    'ru.json': {'label': 'Управление прокси', 'meta': 'Шлюз ротации внешних прокси'},
    'zh-CN.json': {'label': '代理管理', 'meta': '多节点轮询与故障转移'},
    'zh-TW.json': {'label': '代理管理', 'meta': '多節點輪詢與故障轉移'},
}

OAUTH_MODEL_POLICY_NAV_LOCALE_KEYS = {
    'en.json': {'label': 'Model Policy', 'meta': 'Per-plan model availability rules'},
    'ru.json': {'label': 'Политика моделей', 'meta': 'Правила доступности моделей по тарифам'},
    'zh-CN.json': {'label': '模型策略', 'meta': '按账号套餐配置模型可用范围'},
    'zh-TW.json': {'label': '模型策略', 'meta': '依帳號套餐設定模型可用範圍'},
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
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)
    _writes.clear()


def discard_writes() -> None:
    _writes.clear()


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


def auth_files_page_path(target: Path) -> Path:
    for relative in ('src/features/authFiles/AuthFilesPage.tsx', 'src/pages/AuthFilesPage.tsx'):
        path = target / relative
        if path.is_file():
            return path
    raise RuntimeError(f'AuthFilesPage.tsx not found under {target}')


def auth_files_styles_path(target: Path) -> Path:
    for relative in (
        'src/features/authFiles/AuthFilesPage.module.scss',
        'src/pages/AuthFilesPage.module.scss',
    ):
        path = target / relative
        if path.is_file():
            return path
    raise RuntimeError(f'AuthFilesPage.module.scss not found under {target}')


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
        if src.is_dir():
            continue
        rel = src.relative_to(OVERLAY_DIR)
        dst = target / rel
        write(dst, src.read_text())


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
    insert_once(
        client,
        "  private managementKey: string = '';\n",
        "  private managementKey: string = '';\n"
        "  private connectionGeneration: number = 0;\n"
        "  private connectionAbortController = new AbortController();\n",
        "private connectionGeneration: number",
    )
    replace_once_if_present(
        client,
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
        "      this.connectionAbortController.abort();\n"
        "      this.connectionAbortController = new AbortController();\n"
        "      this.connectionGeneration += 1;\n"
        "    }\n"
        "\n"
        "    if (config.timeout) {\n",
    )
    replace_once_if_present(
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
    if 'this.connectionGeneration += 1;' not in read(client):
        raise RuntimeError(f'Pattern not found in {client}: connection change handling')
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
        "import { QuotaPage } from '@/pages/QuotaPage';\nimport { MonitoringCenterPage } from '@/pages/MonitoringCenterPage';\nimport { AccountInspectionPage } from '@/pages/AccountInspectionPage';\nimport { RoutingPolicyPage } from '@/pages/RoutingPolicyPage';\nimport { ProxyPoolPage } from '@/pages/ProxyPoolPage';\nimport { OAuthModelPolicyPage } from '@/pages/OAuthModelPolicyPage';\n",
    )
    replace_once(
        path,
        "  { path: '/quota', element: <QuotaPage /> },\n",
        "  { path: '/quota', element: <QuotaPage /> },\n  { path: '/monitoring', element: <MonitoringCenterPage /> },\n  { path: '/account-inspection', element: <AccountInspectionPage /> },\n  { path: '/routing', element: <RoutingPolicyPage /> },\n  { path: '/proxy-pool', element: <ProxyPoolPage /> },\n  { path: '/oauth-model-policy', element: <OAuthModelPolicyPage /> },\n",
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
        "  IconModelCluster,\n  IconSidebarAccountInspection,\n  IconSidebarMonitor,\n  IconSidebarProxyPool,\n  IconSidebarRouting,\n  IconSidebarProviders,\n",
        "  IconSidebarAccountInspection,\n",
    )
    replace_once(
        path,
        "  oauth: <IconSidebarOauth size={18} />,\n  quota: <IconSidebarQuota size={18} />,\n",
        "  oauth: <IconSidebarOauth size={18} />,\n  quota: <IconSidebarQuota size={18} />,\n  monitoring: <IconSidebarMonitor size={18} />,\n  accountInspection: <IconSidebarAccountInspection size={18} />,\n  routing: <IconSidebarRouting size={18} />,\n  proxyPool: <IconSidebarProxyPool size={18} />,\n  oauthModelPolicy: <IconModelCluster size={18} />,\n",
    )
    insert_once(
        path,
        "              {\n                path: '/plugins',\n",
        "              {\n"
        "                path: '/proxy-pool',\n"
        "                labelKey: 'nav.proxy_pool',\n"
        "                metaKey: 'nav_meta.proxy_pool',\n"
        "                icon: sidebarIcons.proxyPool,\n"
        "              },\n"
        "              {\n"
        "                path: '/oauth-model-policy',\n"
        "                labelKey: 'nav.oauth_model_policy',\n"
        "                metaKey: 'nav_meta.oauth_model_policy',\n"
        "                icon: sidebarIcons.oauthModelPolicy,\n"
        "              },\n"
        "              {\n"
        "                path: '/plugins',\n",
        "path: '/proxy-pool',",
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
    proxy_pool_icon = (
        "export function IconSidebarProxyPool({ size = 20, ...props }: IconProps) {\n"
        "  return (\n"
        f"    <svg {{...{svg_props}}} width={{size}} height={{size}} {{...props}}>\n"
        "      <circle cx=\"6\" cy=\"7\" r=\"2.5\" />\n"
        "      <circle cx=\"18\" cy=\"7\" r=\"2.5\" />\n"
        "      <circle cx=\"12\" cy=\"18\" r=\"2.5\" />\n"
        "      <path d=\"M8.5 7h7\" />\n"
        "      <path d=\"m7.4 9 3.2 6.6\" />\n"
        "      <path d=\"m16.6 9-3.2 6.6\" />\n"
        "      <path d=\"m12.5 4.5 2 2.5-2 2.5\" />\n"
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
    if "export function IconSidebarProxyPool" not in text:
        icons_to_insert += proxy_pool_icon
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
    insert_once(
        path,
        "export interface XaiBillingSummary {\n",
        "export interface XaiFreeQuotaSummary {\n"
        "  source?: 'rate_limit_headers' | 'free_usage_exhausted';\n"
        "  windowKind?: 'rolling_24h' | string;\n"
        "  usedTokens?: number | string;\n"
        "  limitTokens?: number | string;\n"
        "  remainingTokens?: number | string;\n"
        "  limitRequests?: number | string;\n"
        "  remainingRequests?: number | string;\n"
        "  observedAt?: number | string;\n"
        "  exhausted?: boolean;\n"
        "  model?: string;\n"
        "}\n\n"
        "export interface XaiBillingSummary {\n",
        "export interface XaiFreeQuotaSummary",
    )
    replace_once(
        path,
        "  planType?: 'paid';\n",
        "  planType?: 'free' | 'supergrok' | 'x-premium-plus' | 'supergrok-heavy' | 'paid' | 'paid-unknown';\n",
    )
    replace_once(
        path,
        "  usedPercent: number | null;\n}\n\nexport interface XaiQuotaState",
        "  usedPercent: number | null;\n  freeQuota?: XaiFreeQuotaSummary;\n}\n\nexport interface XaiQuotaState",
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
        "  XaiBillingSummary,\n  XaiQuotaState,",
        "  XaiBillingSummary,\n  XaiFreeQuotaSummary,\n  XaiQuotaState,",
    )
    insert_once(
        path,
        "import type { QuotaRenderHelpers } from './QuotaCard';\n",
        "import { useQuotaStore } from '@/stores';\n"
        "import {\n"
        "  XAI_FREE_QUOTA_PROBE_URL,\n"
        "  mergeXaiBillingRuntimeState,\n"
        "  parseXaiFreeQuotaProbe,\n"
        "  resolveXaiPlanType,\n"
        "  xaiFreeQuotaUsedPercent,\n"
        "  type XaiNormalizedPlanType,\n"
        "} from '@/extensions/quota/xaiQuota';\n"
        "import type { QuotaRenderHelpers } from './QuotaCard';\n",
        "mergeXaiBillingRuntimeState",
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

    replace_once(
        path,
        "const requestXaiPaidHealth = async (authIndex: string): Promise<XaiBillingSummary> => {\n",
        "const requestXaiFreeQuota = async (\n"
        "  authIndex: string,\n"
        "  t: TFunction\n"
        "): Promise<XaiFreeQuotaSummary> => {\n"
        "  const result = await apiCallApi.request(\n"
        "    {\n"
        "      authIndex,\n"
        "      method: 'POST',\n"
        "      url: XAI_FREE_QUOTA_PROBE_URL,\n"
        "      header: {\n"
        "        ...XAI_REQUEST_HEADERS,\n"
        "        accept: 'text/event-stream',\n"
        "        'Content-Type': 'application/json',\n"
        "      },\n"
        "      data: JSON.stringify({\n"
        "        model: XAI_PAID_HEALTH_MODEL,\n"
        "        input: [\n"
        "          {\n"
        "            role: 'user',\n"
        "            content: [{ type: 'input_text', text: 'ping' }],\n"
        "          },\n"
        "        ],\n"
        "        instructions: 'You are a helpful assistant. Reply briefly.',\n"
        "        max_output_tokens: 1,\n"
        "        stream: true,\n"
        "        store: false,\n"
        "      }),\n"
        "      useExecutor: true,\n"
        "    },\n"
        "    { timeout: XAI_PAID_HEALTH_REQUEST_TIMEOUT_MS }\n"
        "  );\n"
        "  const quota = parseXaiFreeQuotaProbe(result, XAI_PAID_HEALTH_MODEL);\n"
        "  if (quota) return quota;\n"
        "  if (result.statusCode < 200 || result.statusCode >= 300) {\n"
        "    throw createStatusError(getApiCallErrorMessage(result), result.statusCode);\n"
        "  }\n"
        "  throw new Error(t('xai_quota.empty_data'));\n"
        "};\n\n"
        "const requestXaiPaidHealth = async (authIndex: string): Promise<XaiBillingSummary> => {\n",
    )
    replace_once(
        path,
        "  if (isPaidXaiAuthFile(file)) {\n    return requestXaiPaidHealth(authIndex);\n  }\n",
        "  const previousBilling = useQuotaStore.getState().xaiQuota[file.name]?.billing;\n"
        "  const mergeRuntimeState = (billing: XaiBillingSummary): XaiBillingSummary =>\n"
        "    mergeXaiBillingRuntimeState(\n"
        "      billing,\n"
        "      previousBilling\n"
        "    );\n\n"
        "  if (isPaidXaiAuthFile(file)) {\n"
        "    return mergeRuntimeState(await requestXaiPaidHealth(authIndex));\n"
        "  }\n",
    )
    replace_once(
        path,
        "  const summary = mergeXaiBillingSummaries(weeklySummary, monthlySummary);\n  if (summary) return summary;\n",
        "  const summary = mergeXaiBillingSummaries(weeklySummary, monthlySummary);\n"
        "  if (summary) {\n"
        "    const planType = resolveXaiPlanType(\n"
        "      summary.monthlyLimitCents,\n"
        "      monthlyResult.status === 'fulfilled'\n"
        "    );\n"
        "    const effectivePlanType = planType ?? previousBilling?.planType;\n"
        "    const freeQuota =\n"
        "      effectivePlanType === 'free' ? await requestXaiFreeQuota(authIndex, t) : undefined;\n"
        "    const billing = mergeRuntimeState({\n"
        "      ...summary,\n"
        "      planType,\n"
        "    });\n"
        "    return freeQuota ? { ...billing, freeQuota } : billing;\n"
        "  }\n",
    )
    replace_once(
        path,
        "  try {\n    return await requestXaiPaidHealth(authIndex);\n  } catch {\n",
        "  try {\n    return mergeRuntimeState(await requestXaiPaidHealth(authIndex));\n  } catch {\n",
    )
    replace_once(
        path,
        "const XAI_SUPERGROK_LIMIT_CENTS = 15_000;\n"
        "const XAI_SUPERGROK_HEAVY_LIMIT_CENTS = 150_000;\n\n"
        "const resolveXaiPlan = (\n"
        "  monthlyLimitCents: number | null\n"
        "): { labelKey: string; premium: boolean } | null => {\n"
        "  if (monthlyLimitCents === XAI_SUPERGROK_LIMIT_CENTS) {\n"
        "    return { labelKey: 'plan_supergrok', premium: false };\n"
        "  }\n"
        "  if (monthlyLimitCents === XAI_SUPERGROK_HEAVY_LIMIT_CENTS) {\n"
        "    return { labelKey: 'plan_supergrok_heavy', premium: true };\n"
        "  }\n"
        "  return null;\n"
        "};\n",
        "const resolveXaiPlan = (\n"
        "  billing: XaiBillingSummary\n"
        "): { labelKey?: string; label?: string; premium: boolean } | null => {\n"
        "  const planType = billing.planType ?? resolveXaiPlanType(\n"
        "    billing.monthlyLimitCents,\n"
        "    billing.monthlyLimitCents !== null\n"
        "  );\n"
        "  const plans: Partial<Record<XaiNormalizedPlanType, { labelKey?: string; label?: string; premium: boolean }>> = {\n"
        "    free: { label: 'Free', premium: false },\n"
        "    supergrok: { labelKey: 'plan_supergrok', premium: false },\n"
        "    'x-premium-plus': { labelKey: 'plan_x_premium_plus', premium: true },\n"
        "    'supergrok-heavy': { labelKey: 'plan_supergrok_heavy', premium: true },\n"
        "    paid: { labelKey: 'plan_paid', premium: true },\n"
        "    'paid-unknown': { labelKey: 'plan_paid_unknown', premium: true },\n"
        "  };\n"
        "  return planType ? plans[planType] ?? null : null;\n"
        "};\n",
    )
    replace_once(
        path,
        "  const plan = resolveXaiPlan(billing.monthlyLimitCents);\n",
        "  const plan = resolveXaiPlan(billing);\n"
        "  const planType = billing.planType ?? resolveXaiPlanType(\n"
        "    billing.monthlyLimitCents,\n"
        "    billing.monthlyLimitCents !== null\n"
        "  );\n"
        "  const freeQuota = planType === 'free' ? billing.freeQuota : undefined;\n"
        "  const freeQuotaUsed = freeQuota ? xaiFreeQuotaUsedPercent(billing) : null;\n"
        "  const freeQuotaRemaining =\n"
        "    freeQuotaUsed === null ? null : Math.max(0, Math.min(100, 100 - freeQuotaUsed));\n"
        "  const freeQuotaLabel = freeQuota?.model\n"
        "    ? `${t('xai_quota.free_quota')} · ${freeQuota.model}`\n"
        "    : t('xai_quota.free_quota');\n",
    )
    replace_once(
        path,
        "            t(`xai_quota.${plan.labelKey}`)\n",
        "            plan.label ?? t(`xai_quota.${plan.labelKey}`)\n",
    )
    replace_once(
        path,
        "    hasWeeklyData\n      ? h(\n",
        "    freeQuota\n"
        "      ? h(\n"
        "          'div',\n"
        "          { key: 'free-quota', className: styleMap.quotaRow },\n"
        "          h(\n"
        "            'div',\n"
        "            { className: styleMap.quotaRowHeader },\n"
        "            h('span', { className: styleMap.quotaModel }, freeQuotaLabel),\n"
        "            h(\n"
        "              'div',\n"
        "              { className: styleMap.quotaMeta },\n"
        "              h(\n"
        "                'span',\n"
        "                { className: styleMap.quotaPercent },\n"
        "                freeQuota.exhausted\n"
        "                  ? t('xai_quota.free_quota_exhausted')\n"
        "                  : t('xai_quota.used_percent', { percent: formatXaiPercent(freeQuotaUsed) })\n"
        "              ),\n"
        "              h('span', { className: styleMap.quotaReset }, t('xai_quota.free_quota_window'))\n"
        "            )\n"
        "          ),\n"
        "          h(QuotaProgressBar, {\n"
        "            percent: freeQuotaRemaining,\n"
        "            highThreshold: QUOTA_PROGRESS_HIGH_THRESHOLD,\n"
        "            mediumThreshold: QUOTA_PROGRESS_MEDIUM_THRESHOLD,\n"
        "          })\n"
        "        )\n"
        "      : null,\n"
        "    hasWeeklyData\n      ? h(\n",
    )


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
    insert_once(
        path,
        "import { useAuthStore } from '@/stores';\n",
        "import { quotaPersistenceMiddleware } from '@/extensions/quota/persistenceMiddleware';\nimport { useAuthStore } from '@/stores';\n",
        "quotaPersistenceMiddleware",
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
    replace_once(
        page_path,
        "import { useAuthStore } from '@/stores';\n",
        "import { useAuthStore, useQuotaStore } from '@/stores';\n",
    )
    insert_once(
        page_path,
        "import { GEMINI_CLI_CONFIG } from '@/extensions/quota/geminiCliQuotaConfig';\n",
        "import { GEMINI_CLI_CONFIG } from '@/extensions/quota/geminiCliQuotaConfig';\n"
        "import { resolveXaiPlanType } from '@/extensions/quota/xaiQuota';\n",
        "resolveXaiPlanType",
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
        "const PREMIUM_QUOTA_CODEX_PLAN_TYPES = new Set(['pro', 'prolite', 'pro-lite', 'pro_lite']);\n"
        "type QuotaSearchTranslate = (key: string) => string;\n"
        "type QuotaSearchStore = Pick<\n"
        "  ReturnType<typeof useQuotaStore.getState>,\n"
        "  'antigravityQuota' | 'claudeQuota' | 'codexQuota' | 'geminiCliQuota' | 'xaiQuota'\n"
        ">;\n"
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
        "const addQuotaSearchValue = (values: string[], value: unknown) => {\n"
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
        "const toQuotaSearchRecord = (value: unknown): Record<string, unknown> | null =>\n"
        "  value && typeof value === 'object' && !Array.isArray(value)\n"
        "    ? (value as Record<string, unknown>)\n"
        "    : null;\n"
        "\n"
        "const normalizeQuotaSearchPlan = (value: unknown): string =>\n"
        "  typeof value === 'string' ? value.trim().toLowerCase().replace(/_/g, '-') : '';\n"
        "\n"
        "const addQuotaCodexPlanSearchValues = (\n"
        "  values: string[],\n"
        "  planType: unknown,\n"
        "  t: QuotaSearchTranslate\n"
        ") => {\n"
        "  const normalized = normalizeQuotaSearchPlan(planType);\n"
        "  if (!normalized) return;\n"
        "  values.push(normalized, normalized.replace(/-/g, ' '));\n"
        "  if (normalized === 'pro') values.push(t('codex_quota.plan_pro'));\n"
        "  else if (PREMIUM_QUOTA_CODEX_PLAN_TYPES.has(normalized)) values.push(t('codex_quota.plan_prolite'));\n"
        "  else if (normalized === 'plus') values.push(t('codex_quota.plan_plus'));\n"
        "  else if (normalized === 'team') values.push(t('codex_quota.plan_team'));\n"
        "  else if (normalized === 'free') values.push(t('codex_quota.plan_free'));\n"
        "};\n"
        "\n"
        "const addQuotaClaudePlanSearchValues = (\n"
        "  values: string[],\n"
        "  planType: unknown,\n"
        "  t: QuotaSearchTranslate\n"
        ") => {\n"
        "  const raw = typeof planType === 'string' ? planType.trim() : '';\n"
        "  if (!raw) return;\n"
        "  values.push(raw, raw.replace(/^plan[_-]/i, '').replace(/[_-]/g, ' '));\n"
        "  values.push(t(`claude_quota.${raw}`));\n"
        "};\n"
        "\n"
        "const addQuotaAntigravityPlanSearchValues = (\n"
        "  values: string[],\n"
        "  subscription: unknown,\n"
        "  t: QuotaSearchTranslate\n"
        ") => {\n"
        "  const record = toQuotaSearchRecord(subscription);\n"
        "  if (!record) return;\n"
        "  const plan = normalizeQuotaSearchPlan(record.plan);\n"
        "  addQuotaSearchValue(values, record.plan);\n"
        "  addQuotaSearchValue(values, record.tierName);\n"
        "  addQuotaSearchValue(values, record.tierId);\n"
        "  if (plan === 'free') values.push(t('antigravity_subscription.plan_free'));\n"
        "  else if (plan === 'pro') values.push(t('antigravity_subscription.plan_pro'));\n"
        "  else if (plan === 'ultra') values.push(t('antigravity_subscription.plan_ultra'));\n"
        "  else if (plan === 'ultra-lite') values.push(t('antigravity_subscription.plan_ultra_lite'));\n"
        "};\n"
        "\n"
        "const normalizeQuotaSearchCents = (value: unknown): number | null => {\n"
        "  const source = toQuotaSearchRecord(value)?.val ?? value;\n"
        "  if (typeof source === 'number' && Number.isFinite(source)) return source;\n"
        "  if (typeof source !== 'string') return null;\n"
        "  const parsed = Number(source.trim());\n"
        "  return Number.isFinite(parsed) ? parsed : null;\n"
        "};\n"
        "\n"
        "const addQuotaXaiPlanSearchValues = (\n"
        "  values: string[],\n"
        "  billing: unknown,\n"
        "  t: QuotaSearchTranslate\n"
        ") => {\n"
        "  const record = toQuotaSearchRecord(billing);\n"
        "  if (!record) return;\n"
        "  const monthlyLimitCents = normalizeQuotaSearchCents(record.monthlyLimitCents);\n"
        "  const storedPlanType = normalizeQuotaSearchPlan(record.planType ?? record.plan_type);\n"
        "  const planType = storedPlanType || resolveXaiPlanType(monthlyLimitCents, monthlyLimitCents !== null);\n"
        "  if (!planType) return;\n"
        "  values.push(planType, planType.replace(/-/g, ' '));\n"
        "  if (planType === 'free') values.push('Free');\n"
        "  else if (planType === 'supergrok') values.push(t('xai_quota.plan_supergrok'), 'supergrok');\n"
        "  else if (planType === 'x-premium-plus') values.push(t('xai_quota.plan_x_premium_plus'), 'x premium+');\n"
        "  else if (planType === 'supergrok-heavy') values.push(t('xai_quota.plan_supergrok_heavy'), 'supergrok heavy');\n"
        "  else if (planType === 'paid') values.push(t('xai_quota.plan_paid'));\n"
        "  else if (planType === 'paid-unknown') values.push(t('xai_quota.plan_paid_unknown'));\n"
        "};\n"
        "\n"
        "const buildQuotaStateSearchValues = (\n"
        "  item: AuthFileItem,\n"
        "  quotaStore: QuotaSearchStore,\n"
        "  t: QuotaSearchTranslate\n"
        "): string[] => {\n"
        "  const values: string[] = [];\n"
        "  const name = typeof item.name === 'string' ? item.name : '';\n"
        "  if (!name) return values;\n"
        "  addQuotaAntigravityPlanSearchValues(values, quotaStore.antigravityQuota[name]?.subscription, t);\n"
        "  addQuotaClaudePlanSearchValues(values, quotaStore.claudeQuota[name]?.planType, t);\n"
        "  addQuotaCodexPlanSearchValues(values, quotaStore.codexQuota[name]?.planType, t);\n"
        "  addQuotaSearchValue(values, quotaStore.geminiCliQuota[name]?.tierLabel);\n"
        "  addQuotaSearchValue(values, quotaStore.geminiCliQuota[name]?.tierId);\n"
        "  addQuotaSearchValue(values, quotaStore.geminiCliQuota[name]?.creditBalance);\n"
        "  addQuotaXaiPlanSearchValues(values, quotaStore.xaiQuota[name]?.billing, t);\n"
        "  return values;\n"
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
        "const buildQuotaSearchValues = (\n"
        "  item: AuthFileItem,\n"
        "  quotaStore: QuotaSearchStore,\n"
        "  t: QuotaSearchTranslate\n"
        "): string[] => [\n"
        "  ...QUOTA_SEARCH_FIELD_KEYS.flatMap((key) => collectQuotaSearchValues(item[key])),\n"
        "  ...buildQuotaStateSearchValues(item, quotaStore, t),\n"
        "];\n"
        "\n"
        "export function QuotaPage() {\n",
        "QUOTA_SEARCH_FIELD_KEYS",
    )
    replace_once(
        page_path,
        "  const { t } = useTranslation();\n  const connectionStatus = useAuthStore((state) => state.connectionStatus);\n\n  const [files, setFiles] = useState<AuthFileItem[]>([]);",
        "  const { t } = useTranslation();\n"
        "  const connectionStatus = useAuthStore((state) => state.connectionStatus);\n"
        "  const antigravityQuota = useQuotaStore((state) => state.antigravityQuota);\n"
        "  const claudeQuota = useQuotaStore((state) => state.claudeQuota);\n"
        "  const codexQuota = useQuotaStore((state) => state.codexQuota);\n"
        "  const geminiCliQuota = useQuotaStore((state) => state.geminiCliQuota);\n"
        "  const xaiQuota = useQuotaStore((state) => state.xaiQuota);\n"
        "  const quotaSearchStore = useMemo(\n"
        "    () => ({ antigravityQuota, claudeQuota, codexQuota, geminiCliQuota, xaiQuota }),\n"
        "    [antigravityQuota, claudeQuota, codexQuota, geminiCliQuota, xaiQuota]\n"
        "  );\n"
        "\n"
        "  const [files, setFiles] = useState<AuthFileItem[]>([]);",
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
        "          buildQuotaSearchValues(item, quotaSearchStore, t).some((value) =>\n"
        "            wildcardSearch\n"
        "              ? wildcardSearch.test(value)\n"
        "              : value.toLowerCase().includes(normalizedTerm)\n"
        "          )\n"
        "        )\n"
        "        .map((item) => item.name)\n"
        "    );\n"
        "  }, [files, normalizedSearch, quotaSearchStore, t, wildcardSearch]);\n"
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
    insert_once(
        path,
        "import styles from '@/pages/QuotaPage.module.scss';\n",
        "import { QuotaCachedTime } from '@/extensions/quota/QuotaCardExtras';\n"
        "import styles from '@/pages/QuotaPage.module.scss';\n",
        "from '@/extensions/quota/QuotaCardExtras'",
    )
    replace_once(path, "  errorStatus?: number;\n}", "  errorStatus?: number;\n  cachedAt?: number;\n}")
    text = read(path)
    if '<QuotaCachedTime quotaStatus={quotaStatus} cachedAt={quota.cachedAt} />' in text:
        return

    render_variants = (
        'renderQuotaItems(quota, t, { styles, QuotaProgressBar })',
        'renderQuotaItems(quota, t, { styles, QuotaProgressBar: BoundQuotaProgressBar })',
    )
    matches = [variant for variant in render_variants if variant in text]
    if len(matches) != 1:
        raise RuntimeError(
            f'Expected one quota renderer variant in {path}, found {len(matches)}: {render_variants!r}'
        )
    render_call = matches[0]
    old = f"        ) : quota ? (\n          {render_call}\n        ) : ("
    new = (
        "        ) : quota ? (\n"
        "          <>\n"
        f"            {{{render_call}}}\n"
        "            <QuotaCachedTime quotaStatus={quotaStatus} cachedAt={quota.cachedAt} />\n"
        "          </>\n"
        "        ) : ("
    )
    match_count = text.count(old)
    if match_count != 1:
        raise RuntimeError(f'Expected one quota success branch in {path}, found {match_count}: {old[:120]!r}')
    write(path, text.replace(old, new, 1))


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
    path = auth_files_page_path(target)
    replace_once(
        path,
        "import { useAuthStore, useNotificationStore, useThemeStore } from '@/stores';\n",
        "import { useAuthStore, useNotificationStore, useThemeStore, useQuotaStore } from '@/stores';\n",
    )
    insert_once(
        path,
        "import { useAuthStore, useNotificationStore, useThemeStore, useQuotaStore } from '@/stores';\n",
        "import { resolveXaiPlanType } from '@/extensions/quota/xaiQuota';\n"
        "import { useAuthStore, useNotificationStore, useThemeStore, useQuotaStore } from '@/stores';\n",
        "resolveXaiPlanType",
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
        "  const storedPlanType = normalizeAuthFileSearchPlan(record.planType ?? record.plan_type);\n"
        "  const planType = storedPlanType || resolveXaiPlanType(monthlyLimitCents, monthlyLimitCents !== null);\n"
        "  if (!planType) return;\n"
        "  values.push(planType, planType.replace(/-/g, ' '));\n"
        "  if (planType === 'free') values.push('Free');\n"
        "  else if (planType === 'supergrok') values.push(t('xai_quota.plan_supergrok'), 'supergrok');\n"
        "  else if (planType === 'x-premium-plus') values.push(t('xai_quota.plan_x_premium_plus'), 'x premium+');\n"
        "  else if (planType === 'supergrok-heavy') values.push(t('xai_quota.plan_supergrok_heavy'), 'supergrok heavy');\n"
        "  else if (planType === 'paid-unknown') values.push(t('xai_quota.plan_paid_unknown'));\n"
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
    page_path = auth_files_page_path(target)
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
    text = read(page_path)
    direct_priority = (
        "        const pa = typeof a.priority === 'number' ? a.priority : 0;\n"
        "        const pb = typeof b.priority === 'number' ? b.priority : 0;\n"
    )
    parsed_priority = (
        "        const pa = parsePriorityValue(a.priority) ?? 0;\n"
        "        const pb = parsePriorityValue(b.priority) ?? 0;\n"
    )
    if direct_priority in text and parsed_priority not in text:
        write(page_path, text.replace(direct_priority, parsed_priority, 1))
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
        "        const pa = Number.isFinite(Number(a.priority)) ? Number(a.priority) : 0;\n"
        "        const pb = Number.isFinite(Number(b.priority)) ? Number(b.priority) : 0;\n"
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

    text = read(page_path)
    if 'sortMode={effectiveSortMode}' not in text and 'value={effectiveSortMode}' not in text:
        inline_select = (
            "                value={sortMode}\n"
            "                options={sortOptions}\n"
            "                onChange={handleSortModeChange}\n"
        )
        toolbar_prop = "          sortMode={sortMode}\n"
        if inline_select in text:
            write(
                page_path,
                text.replace(inline_select, inline_select.replace('sortMode', 'effectiveSortMode'), 1),
            )
        elif toolbar_prop in text:
            write(page_path, text.replace(toolbar_prop, "          sortMode={effectiveSortMode}\n", 1))
        else:
            raise RuntimeError(f'Pattern not found in {page_path}: auth files sort control')


def patch_auth_files_gemini_quota(target: Path) -> None:
    constants_path = target / 'src/features/authFiles/constants.ts'
    quota_section_path = target / 'src/features/authFiles/components/AuthFileQuotaSection.tsx'
    card_path = target / 'src/features/authFiles/components/AuthFileCard.tsx'
    styles_path = auth_files_styles_path(target)

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

    card_text = read(card_path)
    legacy_card_class = (
        "        : quotaType === 'codex'\n"
        "          ? styles.codexCard\n"
        "          : quotaType === 'kimi'"
    )
    if legacy_card_class in card_text:
        write(
            card_path,
            card_text.replace(
                legacy_card_class,
                "        : quotaType === 'codex'\n"
                "          ? styles.codexCard\n"
                "          : quotaType === 'gemini-cli'\n"
                "            ? styles.geminiCliCard\n"
                "            : quotaType === 'kimi'",
                1,
            ),
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
    elif "quotaType === 'gemini-cli'" in card_text:
        pass
    elif 'AuthFileQuotaSection' not in card_text:
        raise RuntimeError(f'Pattern not found in {card_path}: quota card layout')


def patch_auth_files_runtime_state(target: Path) -> None:
    type_path = target / 'src/types/authFile.ts'
    card_path = target / 'src/features/authFiles/components/AuthFileCard.tsx'
    page_path = auth_files_page_path(target)

    insert_once(
        type_path,
        "  success?: unknown;\n",
        "  selected?: unknown;\n  success?: unknown;\n",
        "selected?: unknown;",
    )
    card_text = read(card_path)
    legacy_stats = (
        "  const fileStats = {\n    success: normalizeUsageTotal(file.success),\n"
        "    failure: normalizeUsageTotal(file.failed),\n  };\n"
    )
    if legacy_stats in card_text:
        write(
            card_path,
            card_text.replace(
                legacy_stats,
                "  const fileStats = {\n    selected: normalizeUsageTotal(file.selected),\n"
                "    success: normalizeUsageTotal(file.success),\n"
                "    failure: normalizeUsageTotal(file.failed),\n  };\n",
                1,
            ),
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
    elif 'const selectedCount =' not in card_text and 'const successCount = file.successCount ?? 0;' in card_text:
        write(
            card_path,
            card_text.replace(
                '  const successCount = file.successCount ?? 0;\n',
                "  const selectedCount = Math.max(0, Number(file.selected) || 0);\n"
                "  const successCount = file.successCount ?? 0;\n",
                1,
            ),
        )
        insert_once(
            card_path,
            "          <span className={styles.healthCounts}>\n",
            "          <span className={styles.healthCounts}>\n"
            "            <span className={styles.countOk} title={t('auth_files.selected_count')}>\n"
            "              {t('auth_files.selected_count')} {selectedCount}\n"
            "            </span>\n",
            "{t('auth_files.selected_count')} {selectedCount}",
        )
    elif "t('auth_files.selected_count')" not in card_text:
        raise RuntimeError(f'Pattern not found in {card_path}: auth runtime counters')

    insert_once(
        page_path,
        "import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';\n",
        "import { useHeaderRefresh } from '@/hooks/useHeaderRefresh';\n"
        "import { quotaPersistenceMiddleware } from '@/extensions/quota/persistenceMiddleware';\n",
        "quotaPersistenceMiddleware } from '@/extensions/quota/persistenceMiddleware'",
    )
    text = read(page_path)
    if 'quotaPersistenceMiddleware.ensureFresh()' not in text:
        refresh_variants = (
            "    await Promise.all([loadFiles(), loadExcluded(), loadModelAlias()]);\n",
            "    await Promise.all([loadFiles({ background: true }), loadExcluded(), loadModelAlias()]);\n",
        )
        for refresh in refresh_variants:
            if refresh in text:
                replacement = refresh.replace(']);\n', ', quotaPersistenceMiddleware.ensureFresh()]);\n')
                write(page_path, text.replace(refresh, replacement, 1))
                break
        else:
            raise RuntimeError(f'Pattern not found in {page_path}: header refresh')


def patch_account_usage_feature(target: Path) -> None:
    icons_path = target / 'src/components/ui/icons.tsx'
    card_path = target / 'src/features/authFiles/components/AuthFileCard.tsx'
    page_path = auth_files_page_path(target)
    styles_path = auth_files_styles_path(target)

    insert_once(
        icons_path,
        'export function IconModelCluster({ size = 20, ...props }: IconProps) {\n',
        '''export function IconChartColumnIncreasing({ size = 20, ...props }: IconProps) {
  return (
    <svg {...baseSvgProps} width={size} height={size} {...props}>
      <path d="M3 3v18h18" />
      <path d="M7 16v1" />
      <path d="M11 12v5" />
      <path d="M15 8v9" />
      <path d="M19 4v13" />
    </svg>
  );
}

export function IconModelCluster({ size = 20, ...props }: IconProps) {
''',
        'export function IconChartColumnIncreasing',
    )

    replace_once(
        card_path,
        '  IconDownload,\n  IconInfo,\n',
        '  IconChartColumnIncreasing,\n  IconDownload,\n  IconInfo,\n',
    )
    replace_once(
        card_path,
        '  onShowModels: (file: AuthFileItem) => void;\n',
        '  onShowModels: (file: AuthFileItem) => void;\n  onShowUsage: (file: AuthFileItem) => void;\n',
    )
    replace_once(
        card_path,
        '    onShowModels,\n    onDownload,\n',
        '    onShowModels,\n    onShowUsage,\n    onDownload,\n',
    )
    card_text = read(card_path)
    legacy_usage_marker = '            </div>\n          </div>\n\n          <div className={`${styles.cardMeta}'
    if "onClick={() => onShowUsage(file)}" not in card_text and legacy_usage_marker in card_text:
        write(card_path, card_text.replace(legacy_usage_marker, '''            </div>
            {authIndexKey && (
              <Button
                variant="secondary"
                size="sm"
                onClick={() => onShowUsage(file)}
                className={styles.usageCornerButton}
                title={t('account_usage.card_action')}
                aria-label={t('account_usage.card_action')}
                disabled={disableControls}
              >
                <IconChartColumnIncreasing className={styles.actionIcon} size={17} />
              </Button>
            )}
          </div>

          <div className={`${styles.cardMeta}''',
        1))
        insert_once(
            styles_path,
            '.modelsActionButton:global(.btn.btn-sm) {\n',
            '''.usageCornerButton:global(.btn.btn-sm) {
  flex: 0 0 auto;
  align-self: flex-start;
  width: 34px;
  height: 34px;
  min-width: 34px;
  padding: 0;
  background: color-mix(in srgb, #0f766e 9%, var(--bg-secondary));
  border-color: color-mix(in srgb, #0f766e 22%, var(--border-color));
  color: color-mix(in srgb, #0f766e 78%, var(--text-primary));
}

.usageCornerButton:global(.btn.btn-sm):hover {
  background: color-mix(in srgb, #0f766e 14%, var(--bg-secondary));
  border-color: color-mix(in srgb, #0f766e 38%, var(--border-color));
}

.usageCornerButton:global(.btn.btn-sm) > span {
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.fileCardCompact .usageCornerButton:global(.btn.btn-sm) {
  width: 30px;
  height: 30px;
  min-width: 30px;
}

.modelsActionButton:global(.btn.btn-sm) {
''',
            '.usageCornerButton:global(.btn.btn-sm)',
        )
    elif "onClick={() => onShowUsage(file)}" not in card_text:
        actions_marker = '        <div className={styles.actionsMain}>\n'
        if actions_marker not in card_text:
            raise RuntimeError(f'Pattern not found in {card_path}: auth file actions')
        write(
            card_path,
            card_text.replace(
                actions_marker,
                actions_marker
                + "          {authIndexKey && (\n"
                + "            <Button variant=\"secondary\" size=\"sm\" onClick={() => onShowUsage(file)}\n"
                + "              title={t('account_usage.card_action')} disabled={disableControls}>\n"
                + "              <IconChartColumnIncreasing size={14} />\n"
                + "              {t('account_usage.card_action')}\n"
                + "            </Button>\n"
                + "          )}\n",
                1,
            ),
        )

    insert_once(
        page_path,
        "import { AuthFileModelsModal } from '@/features/authFiles/components/AuthFileModelsModal';\n",
        "import { AuthFileModelsModal } from '@/features/authFiles/components/AuthFileModelsModal';\n"
        "import { AccountUsageModal } from '@/features/monitoring/components/AccountUsageModal';\n",
        "AccountUsageModal } from '@/features/monitoring/components/AccountUsageModal'",
    )
    insert_once(
        page_path,
        "import { useAuthStore, useNotificationStore, useThemeStore, useQuotaStore } from '@/stores';\n",
        "import { useAuthStore, useNotificationStore, useThemeStore, useQuotaStore } from '@/stores';\n"
        "import type { AuthFileItem } from '@/types';\n",
        "import type { AuthFileItem } from '@/types';",
    )
    page_text = read(page_path)
    if 'const [accountUsageFile, setAccountUsageFile]' not in page_text:
        state_markers = (
            "  const [displaySettingsOpen, setDisplaySettingsOpen] = useState(false);\n",
            "  const [sortMode, setSortMode] = useState<AuthFilesSortMode>('default');\n",
        )
        for marker in state_markers:
            if marker in page_text:
                write(
                    page_path,
                    page_text.replace(
                        marker,
                        marker
                        + "  const [accountUsageFile, setAccountUsageFile] = useState<AuthFileItem | null>(null);\n",
                        1,
                    ),
                )
                break
        else:
            raise RuntimeError(f'Pattern not found in {page_path}: account usage state')
    page_text = read(page_path)
    if 'onShowUsage={setAccountUsageFile}' not in page_text:
        for indent in ('                  ', '                '):
            marker = f'{indent}onShowModels={{showModels}}\n{indent}onDownload={{handleDownload}}\n'
            if marker in page_text:
                write(
                    page_path,
                    page_text.replace(
                        marker,
                        f'{indent}onShowModels={{showModels}}\n'
                        f'{indent}onShowUsage={{setAccountUsageFile}}\n'
                        f'{indent}onDownload={{handleDownload}}\n',
                        1,
                    ),
                )
                break
        else:
            raise RuntimeError(f'Pattern not found in {page_path}: auth card usage callback')
    insert_once(
        page_path,
        "      <AuthFileModelsModal\n",
        "      <AccountUsageModal file={accountUsageFile} onClose={() => setAccountUsageFile(null)} />\n\n"
        "      <AuthFileModelsModal\n",
        '<AccountUsageModal file={accountUsageFile}',
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


def patch_management_update_check(target: Path) -> None:
    version_path = target / 'src/services/api/version.ts'
    insert_once(
        version_path,
        "  checkLatest: () => apiClient.get<Record<string, unknown>>('/latest-version'),\n",
        "  checkLatest: () => apiClient.get<Record<string, unknown>>('/latest-version'),\n"
        "  checkManagementPanelUpdate: () =>\n"
        "    apiClient.post<{ status: string; updated: boolean; sha256: string }>(\n"
        "      '/management-panel/check-update'\n"
        "    ),\n",
        'checkManagementPanelUpdate:',
    )

    page_path = target / 'src/pages/SystemPage.tsx'
    insert_once(
        page_path,
        "  const [checkingVersion, setCheckingVersion] = useState(false);\n",
        "  const [checkingVersion, setCheckingVersion] = useState(false);\n"
        "  const [checkingManagementUpdate, setCheckingManagementUpdate] = useState(false);\n",
        'const [checkingManagementUpdate, setCheckingManagementUpdate]',
    )
    insert_once(
        page_path,
        "  useEffect(() => {\n    fetchConfig().catch(() => {\n",
        "  const handleManagementUpdateCheck = useCallback(async () => {\n"
        "    setCheckingManagementUpdate(true);\n"
        "    try {\n"
        "      const result = await versionApi.checkManagementPanelUpdate();\n"
        "      if (result.updated) {\n"
        "        showNotification(t('system_info.management_check_update_updated'), 'success');\n"
        "        window.setTimeout(() => {\n"
        "          const nextUrl = new URL(window.location.href);\n"
        "          nextUrl.searchParams.set('_management_updated', Date.now().toString());\n"
        "          window.location.replace(nextUrl.toString());\n"
        "        }, 500);\n"
        "      } else {\n"
        "        showNotification(t('system_info.management_check_update_unchanged'), 'success');\n"
        "      }\n"
        "    } catch (error: unknown) {\n"
        "      const message =\n"
        "        error instanceof Error ? error.message : typeof error === 'string' ? error : '';\n"
        "      showNotification(\n"
        "        `${t('system_info.management_check_update_error')}${message ? `: ${message}` : ''}`,\n"
        "        'error'\n"
        "      );\n"
        "    } finally {\n"
        "      setCheckingManagementUpdate(false);\n"
        "    }\n"
        "  }, [showNotification, t]);\n\n"
        "  useEffect(() => {\n    fetchConfig().catch(() => {\n",
        'const handleManagementUpdateCheck = useCallback',
    )
    replace_once(
        page_path,
        "            <button\n"
        "              type=\"button\"\n"
        "              className={`${styles.infoTile} ${styles.tapTile}`}\n"
        "              onClick={handleInfoVersionTap}\n"
        "            >\n"
        "              <div className={styles.tileHeader}>\n"
        "                <div className={styles.tileLabel}>{t('footer.version')}</div>\n"
        "              </div>\n"
        "              <div className={styles.tileValue}>{appVersion}</div>\n"
        "            </button>\n",
        "            <div\n"
        "              className={`${styles.infoTile} ${styles.tapTile}`}\n"
        "              onClick={handleInfoVersionTap}\n"
        "            >\n"
        "              <div className={styles.tileHeader}>\n"
        "                <div className={styles.tileLabel}>{t('footer.version')}</div>\n"
        "                <Button\n"
        "                  type=\"button\"\n"
        "                  variant=\"ghost\"\n"
        "                  size=\"sm\"\n"
        "                  className={styles.tileAction}\n"
        "                  onClick={(event) => {\n"
        "                    event.stopPropagation();\n"
        "                    void handleManagementUpdateCheck();\n"
        "                  }}\n"
        "                  loading={checkingManagementUpdate}\n"
        "                  title={t('system_info.management_check_update_button')}\n"
        "                  aria-label={t('system_info.management_check_update_button')}\n"
        "                >\n"
        "                  {t('system_info.management_check_update_button')}\n"
        "                </Button>\n"
        "              </div>\n"
        "              <div className={styles.tileValue}>{appVersion}</div>\n"
        "            </div>\n",
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
    auth_files_normalizer = (
        'normalizeAuthFilesResponse'
        if 'normalizeAuthFilesResponse' in read(auth_files_path)
        else 'dedupeAuthFilesResponse'
    )
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
    replace_once_if_present(
        auth_files_path,
        '      .then(dedupeAuthFilesResponse)\n',
        f'      .then({auth_files_normalizer})\n',
    )
    text = read(auth_files_path)
    list_variants = (
        "  list: async () => dedupeAuthFilesResponse(await apiClient.get<AuthFilesResponse>('/auth-files')),\n\n"
        "  setStatus: (name: string, disabled: boolean) =>\n"
        "    apiClient.patch<AuthFileStatusResponse>('/auth-files/status', { name, disabled }),\n\n",
        "  list: async () =>\n"
        "    normalizeAuthFilesResponse(await apiClient.get<AuthFilesResponse>('/auth-files')),\n\n"
        "  setStatus: (name: string, disabled: boolean) =>\n"
        "    apiClient.patch<AuthFileStatusResponse>('/auth-files/status', { name, disabled }),\n\n",
    )
    list_replacement = (
        "  list: fetchAuthFilesList,\n\n  patchFile: async (payload: AuthFilePatchPayload) => {\n    const response = await apiClient.patch<AuthFileStatusResponse>('/auth-files', payload);\n    invalidateAuthFilesListCache();\n    return response;\n  },\n\n  setStatus: async (name: string, disabled: boolean) => {\n    const response = await apiClient.patch<AuthFileStatusResponse>('/auth-files/status', { name, disabled });\n    invalidateAuthFilesListCache();\n    return response;\n  },\n"
    )
    if '  list: fetchAuthFilesList,\n' not in text:
        for list_variant in list_variants:
            if list_variant in text:
                write(auth_files_path, text.replace(list_variant, list_replacement, 1))
                break
        else:
            raise RuntimeError(f'Pattern not found in {auth_files_path}: auth files list method')
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


def patch_locales(target: Path) -> None:
    monitoring = json.loads(LOCALES_FILE.read_text())
    locales_dir = target / 'src/i18n/locales'
    for locale_path in sorted(locales_dir.glob('*.json')):
        data = json.loads(read(locale_path))
        additions = monitoring.get(locale_path.name, {})
        data.setdefault('nav', {}).update(additions.get('nav', {}))
        proxy_pool_nav = PROXY_POOL_NAV_LOCALE_KEYS.get(
            locale_path.name,
            PROXY_POOL_NAV_LOCALE_KEYS['en.json'],
        )
        data.setdefault('nav', {})['proxy_pool'] = proxy_pool_nav['label']
        oauth_model_policy_nav = OAUTH_MODEL_POLICY_NAV_LOCALE_KEYS.get(
            locale_path.name,
            OAUTH_MODEL_POLICY_NAV_LOCALE_KEYS['en.json'],
        )
        data.setdefault('nav', {})['oauth_model_policy'] = oauth_model_policy_nav['label']
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
        data.setdefault('nav_meta', {})['proxy_pool'] = proxy_pool_nav['meta']
        data.setdefault('nav_meta', {})['oauth_model_policy'] = oauth_model_policy_nav['meta']
        data['monitoring'] = additions.get('monitoring', data.get('monitoring', {}))
        data['account_usage'] = additions.get('account_usage', data.get('account_usage', {}))
        data['usage_stats'] = additions.get('usage_stats', data.get('usage_stats', {}))
        data['routing_policy'] = additions.get('routing_policy', data.get('routing_policy', {}))
        data['proxy_pool'] = additions.get(
            'proxy_pool',
            monitoring.get('en.json', {}).get('proxy_pool', data.get('proxy_pool', {})),
        )
        data['oauth_model_policy'] = additions.get(
            'oauth_model_policy',
            monitoring.get('en.json', {}).get(
                'oauth_model_policy',
                data.get('oauth_model_policy', {}),
            ),
        )
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
        data.setdefault('xai_quota', {}).update(
            XAI_QUOTA_LOCALE_KEYS.get(locale_path.name, XAI_QUOTA_LOCALE_KEYS['en.json'])
        )
        data.setdefault('system_info', {}).update(
            MANAGEMENT_UPDATE_LOCALE_KEYS.get(
                locale_path.name,
                MANAGEMENT_UPDATE_LOCALE_KEYS['en.json'],
            )
        )
        write(locale_path, json.dumps(data, ensure_ascii=False, indent=2) + '\n')


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
    patch_account_usage_feature(target)
    patch_runtime_detection(target)
    patch_management_update_check(target)
    patch_api_client_connection_isolation(target)
    patch_supporting_api_and_types(target)
    patch_locales(target)
    flush_writes()
    print(f'OK: CPA-Management customization applied to {target}')


if __name__ == '__main__':
    main()
