#!/bin/sh
case "${1-}" in
    covered) printf '%s\n' 'covered-original' ;;
    uncovered) printf '%s\n' 'uncovered-original' ;;
    *) exit 2 ;;
esac
