#!/bin/bash
#
# BTicino Bridge - Test Suite Master
# Runs all tests automatically
#
# Usage: ./run_all_tests.sh [options]
#
# Options:
#   --all          Run all tests (default)
#   --ui           UI tests only
#   --mqtt         MQTT tests only
#   --deploy       Deploy tests only
#   --sip          SIP tests only
#   --verbose      Show detailed output
#   --dry-run      Only show what would run
#

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Variables
DEVICE_IP="192.168.1.38"
DEVICE_USER="bticino"
TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPTS_DIR="$TEST_DIR/scripts"
DOCS_DIR="$TEST_DIR/docs"
VERBOSE=false
DRY_RUN=false
RUN_ALL=true
RUN_UI=false
RUN_MQTT=false
RUN_DEPLOY=false
RUN_SIP=false

# Counters
TOTAL_TESTS=0
PASSED_TESTS=0
FAILED_TESTS=0

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --all)
            RUN_ALL=true
            shift
            ;;
        --ui)
            RUN_UI=true
            RUN_ALL=false
            shift
            ;;
        --mqtt)
            RUN_MQTT=true
            RUN_ALL=false
            shift
            ;;
        --deploy)
            RUN_DEPLOY=true
            RUN_ALL=false
            shift
            ;;
        --sip)
            RUN_SIP=true
            RUN_ALL=false
            shift
            ;;
        --verbose)
            VERBOSE=true
            shift
            ;;
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        *)
            echo -e "${RED}Unknown option: $1${NC}"
            echo "Use --help to see options"
            exit 1
            ;;
    esac
done

# Logging functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[PASS]${NC} $1"
    ((PASSED_TESTS++))
    ((TOTAL_TESTS++))
}

log_warning() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[FAIL]${NC} $1"
    ((FAILED_TESTS++))
    ((TOTAL_TESTS++))
}

log_test() {
    echo -e "\n${BLUE}=== TEST: $1 ===${NC}"
}

# Test functions
test_connectivity() {
    log_test "Device connectivity"
    
    if ping -c 2 "$DEVICE_IP" > /dev/null 2>&1; then
        log_success "Ping to device ($DEVICE_IP)"
    else
        log_error "Ping to device ($DEVICE_IP) - FAILED"
    fi
    
    if ssh -o BatchMode=yes -o ConnectTimeout=5 "$DEVICE_USER@$DEVICE_IP" "echo OK" > /dev/null 2>&1; then
        log_success "SSH to device"
    else
        log_error "SSH to device - FAILED"
    fi
}

test_ui() {
    log_test "Configuration UI"
    
    # Test Settings Page
    if curl -s "http://$DEVICE_IP:8082/settings" | grep -q "Configuration Settings"; then
        log_success "Settings Page accessible"
    else
        log_error "Settings Page - NOT ACCESSIBLE"
    fi
    
    # Test API Config
    if curl -s "http://$DEVICE_IP:8082/api/config" | grep -q "Bridge"; then
        log_success "API /api/config responds"
    else
        log_error "API /api/config - NOT RESPONDING"
    fi
    
    # Test API Validate
    VALIDATE_RESPONSE=$(curl -s -X POST "http://$DEVICE_IP:8082/api/config/validate" \
        -H "Content-Type: application/json" \
        -d '{"config":{"bridge":{"name":"Test"},"openwebnet":{"port":30006},"web":{"port":8082}}}')
    
    if echo "$VALIDATE_RESPONSE" | grep -q "valid"; then
        log_success "API /api/config/validate works"
    else
        log_error "API /api/config/validate - FAILED"
    fi
    
    # Test API Backup
    BACKUP_RESPONSE=$(curl -s -X POST "http://$DEVICE_IP:8082/api/config/backup")
    
    if echo "$BACKUP_RESPONSE" | grep -q "success"; then
        log_success "API /api/config/backup works"
    else
        log_error "API /api/config/backup - FAILED"
    fi
}

test_mqtt() {
    log_test "MQTT Commands"
    
    if [ -x "$SCRIPTS_DIR/bticino_mqtt_commands_simple.sh" ]; then
        if $VERBOSE; then
            "$SCRIPTS_DIR/bticino_mqtt_commands_simple.sh"
        else
            "$SCRIPTS_DIR/bticino_mqtt_commands_simple.sh" > /dev/null 2>&1
        fi
        
        if [ $? -eq 0 ]; then
            log_success "MQTT commands script executed"
        else
            log_error "MQTT commands script - FAILED"
        fi
    else
        log_warning "MQTT script not found or not executable"
    fi
}

test_deploy() {
    log_test "Deploy Scripts"
    
    # Check deploy scripts
    for script in deploy_to_bticino.sh deploy_auto.sh deploy_and_test_safe.sh; do
        if [ -x "$SCRIPTS_DIR/$script" ]; then
            log_success "Script $script exists and is executable"
        else
            log_error "Script $script - NOT FOUND OR NOT EXECUTABLE"
        fi
    done
    
    # Check binary
    if [ -x "$TEST_DIR/bticino_bridge" ]; then
        log_success "Binary bticino_bridge exists and is executable"
        
        if $VERBOSE; then
            "$TEST_DIR/bticino_bridge" -version
        fi
    else
        log_error "Binary bticino_bridge - NOT FOUND"
    fi
}

test_sip() {
    log_test "SIP Configuration"
    
    # Check flexisip on device
    FLEXISIP_STATUS=$(ssh "$DEVICE_USER@$DEVICE_IP" "ps aux | grep flexisip | grep -v grep" 2>/dev/null)
    
    if [ -n "$FLEXISIP_STATUS" ]; then
        log_success "Flexisip running on device"
    else
        log_warning "Flexisip is NOT running"
    fi
    
    # Check SIP logs
    SIP_LOGS=$(ssh "$DEVICE_USER@$DEVICE_IP" "grep -i 'sip\|register' /var/log/bticino_bridge.log 2>/dev/null | tail -5")
    
    if [ -n "$SIP_LOGS" ]; then
        if $VERBOSE; then
            echo "$SIP_LOGS"
        fi
        log_success "SIP logs available"
    else
        log_warning "No recent SIP logs"
    fi
}

test_documentation() {
    log_test "Documentation"
    
    # Check master README
    if [ -f "$DOCS_DIR/README.md" ]; then
        log_success "Master README.md exists"
    else
        log_error "Master README.md - NOT FOUND"
    fi
    
    # Count documents
    DOC_COUNT=$(ls -1 "$DOCS_DIR"/*.md 2>/dev/null | wc -l)
    log_success "$DOC_COUNT documents in docs/"
    
    # Check scripts
    SCRIPT_COUNT=$(ls -1 "$SCRIPTS_DIR"/*.sh 2>/dev/null | wc -l)
    log_success "$SCRIPT_COUNT scripts in scripts/"
}

# Main
echo -e "${BLUE}"
echo "╔═══════════════════════════════════════════════════════════╗"
echo "║     BTicino Bridge - Test Suite Master v0.13.0           ║"
echo "╚═══════════════════════════════════════════════════════════╝"
echo -e "${NC}"

if [ "$DRY_RUN" = true ]; then
    echo -e "${YELLOW}=== DRY RUN MODE ===${NC}"
    echo "The following tests would run:"
    
    if [ "$RUN_ALL" = true ] || [ "$RUN_UI" = true ]; then
        echo "  - UI Tests"
    fi
    if [ "$RUN_ALL" = true ] || [ "$RUN_MQTT" = true ]; then
        echo "  - MQTT Tests"
    fi
    if [ "$RUN_ALL" = true ] || [ "$RUN_DEPLOY" = true ]; then
        echo "  - Deploy Tests"
    fi
    if [ "$RUN_ALL" = true ] || [ "$RUN_SIP" = true ]; then
        echo "  - SIP Tests"
    fi
    
    echo ""
    echo "Documentation:"
    echo "  - Check docs/README.md"
    echo "  - Check executable scripts"
    exit 0
fi

echo "Configuration:"
echo "  Device: $DEVICE_USER@$DEVICE_IP"
echo "  Test Dir: $TEST_DIR"
echo "  Scripts: $SCRIPTS_DIR"
echo "  Docs: $DOCS_DIR"
echo "  Verbose: $VERBOSE"
echo ""

# Run tests
test_connectivity

if [ "$RUN_ALL" = true ] || [ "$RUN_UI" = true ]; then
    test_ui
fi

if [ "$RUN_ALL" = true ] || [ "$RUN_MQTT" = true ]; then
    test_mqtt
fi

if [ "$RUN_ALL" = true ] || [ "$RUN_DEPLOY" = true ]; then
    test_deploy
fi

if [ "$RUN_ALL" = true ] || [ "$RUN_SIP" = true ]; then
    test_sip
fi

test_documentation

# Summary
echo -e "\n${BLUE}╔═══════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║                    TEST SUMMARY                            ║${NC}"
echo -e "${BLUE}╚═══════════════════════════════════════════════════════════╝${NC}"
echo ""
echo "Total Tests:  $TOTAL_TESTS"
echo -e "${GREEN}Passed:       $PASSED_TESTS${NC}"
echo -e "${RED}Failed:       $FAILED_TESTS${NC}"
echo ""

if [ $FAILED_TESTS -eq 0 ]; then
    echo -e "${GREEN}✅ ALL TESTS PASSED!${NC}"
    exit 0
else
    echo -e "${RED}❌ SOME TESTS FAILED${NC}"
    echo ""
    echo "Check the logs above for more details."
    echo "For more information, use --verbose"
    exit 1
fi
