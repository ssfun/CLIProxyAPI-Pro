#!/bin/sh

# ==========================================
# 辅助函数：统一日志输出
# ==========================================
log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') [$1] [$2] $3"
}

is_truthy() {
    case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
        1|true|yes|on) return 0 ;;
        *) return 1 ;;
    esac
}

# ==========================================
# 环境变量配置
# ==========================================
# komari agent 变量
KOMARI_SERVER="${KOMARI_SERVER:-}"
KOMARI_SECRET="${KOMARI_SECRET:-}"

# WebDAV 恢复变量
WEBDAV_URL="${WEBDAV_URL:-}"
WEBDAV_USERNAME="${WEBDAV_USERNAME:-}"
WEBDAV_PASSWORD="${WEBDAV_PASSWORD:-}"
MANAGEMENT_PASSWORD="${MANAGEMENT_PASSWORD:-}"
USAGE_ALLOW_LEGACY_RESTORE="${USAGE_ALLOW_LEGACY_RESTORE:-false}"

# ==========================================
# 1. 启动 komari-agent
# ==========================================
if [ -n "$KOMARI_SERVER" ] && [ -n "$KOMARI_SECRET" ]; then
    log "Komari" "INFO" "Starting agent..."
    /CLIProxyAPI/komari-agent -e "$KOMARI_SERVER" -t "$KOMARI_SECRET" --disable-auto-update >/dev/null 2>&1 &
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
# 3. 从 WebDAV 恢复 usage 统计
# ==========================================
if [ -n "$WEBDAV_URL" ] && [ -n "$WEBDAV_USERNAME" ] && [ -n "$WEBDAV_PASSWORD" ] && [ -n "$MANAGEMENT_PASSWORD" ]; then
    # 等待主应用就绪
    log "UsageRestore" "INFO" "Waiting for main app to be ready..."
    RETRIES=0
    while [ $RETRIES -lt 30 ]; do
        if curl -sf -H "Authorization: Bearer $MANAGEMENT_PASSWORD" \
            http://127.0.0.1:8317/v0/management/usage > /dev/null 2>&1; then
            log "UsageRestore" "INFO" "Main app is ready."
            break
        fi
        RETRIES=$((RETRIES + 1))
        sleep 1
    done

    if [ $RETRIES -lt 30 ]; then
        # 获取 WebDAV 中最新的备份文件名
        LATEST_FILE=$(curl -s -X PROPFIND \
            -u "$WEBDAV_USERNAME:$WEBDAV_PASSWORD" \
            "$WEBDAV_URL/" \
            -H "Depth: 1" | grep -oE 'usage-export-[0-9_]+\.(jsonl|json)' | sort | tail -n 1)

        if [ -n "$LATEST_FILE" ]; then
            log "UsageRestore" "INFO" "Downloading $LATEST_FILE from WebDAV..."
            curl -sf -u "$WEBDAV_USERNAME:$WEBDAV_PASSWORD" \
                "$WEBDAV_URL/$LATEST_FILE" -o /tmp/usage-restore.jsonl

            if [ -f /tmp/usage-restore.jsonl ]; then
                IMPORT_URL="http://127.0.0.1:8317/v0/management/usage/import"
                if ! awk 'NF { print; exit }' /tmp/usage-restore.jsonl | \
                    grep -Eq '"record_type"[[:space:]]*:[[:space:]]*"backup_manifest"'; then
                    if is_truthy "$USAGE_ALLOW_LEGACY_RESTORE"; then
                        IMPORT_URL="$IMPORT_URL?allow_legacy=1"
                        log "UsageRestore" "WARN" "Importing manifest-free legacy backup because USAGE_ALLOW_LEGACY_RESTORE is enabled."
                    else
                        IMPORT_URL=""
                        log "UsageRestore" "WARN" "Backup has no integrity manifest; skipping automatic restore. Set USAGE_ALLOW_LEGACY_RESTORE=true only for a trusted legacy backup."
                    fi
                fi
                if [ -n "$IMPORT_URL" ]; then
                    log "UsageRestore" "INFO" "Importing usage data..."
                    RESULT=$(curl -s -X POST \
                        -H "Content-Type: application/x-ndjson" \
                        -H "Authorization: Bearer $MANAGEMENT_PASSWORD" \
                        --data-binary @/tmp/usage-restore.jsonl \
                        "$IMPORT_URL")
                    log "UsageRestore" "INFO" "Import result: $RESULT"
                fi
                rm -f /tmp/usage-restore.jsonl
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
wait $MAIN_PID
