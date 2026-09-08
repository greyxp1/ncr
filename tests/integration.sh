#!/usr/bin/env bash
set -euo pipefail

repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
cd "$repo"
cargo clippy --all-targets --locked -- -D warnings
ncr=(cargo run --quiet --locked --)
system=$(nix config show system)
export LC_ALL=C NCR_TEST_SYSTEM=$system
export NCR_TEST_BASH
NCR_TEST_BASH=$(readlink -f "$(command -v bash)")
unset NCR_FLAKE NCR_LIVE
small=path:./tests/fixtures/small
lock_files=(flake.lock tests/fixtures/mixed/flake.lock tests/fixtures/home-only/flake.lock)
lock_hashes=$(nix hash file "${lock_files[@]}")
tmp=$(mktemp -d "${TMPDIR:-/tmp}/ncr-integration.XXXXXX")
trap 'rm -rf -- "$tmp"' EXIT
transcript=$tmp/transcript
report=$tmp/report
rows=$tmp/rows
scenario=
status=0

fail() {
	printf 'FAIL: %s: %s\n' "$scenario" "$1" >&2
	exit 1
}
run() {
	scenario=$1
	shift
	printf '\nChecking %s\n' "$scenario"
	status=0
	case "$system" in
		*-darwin) script -q "$transcript" "$@" || status=$? ;;
		*)
			local command
			printf -v command '%q ' "$@"
			SHELL="$BASH" script -qefc "$command" "$transcript" || status=$?
			;;
	esac
	tr '\r' '\n' <"$transcript" | sed $'s/\033\\[[0-9;?]*[A-Za-z]//g' >"$report"
	awk -F '│' '/^│/ {
		for (i = 2; i < NF; i++) sub(/^[[:space:]]+/, "", $i)
		for (i = 2; i < NF; i++) sub(/[[:space:]]+$/, "", $i)
		if ($2 == "host") next
		if (NF == 8) print $2 "|" $3 "|" $4 "|" $5 "|" $6 "|" $7
		else if (NF == 7) print $2 "|-|" $3 "|" $4 "|" $5 "|" $6
		else exit 1
	}' "$report" >"$rows" || fail "malformed report row"
}
run_success() {
	run "$@"
	((status == 0)) || fail "expected success, got exit status $status"
	awk -F '|' '
		$4 == "—" && $5 == "—" && $6 == "—" { next }
		$4 ~ /^([0-9]+m)?[0-9]+[.][0-9]s$/ && $6 ~ /^[1-9][0-9]*$/ { next }
		{ exit 1 }
	' "$rows" || fail "invalid evaluation duration or skipped measurements"
}
run_failure() {
	run "$@"
	((status != 0)) || fail "expected failure"
}
expect() {
	grep -Fq -- "$2" "$1" || fail "expected $1 to contain $2"
}
expect_rows() {
	cat >"$tmp/expected"
	cut -d '|' -f 1,2,3,5,6 "$rows" >"$tmp/actual"
	diff -u "$tmp/expected" "$tmp/actual" || fail "unexpected rows, measurements, or ordering"
}

run_success "automatic discovery and closure measurements" "${ncr[@]}" "$small"
# Query NAR sizes independently of NCR's recursive path-info parser. The large
# output references the small output, so its closure contains exactly two paths.
small_path=$(nix eval --impure --raw "$small#systemConfigs.alpha.outPath")
large_path=$(nix eval --impure --raw "$small#systemConfigs.large.outPath")
small_size=$(nix-store --query --size "$small_path")
large_size=$(nix-store --query --size "$large_path")
large_closure=$(awk -v size="$((small_size + large_size))" 'BEGIN { printf "%.1f KiB", size / 1024 }')
expect_rows <<EOF
shared|NixOS|$system|$small_size B|1
shared|nix-darwin|$system|$small_size B|1
shared|Home Manager|$system|$small_size B|1
large|system-manager|$system|$large_closure|2
alpha|system-manager|$system|$small_size B|1
beta|system-manager|$system|$small_size B|1
EOF
expect "$report" "2 other-system configurations hidden"

run_success "show skipped configurations" "${ncr[@]}" --show-skipped "$small#systemConfigs"
expect_rows <<EOF
large|-|$system|$large_closure|2
alpha|-|$system|$small_size B|1
beta|-|$system|$small_size B|1
foreign|-|ncr-foreign|—|—
EOF
if grep -Fq 'configurations hidden' "$report"; then fail "shown rows counted as hidden"; fi

run_success "all systems" "${ncr[@]}" --all-systems "$small#systemConfigs"
expect_rows <<EOF
foreign|-|ncr-foreign|$large_closure|2
large|-|$system|$large_closure|2
alpha|-|$system|$small_size B|1
beta|-|$system|$small_size B|1
EOF

run_success "explicit foreign configuration" "${ncr[@]}" "$small#systemConfigs.foreign"
expect_rows <<EOF
foreign|-|ncr-foreign|$large_closure|2
EOF

run_success "duplicate explicit name across configuration kinds" "${ncr[@]}" "$small" shared shared
expect_rows <<EOF
shared|NixOS|$system|$small_size B|1
shared|nix-darwin|$system|$small_size B|1
shared|Home Manager|$system|$small_size B|1
EOF

run_success "default flake and Home Manager filter" env NCR_FLAKE="$small" "${ncr[@]}" --home
expect_rows <<EOF
shared|-|$system|$small_size B|1
EOF
expect "$report" "1 other-system configuration hidden"

for selector in shared homeConfigurations.shared; do
	run_success "default flake selector $selector" env NCR_FLAKE="$small" "${ncr[@]}" --home "$selector"
	expect_rows <<EOF
shared|-|$system|$small_size B|1
EOF
done

run_success "unqualified fragment" "${ncr[@]}" --home "$small#shared"
expect_rows <<EOF
shared|-|$system|$small_size B|1
EOF

run_failure "unknown configuration" "${ncr[@]}" "$small" missing
expect "$report" 'unknown configuration "missing"'
expect "$report" 'system-manager: alpha, beta, foreign, large'
run_failure "Home Manager namespace conflict" "${ncr[@]}" --home "$small#nixosConfigurations"
expect "$report" '--home conflicts with'
run_failure "unsupported flake" "${ncr[@]}" path:.
expect "$report" 'provides none of'
run_failure "missing flake reference" "${ncr[@]}"
expect "$report" 'missing flake reference and NCR_FLAKE is not set'

run_success "NixOS module contract" nix eval --raw .#checks.x86_64-linux.module.name

case "$system" in
	x86_64-linux) shared=desktop; system_only=laptop; home=grey@desktop; home_alt=para@desktop; section=NixOS; foreign=darwinConfigurations ;;
	aarch64-linux) shared=server; system_only=pi; home=grey@server; home_alt=para@server; section=NixOS; foreign=darwinConfigurations ;;
	aarch64-darwin) shared=macbook; system_only=studio; home=grey@macbook; home_alt=para@macbook; section=nix-darwin; foreign=nixosConfigurations ;;
	*) printf 'unsupported fixture system: %s\n' "$system" >&2; exit 1 ;;
esac
run_success "real Home Manager discovery and lazy filtering" "${ncr[@]}" path:./tests/fixtures/home-only
cut -d '|' -f 1,2,3 "$rows" | sort >"$tmp/actual"
printf '%s|-|%s\n' "$home" "$system" "$home_alt" "$system" | sort >"$tmp/expected"
diff -u "$tmp/expected" "$tmp/actual" || fail "unexpected Home Manager selection"
expect "$report" '4 other-system configurations hidden'
run_success "real mixed configurations" "${ncr[@]}" path:./tests/fixtures/mixed
cut -d '|' -f 1,2,3 "$rows" | sort >"$tmp/actual"
printf '%s|%s|%s\n' "$shared" "$section" "$system" "$system_only" "$section" "$system" \
	"$shared" 'Home Manager' "$system" "$home" 'Home Manager' "$system" | sort >"$tmp/expected"
diff -u "$tmp/expected" "$tmp/actual" || fail "unexpected mixed selection"
expect "$report" '8 other-system configurations hidden'
run_failure "real foreign namespace filtering" "${ncr[@]}" "path:./tests/fixtures/mixed#$foreign"
expect "$report" 'no supported configurations match system'

scenario="fixture lock files"
[[ $(nix hash file "${lock_files[@]}") == "$lock_hashes" ]] || fail "lock files changed during the suite"
printf 'All integration checks passed on %s.\n' "$system"
