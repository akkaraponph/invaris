#!/bin/sh
set -eu
case "$(cat dependencies.txt)" in
  allowed-lib) findings='[]'; status=0 ;;
  forbidden-lib) findings='[{"policyId":"no-forbidden-dependency","path":"dependencies.txt"}]'; status=1 ;;
  *) exit 2 ;;
esac
printf '{"version":"invaris.inspection/v1alpha1","policyId":"no-forbidden-dependency","inspected":1,"findings":%s}\n' "$findings"
exit "$status"
