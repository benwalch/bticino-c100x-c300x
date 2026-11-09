#!/bin/bash
#
# BTicino Bridge - SINGLE Deploy (Build + Deploy)
# Unifies web + go build + deploy into a single command
#
# Usage: ./scripts/deploy.sh [options]
#
# Options:
#   --skip-web     Don't build web (use previous build)
#   --skip-go      Don't build Go (use previous build)
#   --skip-config  Don't deploy config.yaml (keep remote config)
#   --skip-docs    Don't deploy API docs (Swagger UI)
#   --config FILE  Config file to deploy (default: configs/config.yaml)
#   --dry-run      Only show what would be done
#   --verbose      Detailed output
#

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Variables
SKIP_WEB=false
SKIP_GO=false
SKIP_CONFIG=false
SKIP_DOCS=false
DRY_RUN=false
VERBOSE=false
REMOTE="bticino"
REMOTE_DIR="/home/bticino/cfg/extra"
CONFIG_FILE="configs/config.yaml"
KEEP_BACKUPS=2

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --skip-web)
            SKIP_WEB=true
            shift
            ;;
        --skip-go)
            SKIP_GO=true
            shift
            ;;
        --skip-config)
            SKIP_CONFIG=true
            shift
            ;;
        --skip-docs)
            SKIP_DOCS=true
            shift
            ;;
        --config)
            CONFIG_FILE="$2"
            shift 2
            ;;
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        --verbose)
            VERBOSE=true
            shift
            ;;
        *)
            echo -e "${RED}Unknown option: $1${NC}"
            exit 1
            ;;
    esac
done

# Logging
log() {
    echo -e "${BLUE}[$(date +'%H:%M:%S')]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[$(date +'%H:%M:%S')] ✅ ${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[$(date +'%H:%M:%S')] ⚠️  ${NC} $1"
}

log_error() {
    echo -e "${RED}[$(date +'%H:%M:%S')] ❌ ${NC} $1"
}

# Main
echo -e "${BLUE}"
echo "╔═══════════════════════════════════════════════════════════╗"
echo "║        BTicino Bridge - SINGLE Deploy v0.14.2            ║"
echo "╚═══════════════════════════════════════════════════════════╝"
echo -e "${NC}"

if [ "$DRY_RUN" = true ]; then
    echo -e "${YELLOW}=== DRY RUN MODE ===${NC}"
    echo "The following steps would run:"
    [ "$SKIP_WEB" = false ] && echo "  - Build web frontend"
    [ "$SKIP_GO" = false ] && echo "  - Build Go binary"
    echo "  - Deploy to device"
    [ "$SKIP_CONFIG" = false ] && echo "  - Deploy config: $CONFIG_FILE -> $REMOTE_DIR/config.yaml"
    [ "$SKIP_DOCS" = false ] && echo "  - Deploy API docs: docs/ -> $REMOTE_DIR/docs/"
    exit 0
fi

# Step 1: Build Web (if web/ exists and not skipped)
if [ "$SKIP_WEB" = false ] && [ -d "web" ]; then
    log "[1/3] Building web frontend..."
    if [ -f "web/package.json" ]; then
        ./scripts/build_web.sh
        log_success "Web frontend built (web/dist/)"
    else
        log_warning "web/package.json not found, skipping web build"
        SKIP_WEB=true
    fi
else
    if [ "$SKIP_WEB" = false ]; then
        log_warning "web/ directory not found, skipping web build"
    fi
fi

# Step 2: Build Go
if [ "$SKIP_GO" = false ]; then
    log "[2/3] Building Go binary..."
    # Stripped: a smaller binary means fewer flash writes. Stack traces keep their lines.
    if GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags='-s -w' -o bticino_bridge ./cmd/main.go; then
        log_success "Go binary built (bticino_bridge)"
    else
        log_error "Failed to build Go binary"
        exit 1
    fi
fi

# Step 3: Deploy
log "[3/3] Deploying to device..."

# Check SSH
if ! ssh -o BatchMode=yes -o ConnectTimeout=5 "$REMOTE" "echo OK" > /dev/null 2>&1; then
    log_error "SSH connection failed. Check SSH keys."
    exit 1
fi
log_success "SSH OK"

# The flash is mounted sync with data=journal, so every write counts twice:
# only what changed is written, and backups are hard links, which copy no data.
REMOTE_SUMS=$(mktemp)
trap 'rm -f "$REMOTE_SUMS"' EXIT
ssh "$REMOTE" "cd $REMOTE_DIR && md5sum bticino_bridge VERSION config.yaml web/dist/index.html web/dist/assets/* docs/* 2>/dev/null" > "$REMOTE_SUMS" || true

local_md5() {
    if command -v md5sum > /dev/null; then
        md5sum < "$1" | cut -d' ' -f1
    else
        md5 -q "$1"
    fi
}

is_unchanged() { # local_file remote_path
    grep -qFx "$(local_md5 "$1")  $2" "$REMOTE_SUMS"
}

# Decodes into RAM first, so a broken transfer never reaches the flash, then writes
# <remote_path>.new in large blocks.
send_new() { # local_file remote_path
    local md5
    md5=$(local_md5 "$1")
    base64 < "$1" | ssh "$REMOTE" "cd $REMOTE_DIR || exit 1
        stage=/tmp/bticino_deploy.stage
        base64 -d > \$stage
        rc=1
        if [ \"\$(md5sum < \$stage | cut -d' ' -f1)\" = '$md5' ] &&
           dd if=\$stage of='$2.new' bs=1M 2> /dev/null &&
           [ \"\$(md5sum < '$2.new' | cut -d' ' -f1)\" = '$md5' ]; then
            rc=0
        else
            rm -f '$2.new'
        fi
        rm -f \$stage
        exit \$rc"
}

install_file() { # local_file remote_path
    is_unchanged "$1" "$2" && return 0
    if ! send_new "$1" "$2" || ! ssh "$REMOTE" "cd $REMOTE_DIR && mv '$2.new' '$2'"; then
        log_error "Transfer of $1 failed, device file unchanged"
        exit 1
    fi
}

# The rename gives <remote_path> a new inode, so the backup keeps the old content
# even if the file is rewritten in place later.
install_with_backup() { # local_file remote_path mode
    if is_unchanged "$1" "$2"; then
        log "$2 unchanged, nothing written"
        return 0
    fi
    if ! send_new "$1" "$2"; then
        log_error "Transfer of $1 failed, device file unchanged"
        exit 1
    fi
    if ! ssh "$REMOTE" "cd $REMOTE_DIR && chmod $3 '$2.new' &&
        if [ -f '$2' ]; then ln -f '$2' \"$2.backup.\$(date +%Y%m%d_%H%M%S)\" || exit 1; fi
        mv '$2.new' '$2'"; then
        log_error "Activating $2 failed"
        exit 1
    fi
    log_success "$2 transferred (backup is a hard link)"
}

log "Transferring..."
install_file VERSION VERSION
if [ -f bticino_bridge ]; then
    install_with_backup bticino_bridge bticino_bridge 755
else
    log_error "bticino_bridge not found"
    exit 1
fi

# Transfer Svelte web files (if present). Assets first, so the new index.html never
# points to a missing file.
if [ "$SKIP_WEB" = false ] && [ -d "web/dist" ]; then
    log "Transferring Svelte web files..."
    ssh "$REMOTE" "mkdir -p $REMOTE_DIR/web/dist/assets"
    asset_names=""
    for file in web/dist/assets/*; do
        if [ -f "$file" ]; then
            filename=$(basename "$file")
            asset_names="$asset_names $filename"
            install_file "$file" "web/dist/assets/$filename"
        fi
    done
    install_file web/dist/index.html web/dist/index.html
    # Old builds would otherwise stay on the flash forever.
    if [ -n "$asset_names" ]; then
        ssh "$REMOTE" "cd $REMOTE_DIR/web/dist/assets && for f in *; do case ' $asset_names ' in *\" \$f \"*) ;; *) rm -f \"\$f\" ;; esac; done" ||
            log_warning "Could not remove old web assets"
    fi
    log_success "Svelte web files up to date"
fi

# Transfer config.yaml (matches '-config config.yaml' used on restart)
if [ "$SKIP_CONFIG" = false ]; then
    if [ -f "$CONFIG_FILE" ]; then
        log "Transferring config ($CONFIG_FILE -> $REMOTE_DIR/config.yaml)..."
        install_with_backup "$CONFIG_FILE" config.yaml 644
    else
        log_warning "Config file $CONFIG_FILE not found, skipping config deploy"
    fi
else
    log "Skipping config deploy (--skip-config)"
fi

# Transfer API docs for /api/docs/ (server does FileServer(http.Dir("docs")))
if [ "$SKIP_DOCS" = false ]; then
    if [ -d "docs" ]; then
        log "Transferring API docs (docs/ -> $REMOTE_DIR/docs/)..."
        ssh "$REMOTE" "mkdir -p $REMOTE_DIR/docs"
        for file in docs/index.html docs/swagger.json docs/swagger.yaml docs/swagger-ui-bundle.js docs/swagger-ui-standalone-preset.js docs/swagger-ui.css; do
            if [ -f "$file" ]; then
                install_file "$file" "docs/$(basename "$file")"
            else
                log_warning "Docs file $file not found, skipping"
            fi
        done
        log_success "API docs up to date (check /api/docs/ with trailing slash)"
    else
        log_warning "docs/ directory not found, skipping docs deploy"
    fi
else
    log "Skipping docs deploy (--skip-docs)"
fi

# Restart service: TERM first so in-dialog sessions get their BYE, wait up to
# 15 s for the exit (grace covers frame grab + BYE), -9 only if it hangs.
# pidof, because the remote shell has the bridge's name in its command line.
log "Restarting service..."
ssh "$REMOTE" "cd $REMOTE_DIR && (pkill bticino_bridge || true); i=0; while pidof bticino_bridge > /dev/null && [ \$i -lt 15 ]; do sleep 1; i=\$((i + 1)); done; (pkill -9 bticino_bridge || true); sleep 2; nohup ./bticino_bridge -config config.yaml > /dev/null 2>&1 &"
log_success "Service restarted"

# Verify
log "Verifying..."
# Checks the bridge about 10 s into its run, which also catches an early crash.
sleep 12
if ssh "$REMOTE" "pidof bticino_bridge > /dev/null"; then
    log_success "Service running"
else
    log_error "Service not running. Last lines of the crash log:"
    ssh "$REMOTE" "tail -n 30 $REMOTE_DIR/bticino_bridge.crash.log" || true
    exit 1
fi

# Only after a successful start, so a failed deploy still has all its rollback options.
log "Removing old backups, keeping $KEEP_BACKUPS of each..."
ssh "$REMOTE" "cd $REMOTE_DIR && for prefix in bticino_bridge.backup. config.yaml.backup.; do
        ls -1r \"\$prefix\"* 2> /dev/null | tail -n +$((KEEP_BACKUPS + 1)) | while read -r old; do rm -f \"\$old\"; done
    done" || log_warning "Could not remove old backups"

echo
echo -e "${GREEN}╔═══════════════════════════════════════════════════════════╗${NC}"
echo -e "${GREEN}║              ✅ Deploy completed successfully!             ║${NC}"
echo -e "${GREEN}╚═══════════════════════════════════════════════════════════╝${NC}"
echo
HOST=$(ssh -G "$REMOTE" | awk '/^hostname /{print $2}')
echo "Next steps:"
echo "  1. Check: curl http://$HOST:8082/api/status"
echo "  2. Logs: ssh $REMOTE 'tail -f /var/log/bticino_bridge.log'"
echo "  3. Web UI: http://$HOST:8082/"
echo
