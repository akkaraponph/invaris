#!/bin/sh
set -eu

case "${1-}" in
    "") real_provider=false ;;
    --with-dependency-cruiser) real_provider=true ;;
    *) echo "usage: sh scripts/smoke.sh [--with-dependency-cruiser]" >&2; exit 2 ;;
esac
test "$#" -le 1

cd "$(dirname "$0")/.."
root=$(pwd -P)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/invaris-smoke.XXXXXXXX")
trap 'rm -r -- "$tmp"' EXIT
trap 'exit 1' HUP INT TERM
export GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local
go build -o "$tmp/invaris" ./cmd/invaris
go build -o "$tmp/invaris-command-provider" ./cmd/invaris-command-provider
export PATH="$tmp:$PATH"

count=0
accept() {
    "$tmp/invaris" --report "$tmp/report.json" "$1" "$2" >"$tmp/stdout.json"
    cmp "$tmp/report.json" "$tmp/stdout.json"
    grep -q '"schemaVersion": "invaris.evidence/v1alpha2"' "$tmp/stdout.json"
    grep -q '"decision": "ACCEPT"' "$tmp/stdout.json"
    rm "$tmp/report.json"
    count=$((count + 1))
    printf 'PASS %s %s\n' "$1" "$2"
}

for config in "$root/examples/invaris.json" "$root/examples/structured/invaris.json" "$root/examples/v1alpha2/invaris.json"; do
    for action in run check challenge; do
        accept "$action" "$config"
    done
done
for action in run check; do
    accept "$action" "$root/examples/provider/invaris.json"
done

for action in check challenge; do
    accept "$action" "$root/examples/forbidden-dependency/invaris.json"
done

mutation() {
    cfg="$root/examples/mutation/$1.json"
    status=0
    "$tmp/invaris" --report "$tmp/report.json" mutate "$cfg" >"$tmp/stdout.json" || status=$?
    test "$status" -eq "$2"
    cmp "$tmp/report.json" "$tmp/stdout.json"
    grep -q '"schemaVersion": "invaris.evidence/v1alpha3"' "$tmp/stdout.json"
    grep -q "\"verdict\": \"$3\"" "$tmp/stdout.json"
    rm "$tmp/report.json"
    count=$((count + 1))
    printf 'PASS mutate %s (%s)\n' "$cfg" "$3"
}

mutation killed 0 KILLED
mutation survived 1 SURVIVED
mutation invalid 2 INVALID

if "$real_provider"; then
    export PATH="$root/adapters/dependency-cruiser:$PATH"
    for action in run check challenge; do
        accept "$action" "$root/examples/dependency-cruiser/invaris.json"
    done
else
    echo 'SKIP Dependency-Cruiser (offline tier)'
fi
printf '%s smoke checks passed\n' "$count"
