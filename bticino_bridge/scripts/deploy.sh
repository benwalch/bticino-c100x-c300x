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
DRY_RUN=false
VERBOSE=false
REMOTE="bticino"
REMOTE_DIR="/home/bticino/cfg/extra"

# Parsear argumentos
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
    echo "Los siguientes pasos se ejecutarían:"
    [ "$SKIP_WEB" = false ] && echo "  - Build web frontend"
    [ "$SKIP_GO" = false ] && echo "  - Build Go binary"
    echo "  - Deploy to device"
    exit 0
fi

# Paso 1: Build Web (si existe web/ y no se skip)
if [ "$SKIP_WEB" = false ] && [ -d "web" ]; then
    log "[1/3] Building web frontend..."
    if [ -f "web/package.json" ]; then
        cd web
        npm install --silent
        npm run build --silent
        cd ..
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
    GOOS=linux GOARCH=arm GOARM=7 go build -o bticino_bridge ./cmd/main.go 2>&1 | grep -v "^#" || true
    if [ -f "bticino_bridge" ]; then
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

# Transfer binary
log "Transferring binary..."
# Transfer VERSION file
echo "Transferring VERSION file..."
cat VERSION | ssh "$REMOTE" "cat > $REMOTE_DIR/VERSION"
log_success "VERSION file transferred"

base64 bticino_bridge | ssh "$REMOTE" "cd $REMOTE_DIR && base64 -d > bticino_bridge.new && chmod +x bticino_bridge.new"
log_success "Binary transferred"

# Backup
log "Creating backup..."
ssh "$REMOTE" "cd $REMOTE_DIR && [ -f bticino_bridge ] && cp bticino_bridge bticino_bridge.backup.\$(date +%Y%m%d_%H%M%S) || true"
log_success "Backup created"

# Activar
log "Activating new binary..."
ssh "$REMOTE" "cd $REMOTE_DIR && mv bticino_bridge.new bticino_bridge"
log_success "Binary activated"

# Transferir web Svelte (si existe)
if [ "$SKIP_WEB" = false ] && [ -d "web/dist" ]; then
    log "Transferring Svelte web files..."
    ssh "$REMOTE" "cd $REMOTE_DIR && mkdir -p web/dist/assets"
    # Copiar archivos con base64 (más confiable que scp)
    base64 web/dist/index.html | ssh "$REMOTE" "base64 -d > $REMOTE_DIR/web/dist/index.html"
    for file in web/dist/assets/*; do
        if [ -f "$file" ]; then
            filename=$(basename "$file")
            base64 "$file" | ssh "$REMOTE" "base64 -d > $REMOTE_DIR/web/dist/assets/$filename"
        fi
    done
    log_success "Svelte web files transferred"
fi

# Reiniciar servicio
log "Restarting service..."
ssh "$REMOTE" "cd $REMOTE_DIR && pkill -9 bticino_bridge || true && sleep 2 && nohup ./bticino_bridge -config config.yaml > /var/log/bticino_bridge.log 2>&1 &"
sleep 5
log_success "Service restarted"

# Verify
log "Verifying..."
if ssh "$REMOTE" "ps aux | grep bticino_bridge | grep -v grep > /dev/null 2>&1"; then
    log_success "Service running"
else
    log_error "Service not running"
    exit 1
fi

echo
echo -e "${GREEN}╔═══════════════════════════════════════════════════════════╗${NC}"
echo -e "${GREEN}║              ✅ Deploy completed successfully!             ║${NC}"
echo -e "${GREEN}╚═══════════════════════════════════════════════════════════╝${NC}"
echo
echo "Next steps:"
echo "  1. Check: curl http://192.168.1.38:8082/api/status"
echo "  2. Logs: ssh bticino 'tail -f /var/log/bticino_bridge.log'"
echo "  3. Web UI: http://192.168.1.38:8082/"
echo
