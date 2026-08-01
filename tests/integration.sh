#!/usr/bin/env bash
set -euo pipefail

repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
system=$(nix config show system)

case "$system" in
	x86_64-linux | aarch64-linux)
		section=NixOS
		foreign_section=nix-darwin
		namespace=nixosConfigurations
		foreign_namespace=darwinConfigurations
		namespace_skipped=2
		;;
	aarch64-darwin)
		section=nix-darwin
		foreign_section=NixOS
		namespace=darwinConfigurations
		foreign_namespace=nixosConfigurations
		namespace_skipped=0
		;;
	*)
		printf 'ncr integration tests: unsupported system %q\n' "$system" >&2
		exit 1
		;;
esac

if [[ -n ${NCR_BIN:-} ]]; then
	ncr=$NCR_BIN
else
	package=$(nix build --no-link --print-out-paths "path:$repo")
	ncr=$package/bin/ncr
fi
if [[ ! -x $ncr ]]; then
	printf 'ncr integration tests: executable not found: %s\n' "$ncr" >&2
	exit 1
fi

export CLICOLOR_FORCE=1
export NCR_LIVE=1
export NCR_TEST_SYSTEM=$system
if [[ -t 1 && -z ${NO_COLOR:-} && ${TERM:-} != dumb ]]; then
	interactive=true
	cyan=$'\033[1;36m'
	green=$'\033[1;32m'
	reset=$'\033[0m'
else
	interactive=false
	export NO_COLOR=1
	cyan=
	green=
	reset=
fi
cd "$repo"
mixed=path:./tests/fixtures/mixed
home_only=path:./tests/fixtures/home-only
root=path:.
systems=(x86_64-linux aarch64-linux aarch64-darwin)
configuration_names() {
	case "$1" in
		x86_64-linux) printf '%s\n' 'desktop laptop grey@desktop para@desktop' ;;
		aarch64-linux) printf '%s\n' 'server pi grey@server para@server' ;;
		aarch64-darwin) printf '%s\n' 'macbook studio grey@macbook para@macbook' ;;
	esac
}
read -r shared system_only home home_alt < <(configuration_names "$system")
lock_files=(flake.lock tests/fixtures/mixed/flake.lock tests/fixtures/home-only/flake.lock)
lock_hashes=$(nix hash file "${lock_files[@]}")

tmp=$(mktemp -d "${TMPDIR:-/tmp}/ncr-integration.XXXXXX")
stdout=$tmp/stdout
stderr=$tmp/stderr
plain_stdout=$tmp/stdout.plain
plain_stderr=$tmp/stderr.plain
stdout_pipe=$tmp/stdout.pipe
stderr_pipe=$tmp/stderr.pipe
mkfifo "$stdout_pipe" "$stderr_pipe"
cleanup() {
	rm -rf -- "$tmp"
}
trap cleanup EXIT

scenario=
status=0
display_lines=0
terminal_columns() {
	local rows columns
	if read -r rows columns < <(stty size </dev/tty 2>/dev/null) && ((columns > 0)); then
		printf '%d\n' "$columns"
	elif [[ ${COLUMNS:-} =~ ^[1-9][0-9]*$ ]]; then
		printf '%d\n' "$COLUMNS"
	else
		printf '80\n'
	fi
}
display_rows() {
	local file=$1 columns=$2 line width rows total=0
	while IFS= read -r line || [[ -n $line ]]; do
		width=${#line}
		if ((width == 0)); then
			rows=1
		else
			rows=$(((width + columns - 1) / columns))
		fi
		total=$((total + rows))
	done <"$file"
	printf '%d\n' "$total"
}
fail() {
	printf 'FAIL: %s: %s\n' "$scenario" "$1" >&2
	printf '%s\n' '--- stdout ---' >&2
	sed -n '1,240p' "$plain_stdout" >&2
	printf '%s\n' '--- stderr ---' >&2
	sed -n '1,240p' "$plain_stderr" >&2
	exit 1
}
run() {
	scenario=$1
	shift
	local command=$1
	shift
	printf '\n%s── %s%s\n' "$cyan" "$scenario" "$reset"
	local stdout_pid stderr_pid stdout_status stderr_status
	tee "$stdout" <"$stdout_pipe" &
	stdout_pid=$!
	tee "$stderr" <"$stderr_pipe" >&2 &
	stderr_pid=$!
	set +e
	"$command" "$@" >"$stdout_pipe" 2>"$stderr_pipe"
	status=$?
	wait "$stdout_pid"
	stdout_status=$?
	wait "$stderr_pid"
	stderr_status=$?
	set -e
	local captured
	captured=$(<"$stdout")
	local final_stdout=${captured##*$'\033[J'}
	printf '%s\n' "$final_stdout" |
		sed $'s/\033\\[[0-9;]*m//g' >"$plain_stdout"
	sed $'s/\033\\[[0-9;]*m//g' "$stderr" >"$plain_stderr"
	if $interactive; then
		local columns
		columns=$(terminal_columns)
		display_lines=$(display_rows "$plain_stderr" "$columns")
		if [[ -n $final_stdout ]]; then
			display_lines=$((display_lines + $(display_rows "$plain_stdout" "$columns")))
		fi
	fi
	if ((stdout_status != 0 || stderr_status != 0)); then
		status=125
		fail "failed to capture command output"
	fi
}
expect() {
	if ! grep -Fq -- "$2" "$1"; then
		fail "expected $1 to contain $2"
	fi
}
reject() {
	if grep -Fq -- "$2" "$1"; then
		fail "expected $1 not to contain $2"
	fi
}
expect_exact_count() {
	local count
	count=$(grep -Fo -- "$2" "$1" | wc -l || true)
	if ((count != $3)); then
		fail "expected $1 to contain $2 exactly $3 times, found $count"
	fi
}
run_success() {
	run "$@"
	if ((status != 0)); then
		fail "expected success, got exit status $status"
	fi
	reject "$plain_stdout" "all hosts"
	reject "$plain_stdout" "all homes"
}
run_failure() {
	run "$@"
	if ((status == 0)); then
		fail "expected failure"
	fi
}
pass() {
	if $interactive; then
		local distance=$((display_lines + 1))
		printf '\033[%dA\r\033[2K%s── %s%s %s✓ Passed%s\033[%dB\r' \
			"$distance" "$cyan" "$scenario" "$reset" "$green" "$reset" "$distance"
	else
		printf '%s✓ Passed%s\n' "$green" "$reset"
	fi
}

run_success "Home Manager-only automatic discovery" "$ncr" "$home_only"
expect "$plain_stdout" "$home"
expect "$plain_stdout" "$home_alt"
reject "$plain_stdout" "NixOS"
reject "$plain_stdout" "nix-darwin"
reject "$plain_stdout" "Home Manager"
reject "$plain_stdout" "│ type "
reject "$plain_stdout" "--"
expect "$plain_stdout" "4 other-system configurations hidden"
expect "$stdout" $'\033[J'
expect "$stdout" "Realizing closures"
for removed_status in detecting waiting calculating skipped "Inspecting closures" "⠋"; do
	reject "$stdout" "$removed_status"
done
reject "$plain_stdout" "Realizing closures"
reject "$plain_stderr" "foreign activation package was evaluated before filtering"
for candidate in "${systems[@]}"; do
	if [[ $candidate != "$system" ]]; then
		read -r candidate_shared candidate_system candidate_home candidate_alt \
			< <(configuration_names "$candidate")
		reject "$plain_stdout" "$candidate_home"
		reject "$plain_stdout" "$candidate_alt"
	fi
done
pass
if ! $interactive; then
	unset NCR_LIVE
fi

run_success "mixed automatic discovery" "$ncr" "$mixed"
expect "$plain_stdout" "$section"
reject "$plain_stdout" "$foreign_section"
expect "$plain_stdout" "Home Manager"
expect "$plain_stdout" "$shared"
expect "$plain_stdout" "$system_only"
expect "$plain_stdout" "$home"
expect "$plain_stdout" "│ type "
expect_exact_count "$plain_stdout" "│ host " 1
reject "$plain_stdout" "--"
expect "$plain_stdout" "8 other-system configurations hidden"
for candidate in "${systems[@]}"; do
	if [[ $candidate != "$system" ]]; then
		read -r candidate_shared candidate_system candidate_home candidate_alt \
			< <(configuration_names "$candidate")
		reject "$plain_stdout" "$candidate_shared"
		reject "$plain_stdout" "$candidate_system"
		reject "$plain_stdout" "$candidate_home"
	fi
done
reject "$plain_stderr" "required to build"
pass

run_success "show skipped configurations" "$ncr" --show-skipped "$mixed"
expect "$plain_stdout" "NixOS"
expect "$plain_stdout" "nix-darwin"
expect "$plain_stdout" "Home Manager"
expect_exact_count "$plain_stdout" "--" 24
reject "$plain_stdout" "other-system configurations hidden"
for candidate in "${systems[@]}"; do
	if [[ $candidate != "$system" ]]; then
		read -r candidate_shared candidate_system candidate_home candidate_alt \
			< <(configuration_names "$candidate")
		expect "$plain_stdout" "$candidate_shared"
		expect "$plain_stdout" "$candidate_system"
		expect "$plain_stdout" "$candidate_home"
	fi
done
pass

run_success "Home Manager filter" "$ncr" --home "$mixed"
expect "$plain_stdout" "$shared"
expect "$plain_stdout" "$home"
reject "$plain_stdout" "$system_only"
reject "$plain_stdout" "Home Manager"
reject "$plain_stdout" "│ type "
reject "$plain_stdout" "--"
expect "$plain_stdout" "4 other-system configurations hidden"
pass

run_success "qualified current-system namespace" "$ncr" "$mixed#$namespace"
expect "$plain_stdout" "$shared"
expect "$plain_stdout" "$system_only"
reject "$plain_stdout" "$home"
reject "$plain_stdout" "Home Manager"
reject "$plain_stdout" "$section"
reject "$plain_stdout" "--"
if ((namespace_skipped > 0)); then
	expect "$plain_stdout" "$namespace_skipped other-system configurations hidden"
else
	reject "$plain_stdout" "other-system configurations hidden"
fi
pass

run_success "duplicate explicit name" "$ncr" "$mixed" "$shared"
expect "$plain_stdout" "$section"
expect "$plain_stdout" "Home Manager"
expect_exact_count "$plain_stdout" "$shared" 2
reject "$plain_stdout" "--"
pass

run_success "unqualified fragment" "$ncr" "$home_only#$home"
expect "$plain_stdout" "$home"
reject "$plain_stdout" "--"
pass

run_success "qualified configuration" "$ncr" "$mixed#$namespace.$system_only"
expect "$plain_stdout" "$system_only"
reject "$plain_stdout" "$shared"
reject "$plain_stdout" "$home"
reject "$plain_stdout" "Home Manager"
reject "$plain_stdout" "--"
pass

run_failure "foreign namespace filtering" "$ncr" "$mixed#$foreign_namespace"
expect "$plain_stderr" "no supported configurations match system"
reject "$plain_stderr" "realise selected configuration closures"
reject "$plain_stderr" "required to build"
pass

run_failure "unknown explicit name" "$ncr" "$mixed" definitely-missing
expect "$plain_stderr" 'unknown configuration "definitely-missing"'
expect "$plain_stderr" "NixOS:"
expect "$plain_stderr" "nix-darwin:"
expect "$plain_stderr" "Home Manager:"
pass

run_failure "--home namespace conflict" "$ncr" --home "$mixed#$namespace"
expect "$plain_stderr" "--home conflicts with"
pass

run_failure "unsupported flake" "$ncr" "$root"
expect "$plain_stderr" "provides none of nixosConfigurations, darwinConfigurations, or homeConfigurations"
pass

run_failure "unknown option" "$ncr" --definitely-unknown
expect "$plain_stderr" 'unknown option "--definitely-unknown"'
pass

run_failure "missing flake reference" "$ncr"
expect "$plain_stderr" "missing flake reference"
pass

run_failure "flag order: --home first" "$ncr" --home --all-systems "$root"
expect "$plain_stderr" "does not provide homeConfigurations"
pass

run_failure "flag order: --all-systems first" "$ncr" --all-systems --home "$root"
expect "$plain_stderr" "does not provide homeConfigurations"
pass

scenario="fixture lock files"
if [[ $(nix hash file "${lock_files[@]}") != "$lock_hashes" ]]; then
	fail "fixture lock files changed during the suite"
fi

printf '\n%s✓ All integration tests passed on %s.%s\n' "$green" "$system" "$reset"
