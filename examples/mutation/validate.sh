#!/bin/sh
set -eu

# Open the subject separately so an unreadable file cannot look like syntax failure.
exec 3<behavior.sh
if sh -n <&3; then
    status=VALID
    code=0
else
    case $? in
        2) status=INVALID; code=1 ;;
        *) exit 2 ;;
    esac
fi
printf '{"version":"invaris.validity-result/v1alpha1","status":"%s"}\n' "$status"
exit "$code"
