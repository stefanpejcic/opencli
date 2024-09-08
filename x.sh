# debug.sh
for arg in "$@"; do
    if [ "$arg" == "-x" ]; then
        set -x
        echo "-x flag provided: display each command as it executes."
    fi
done
