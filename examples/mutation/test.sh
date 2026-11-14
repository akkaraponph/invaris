#!/bin/sh
set -eu

# A failure to run the subject is an infrastructure failure.
if ! actual=$(sh behavior.sh covered); then
    exit 2
fi
if [ "$actual" = covered-original ]; then
    status=PASS
    code=0
else
    status=ASSERTION_FAILED
    code=1
fi
printf '{"version":"invaris.test-result/v1alpha1","tests":[{"id":"covered","status":"%s"}]}\n' "$status"
exit "$code"
