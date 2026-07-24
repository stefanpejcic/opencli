#!/bin/bash
# Quick end-to-end check that a user created via `opencli user-add` is
# correctly isolated on its own rootless podman instance.
# Usage: bash test_podman_user.sh <username>

set -u
USERNAME="${1:?Usage: $0 <username>}"
PASS="\033[0;32mPASS\033[0m"
FAIL="\033[0;31mFAIL\033[0m"
WARN="\033[0;33mWARN\033[0m"

echo "== Testing user: $USERNAME =="
echo

# 1. linux user + uid
UID_N=$(stat -c '%u' "/home/$USERNAME" 2>/dev/null)
if [[ -n "$UID_N" ]]; then
    echo -e "[$PASS] linux user exists, uid=$UID_N"
else
    echo -e "[$FAIL] linux user '$USERNAME' does not exist"
    exit 1
fi

# 2. lingering enabled
if loginctl show-user "$USERNAME" 2>/dev/null | grep -q "^Linger=yes"; then
    echo -e "[$PASS] lingering enabled"
else
    echo -e "[$FAIL] lingering NOT enabled"
fi

# 3. podman.socket active for that user
SOCK_STATUS=$(machinectl shell "${USERNAME}@" /bin/bash -c 'systemctl --user is-active podman.socket' 2>/dev/null)
if [[ "$SOCK_STATUS" == "active" ]]; then
    echo -e "[$PASS] podman.socket is active"
else
    echo -e "[$FAIL] podman.socket is '$SOCK_STATUS' (expected active)"
    machinectl shell "${USERNAME}@" /bin/bash -c 'systemctl --user status podman.socket --no-pager' 2>/dev/null
fi

# 4. socket file exists at expected path
SOCK_PATH="/hostfs/run/user/${UID_N}/podman/podman.sock"
if [[ -S "$SOCK_PATH" ]]; then
    echo -e "[$PASS] socket file exists: $SOCK_PATH"
else
    echo -e "[$FAIL] socket file missing: $SOCK_PATH"
fi

# 5. storage.conf points graphroot at ~/docker-data and shared store is wired in
CONF="/home/${USERNAME}/.config/containers/storage.conf"
if [[ -f "$CONF" ]]; then
    if grep -q "graphroot.*docker-data" "$CONF" && grep -q "shared-containers" "$CONF"; then
        echo -e "[$PASS] storage.conf looks correct:"
        sed 's/^/       /' "$CONF"
    else
        echo -e "[$FAIL] storage.conf exists but content looks wrong:"
        sed 's/^/       /' "$CONF"
    fi
else
    echo -e "[$FAIL] storage.conf missing: $CONF"
fi

# 6. can we actually talk to this user's podman via remote socket?
INFO=$(CONTAINER_HOST="unix://${SOCK_PATH}" timeout 5 podman --remote info --format '{{.Store.GraphRoot}}' 2>&1)
if [[ $? -eq 0 ]]; then
    echo -e "[$PASS] podman --remote info works, GraphRoot=$INFO"
else
    echo -e "[$FAIL] podman --remote info failed: $INFO"
fi

# 7. THE important isolation check: root's podman must NOT see this user's containers
echo
echo "-- root's podman ps -a (should NOT contain this user's app containers) --"
podman ps -a --format "table {{.Names}}\t{{.Image}}" 2>/dev/null

# 8. this user's own containers (queried via their own socket, bypassing root entirely)
echo
echo "-- ${USERNAME}'s own podman ps -a (via their rootless socket) --"
CONTAINER_HOST="unix://${SOCK_PATH}" timeout 5 podman --remote ps -a --format "table {{.Names}}\t{{.Image}}\t{{.Status}}" 2>&1

# 9. autostart actually started services (check compose ps from inside their homedir)
echo
echo "-- ${USERNAME}'s podman-compose ps --"
( cd "/home/${USERNAME}" && CONTAINER_HOST="unix://${SOCK_PATH}" timeout 5 podman-compose ps 2>&1 )

# 10. shared image store dedup sanity check - user's own graphroot should NOT contain
#     full copies of images that already exist in the shared store
echo
echo "-- disk usage: user's own docker-data vs shared store --"
du -sh "/home/${USERNAME}/docker-data" 2>/dev/null
du -sh /var/lib/containers/shared-storage 2>/dev/null

echo
echo "== done =="
