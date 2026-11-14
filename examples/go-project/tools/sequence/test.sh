#!/bin/sh
# A deliberately nondeterministic test with a predetermined outcome sequence,
# used to demonstrate MIXED campaign results without randomness.
#
# On the original code it always passes. On a mutant it counts its runs in
# $INVARIS_FIXTURE_STATE/count and fails on odd counts and passes on even
# counts, so three attempts give KILLED, SURVIVED, KILLED. Use a fresh,
# empty state directory for each campaign run. Without one it exits 2, which
# Invaris records as INCONCLUSIVE.
set -eu

state=${INVARIS_FIXTURE_STATE:-}
if [ -z "$state" ] || [ ! -d "$state" ]; then
    echo "sequence: INVARIS_FIXTURE_STATE must name a directory" >&2
    exit 2
fi
pass='{"version":"invaris.test-result/v1alpha1","tests":[{"id":"sequence","status":"PASS"}]}'
fail='{"version":"invaris.test-result/v1alpha1","tests":[{"id":"sequence","status":"ASSERTION_FAILED"}]}'

if grep -q 'cents\*percent/100' internal/discount/discount.go; then
    printf '%s\n' "$pass"
    exit 0
fi
count=$(( $(cat "$state/count" 2>/dev/null || echo 0) + 1 ))
echo "$count" > "$state/count"
if [ $((count % 2)) -eq 1 ]; then
    printf '%s\n' "$fail"
    exit 1
fi
printf '%s\n' "$pass"
