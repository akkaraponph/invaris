#!/bin/sh
# Offline structured scanner for the built-in operator example.
# Usage: sh scan.sh POLICY FILE
# POLICY is trailing-whitespace, line-endings or synthetic-secret. Prints one
# invaris.inspection/v1alpha1 document and exits 1 when FILE violates it.
set -eu
policy=$1 file=$2
cr=$(printf '\r')
case "$policy" in
  trailing-whitespace) grep -q '[[:blank:]]$' "$file" && found=1 || found=0 ;;
  line-endings) grep -q "$cr" "$file" && found=1 || found=0 ;;
  synthetic-secret) grep -q 'INVARIS_TEST_TOKEN_DO_NOT_USE_00000000' "$file" && found=1 || found=0 ;;
  *) echo "unknown policy $policy" >&2; exit 2 ;;
esac
if [ "$found" = 1 ]; then
  printf '{"version":"invaris.inspection/v1alpha1","policyId":"%s","inspected":1,"findings":[{"policyId":"%s","path":"%s"}]}\n' "$policy" "$policy" "$file"
  exit 1
fi
printf '{"version":"invaris.inspection/v1alpha1","policyId":"%s","inspected":1,"findings":[]}\n' "$policy"
