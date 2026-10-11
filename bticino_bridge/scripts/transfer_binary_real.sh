#!/bin/bash
#
# Transfer of the bticino_bridge binary to the real device
# Uses base64 + chunks for maximum compatibility
#

set -e

echo "========================================"
echo "  BTicino Bridge - Deploy Real"
echo "  Binary transfer v0.12.0"
echo "========================================"
echo ""

# Configuration
SSH_HOST="192.168.1.38"
SSH_USER="root"
REMOTE_DIR="/home/bticino/cfg/extra"
BINARY_NAME="bticino_bridge"

# SSH options for compatibility with legacy device
SSH_OPTS="-o HostKeyAlgorithms=+ssh-rsa -o PubkeyAcceptedAlgorithms=+ssh-rsa"

echo "Host: ${SSH_HOST}"
echo "User: ${SSH_USER}"
echo "Remote: ${REMOTE_DIR}"
echo ""

# Step 1: Check SSH connection
echo "[1/6] Checking SSH connection..."
if ! ssh ${SSH_OPTS} ${SSH_USER}@${SSH_HOST} "echo 'SSH OK'" > /dev/null 2>&1; then
    echo "ERROR: Could not connect via SSH"
    echo "Try: ssh ${SSH_USER}@${SSH_HOST}"
    exit 1
fi
echo "✅ SSH connection OK"
echo ""

# Step 2: Remount filesystem as read-write
echo "[2/6] Remounting filesystem as read-write..."
ssh ${SSH_OPTS} ${SSH_USER}@${SSH_HOST} "mount -o remount,rw /" 2>/dev/null || echo "⚠️ Already RW or failed (not critical)"
echo "✅ Filesystem RW"
echo ""

# Step 3: Create recordings directory
echo "[3/6] Creating recordings directory..."
ssh ${SSH_OPTS} ${SSH_USER}@${SSH_HOST} "mkdir -p ${REMOTE_DIR}/recordings && chmod 755 ${REMOTE_DIR}/recordings"
echo "✅ Recordings directory created"
echo ""

# Step 4: Transfer chunks via base64
echo "[4/6] Transferring binary (10 chunks)..."
echo "    This may take 2-5 minutes..."

# Function to transfer one chunk
transfer_chunk() {
    local chunk_file=$1
    local chunk_name=$(basename "$chunk_file")
    
    # Read chunk and send over SSH
    cat "$chunk_file" | ssh ${SSH_OPTS} ${SSH_USER}@${SSH_HOST} "cat >> ${REMOTE_DIR}/${chunk_name}"
    echo "    ✅ Chunk: ${chunk_name}"
}

# Transfer each chunk
for chunk in /tmp/bticino_bridge.base64.chunk.*; do
    transfer_chunk "$chunk"
done

echo "✅ All chunks transferred"
echo ""

# Step 5: Assemble and decode binary
echo "[5/6] Assembling and decoding binary..."
ssh ${SSH_OPTS} ${SSH_USER}@${SSH_HOST} "cd ${REMOTE_DIR} && cat bticino_bridge.base64.chunk.* > bticino_bridge.base64 && base64 -d bticino_bridge.base64 > ${BINARY_NAME}.new && rm bticino_bridge.base64.chunk.* && rm bticino_bridge.base64"

# Verify binary
ssh ${SSH_OPTS} ${SSH_USER}@${SSH_HOST} "ls -lh ${REMOTE_DIR}/${BINARY_NAME}.new"
echo "✅ Binary decoded"
echo ""

# Step 6: Make executable and activate
echo "[6/6] Activating binary..."
ssh ${SSH_OPTS} ${SSH_USER}@${SSH_HOST} "chmod +x ${REMOTE_DIR}/${BINARY_NAME}.new && mv ${REMOTE_DIR}/${BINARY_NAME}.new ${REMOTE_DIR}/${BINARY_NAME}"
ssh ${SSH_OPTS} ${SSH_USER}@${SSH_HOST} "ls -lh ${REMOTE_DIR}/${BINARY_NAME}"
echo "✅ Binary activated"
echo ""

echo "========================================"
echo "  Deploy complete!"
echo "========================================"
echo ""
echo "Next steps:"
echo "  1. Create configuration: ssh root@${SSH_HOST}"
echo "     cd ${REMOTE_DIR}"
echo "     nano config.yaml (or use the example)"
echo ""
echo "  2. Test binary: ssh root@${SSH_HOST}"
echo "     cd ${REMOTE_DIR}"
echo "     ./${BINARY_NAME} -version"
echo ""
echo "  3. Start service manually:"
echo "     ./${BINARY_NAME} -config config.yaml &"
echo ""
echo "To roll back:"
echo "  ssh root@${SSH_HOST} 'rm ${REMOTE_DIR}/${BINARY_NAME}'"
echo "  (and restore backup if one exists)"
echo ""
