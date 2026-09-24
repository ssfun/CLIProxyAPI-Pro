#!/bin/sh

# ==========================================
# 辅助函数：统一日志输出
# ==========================================
log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') [$1] [$2] $3"
}

# ==========================================
# 环境变量配置
# ==========================================
# komari agent 变量
KOMARI_SERVER="${KOMARI_SERVER:-}"
KOMARI_SECRET="${KOMARI_SECRET:-}"

# Pro 数据备份恢复变量；保留旧变量作为兼容别名。
BACKUP_WEBDAV_URL="${CLIPROXY_BACKUP_WEBDAV_URL:-${WEBDAV_URL:-}}"
BACKUP_WEBDAV_USERNAME="${CLIPROXY_BACKUP_WEBDAV_USERNAME:-${WEBDAV_USERNAME:-}}"
BACKUP_WEBDAV_PASSWORD="${CLIPROXY_BACKUP_WEBDAV_PASSWORD:-${WEBDAV_PASSWORD:-}}"
MANAGEMENT_PASSWORD="${MANAGEMENT_PASSWORD:-}"

KOMARI_PID=""
MAIN_PID=""

stop_child() {
    child_pid="$1"
    signal_name="$2"
    if [ -n "$child_pid" ] && kill -0 "$child_pid" 2>/dev/null; then
        kill -s "$signal_name" "$child_pid" 2>/dev/null || true
    fi
}

# Invoked indirectly by the TERM/INT traps below; ShellCheck cannot trace trap strings.
# shellcheck disable=SC2317,SC2329
shutdown() {
    signal_name="$1"
    trap - TERM INT
    log "Entrypoint" "INFO" "Forwarding $signal_name and waiting for child processes..."
    stop_child "$MAIN_PID" "$signal_name"
    stop_child "$KOMARI_PID" "$signal_name"

    main_status=0
    if [ -n "$MAIN_PID" ]; then
        wait "$MAIN_PID" || main_status=$?
    fi
    if [ -n "$KOMARI_PID" ]; then
        wait "$KOMARI_PID" 2>/dev/null || true
    fi
    exit "$main_status"
}

trap 'shutdown TERM' TERM
trap 'shutdown INT' INT

# ==========================================
# 1. 启动 komari-agent
# ==========================================
if [ -n "$KOMARI_SERVER" ] && [ -n "$KOMARI_SECRET" ]; then
    log "Komari" "INFO" "Starting agent..."
    /CLIProxyAPI/komari-agent -e "$KOMARI_SERVER" -t "$KOMARI_SECRET" --disable-auto-update >/dev/null 2>&1 &
    KOMARI_PID=$!
else
    log "Komari" "WARN" "Skipped."
fi

# ==========================================
# 2. 启动主应用
# ==========================================
log "MainApp" "INFO" "Starting CLIProxyAPI..."
/CLIProxyAPI/CLIProxyAPI &
MAIN_PID=$!

# ==========================================
# 3. 从 WebDAV 恢复完整 Pro 数据
# ==========================================
if [ -n "$BACKUP_WEBDAV_URL" ] && [ -n "$BACKUP_WEBDAV_USERNAME" ] && [ -n "$BACKUP_WEBDAV_PASSWORD" ] && [ -n "$MANAGEMENT_PASSWORD" ]; then
    # 等待主应用就绪
    log "UsageRestore" "INFO" "Waiting for main app to be ready..."
    RETRIES=0
    while [ $RETRIES -lt 30 ]; do
        if curl -sf -H "Authorization: Bearer $MANAGEMENT_PASSWORD" \
            http://127.0.0.1:8317/v0/management/data/overview > /dev/null 2>&1; then
            log "UsageRestore" "INFO" "Main app is ready."
            break
        fi
        RETRIES=$((RETRIES + 1))
        sleep 1
    done

    if [ $RETRIES -lt 30 ]; then
        # 优先恢复新数据管理备份；仅在没有新备份时回退旧 usage-export 文件。
        WEBDAV_LISTING=$(curl -s -X PROPFIND \
            -u "$BACKUP_WEBDAV_USERNAME:$BACKUP_WEBDAV_PASSWORD" \
            "$BACKUP_WEBDAV_URL/" \
            -H "Depth: 1")
        LATEST_FILE=$(printf '%s' "$WEBDAV_LISTING" | grep -oE 'cliproxy-pro-backup-[0-9_]+\.jsonl' | sort | tail -n 1)
        if [ -z "$LATEST_FILE" ]; then
            LATEST_FILE=$(printf '%s' "$WEBDAV_LISTING" | grep -oE 'usage-export-[0-9_]+\.(jsonl|json)' | sort | tail -n 1)
        fi

        if [ -n "$LATEST_FILE" ]; then
            log "UsageRestore" "INFO" "Downloading $LATEST_FILE from WebDAV..."
            curl -sf -u "$BACKUP_WEBDAV_USERNAME:$BACKUP_WEBDAV_PASSWORD" \
                "$BACKUP_WEBDAV_URL/$LATEST_FILE" -o /tmp/cliproxy-pro-restore.jsonl

            if [ -f /tmp/cliproxy-pro-restore.jsonl ]; then
                RESTORE_URL="http://127.0.0.1:8317/v0/management/data/backups/restore"
                if ! awk 'NF { print; exit }' /tmp/cliproxy-pro-restore.jsonl | \
                    grep -Eq '"record_type"[[:space:]]*:[[:space:]]*"backup_manifest"'; then
                    log "UsageRestore" "WARN" "Importing manifest-free legacy backup without integrity verification during the compatibility transition."
                    RESTORE_URL="${RESTORE_URL}?allow_legacy=1"
                fi
                log "UsageRestore" "INFO" "Restoring Pro data through the data-management pipeline..."
                HTTP_STATUS=$(curl -sS -o /tmp/cliproxy-pro-restore-response.json -w "%{http_code}" -X POST \
                    -H "Content-Type: application/octet-stream" \
                    -H "Authorization: Bearer $MANAGEMENT_PASSWORD" \
                    --data-binary @/tmp/cliproxy-pro-restore.jsonl \
                    "$RESTORE_URL" || true)
                HTTP_STATUS="${HTTP_STATUS:-000}"
                RESTORE_RESULT=$(cat /tmp/cliproxy-pro-restore-response.json 2>/dev/null || true)
                if [ "$HTTP_STATUS" -ge 200 ] && [ "$HTTP_STATUS" -lt 300 ]; then
                    log "UsageRestore" "INFO" "Pro data restore succeeded: $RESTORE_RESULT"
                else
                    log "UsageRestore" "WARN" "Pro data restore failed with status $HTTP_STATUS: $RESTORE_RESULT"
                fi
                rm -f /tmp/cliproxy-pro-restore.jsonl /tmp/cliproxy-pro-restore-response.json
            else
                log "UsageRestore" "WARN" "Download failed."
            fi
        else
            log "UsageRestore" "INFO" "No backup found on WebDAV, skipping."
        fi
    else
        log "UsageRestore" "WARN" "Main app not ready after 30s, skipping restore."
    fi
else
    log "UsageRestore" "WARN" "WebDAV config incomplete, skipping restore."
fi

# 等待主进程
MAIN_STATUS=0
wait "$MAIN_PID" || MAIN_STATUS=$?
stop_child "$KOMARI_PID" TERM
if [ -n "$KOMARI_PID" ]; then
    wait "$KOMARI_PID" 2>/dev/null || true
fi
exit "$MAIN_STATUS"
