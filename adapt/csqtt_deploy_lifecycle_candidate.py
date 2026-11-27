"""Pinned, separate lifecycle candidate; original CSQTTR 2.5.3 update semantics."""
import hashlib
from csqtt_deploy_candidate import candidate_bytes

# Filled after reproducible generation; no production asset is overwritten.
LIFECYCLE_SHA = '3ef1b3977d289e4bd878fe9ad87395abf30345c9b54c784bf69876ed33c43529'

def lifecycle_bytes(baseline: bytes, verify=True) -> bytes:
    s = candidate_bytes(baseline).decode()
    def replace(old, new):
        nonlocal s
        if s.count(old) != 1:
            raise ValueError("lifecycle source anchor drift")
        s = s.replace(old, new, 1)

    # Private SFTP workspace: never consume another operation's fixed /tmp uploads.
    replace('readonly UPLOAD_BINARY="/tmp/.csqtt-core-upload-server"',
            'readonly UPLOAD_BINARY="${CSQTT_DEPLOY_WORKSPACE:?}/csqtt-core8-linux-arm64"')
    replace('readonly UPLOAD_ENV_FILE="/tmp/.csqtt-core-upload-web.env"',
            'readonly UPLOAD_ENV_FILE="${CSQTT_DEPLOY_WORKSPACE:?}/csqtt-core.env"')
    replace('readonly UPLOAD_OVERRIDES_FILE="/tmp/.csqtt-core-upload-overrides.json"',
            'readonly UPLOAD_OVERRIDES_FILE="${CSQTT_DEPLOY_WORKSPACE:?}/deploy-overrides.json"')
    replace('SYSTEMD_NEEDS_RELOAD=0\nDEPLOY_PHASE=', 'PRESERVE_CONFIG=0\nSYSTEMD_NEEDS_RELOAD=0\nDEPLOY_PHASE=')
    replace('    if [ "$action" = install ] ||', '    if [ "$action" = update ] || [ "$action" = install ] ||')
    replace('    case "$action" in\n        status|', '    case "$action" in update) PRESERVE_CONFIG=1 ;; esac\n\n    case "$action" in\n        status|')
    replace('        install|--install|-i)\n', '        install|--install|-i|update)\n')
    # Direct adaptation of upstream prepare_uploaded_release, with Core paths and
    # stricter existing path/SQLite checks before any service is stopped.
    replace('    DEPLOY_PHASE="preflight"\n    [ -f "$UPLOAD_BINARY" ]',
'''    DEPLOY_PHASE="preflight"
    if [ "$PRESERVE_CONFIG" = 1 ]; then
        [ -d "$CSQTT_CONFIG_DIR" ] && [ ! -L "$CSQTT_CONFIG_DIR" ] ||
            die "Unsafe existing configuration directory" "$EXIT_PREFLIGHT_FAILED"
        [ -s "$CSQTT_ENV_FILE" ] && [ ! -L "$CSQTT_ENV_FILE" ] ||
            die "Existing WEB configuration is unavailable" "$EXIT_PREFLIGHT_FAILED"
        [ -s "$CSQTT_CONFIG_DIR/$CSQTT_DATABASE_FILE" ] &&
            [ ! -L "$CSQTT_CONFIG_DIR/$CSQTT_DATABASE_FILE" ] ||
            die "Existing SQLite database is unavailable" "$EXIT_PREFLIGHT_FAILED"
        # The manager independently checks SQLite integrity before execution.
        # Copy only the existing server configuration, never update-form secrets.
        [ ! -e "$UPLOAD_ENV_FILE" ] && [ ! -L "$UPLOAD_ENV_FILE" ] &&
            [ ! -e "$UPLOAD_OVERRIDES_FILE" ] && [ ! -L "$UPLOAD_OVERRIDES_FILE" ] ||
            die "Unexpected update uploads" "$EXIT_PREFLIGHT_FAILED"
        install -m 0600 "$CSQTT_ENV_FILE" "$UPLOAD_ENV_FILE"
        if [ -s "$CSQTT_DEPLOY_OVERRIDES_FILE" ]; then
            [ ! -L "$CSQTT_DEPLOY_OVERRIDES_FILE" ] ||
                die "Unsafe deploy configuration" "$EXIT_PREFLIGHT_FAILED"
            install -m 0600 "$CSQTT_DEPLOY_OVERRIDES_FILE" "$UPLOAD_OVERRIDES_FILE"
        else
            ( umask 077; set -C; printf '{"main_password":""}\\n' > "$UPLOAD_OVERRIDES_FILE" )
        fi
        log_info "Обновление: существующие пароли, WEB-конфигурация и SQLite сохраняются"
    fi
    [ -f "$UPLOAD_BINARY" ]''')
    # Preserve all existing settings, including unknown future config files.
    replace('    clear_runtime_config_preserving_database\n',
            '    if [ "$PRESERVE_CONFIG" != 1 ]; then clear_runtime_config_preserving_database; fi\n')
    replace('    if ! grep -Eq \'^[[:space:]]*(export[[:space:]]+)?CSQTT_PUBLIC_IP=.+$\' "$CSQTT_ENV_FILE"; then',
            '    if [ "$PRESERVE_CONFIG" != 1 ] && ! grep -Eq \'^[[:space:]]*(export[[:space:]]+)?CSQTT_PUBLIC_IP=.+$\' "$CSQTT_ENV_FILE"; then')
    # Inventory remains usable without a workspace; mutation entry points require it.
    s = s.replace('${CSQTT_DEPLOY_WORKSPACE:?}', '${CSQTT_DEPLOY_WORKSPACE:-/nonexistent/csqtt-core-no-upload}')
    # Validate private workspace and serialise all lifecycle operations before
    # opening the installer log, package installation or resource changes.
    replace('    mkdir -p "$(dirname "$LOG_FILE")"\n',
'''    case "$action" in
        install|update|uninstall|--install|-i|--uninstall|-u)
            [[ "${CSQTT_DEPLOY_WORKSPACE:-}" =~ ^/tmp/csqtt-deploy-[a-f0-9]{32}$ ]] ||
                { echo "Invalid private deployment workspace" >&2; exit 20; }
            [ -d "$CSQTT_DEPLOY_WORKSPACE" ] && [ ! -L "$CSQTT_DEPLOY_WORKSPACE" ] &&
                [ "$(stat -c '%u:%a' "$CSQTT_DEPLOY_WORKSPACE")" = 0:700 ] ||
                { echo "Unsafe private deployment workspace" >&2; exit 20; }
            # /run/lock is root-owned; refuse a pre-existing symlink.
            [ ! -L /run/lock/csqtt-core-deploy.lock ] || exit 20
            exec 9>/run/lock/csqtt-core-deploy.lock
            flock -n 9 || { echo "CSQTT Core deployment already running" >&2; exit 20; }
            ;;
    esac
    mkdir -p "$(dirname "$LOG_FILE")"
''')
    result = s.encode()
    if verify and hashlib.sha256(result).hexdigest() != LIFECYCLE_SHA:
        raise ValueError("unexpected lifecycle installer digest")
    return result
