#!/bin/sh
set -eu
case "$(cat source.txt)" in
  SAFE) findings='[]'; status=0 ;;
  FORBIDDEN) findings='[{"policyId":"no-forbidden-marker","path":"source.txt"}]'; status=1 ;;
  *) exit 2 ;;
esac
printf '{"version":"invaris.inspection/v1alpha1","policyId":"no-forbidden-marker","inspected":1,"findings":%s}\n' "$findings"
exit "$status"
