#!@shell@

"@nh@" "$@" || exit $?
[ -z "${NCR_SKIP_WARM:-}" ] || exit 0

command=
skip_value=false
for argument in "$@"; do
	if $skip_value; then
		skip_value=false
		continue
	fi
	case "$argument" in
		-h | --help | -n | --dry | --no-gc)
			exit 0
			;;
		-e | --elevation-strategy)
			skip_value=true
			;;
		--elevation-strategy=* | -*)
			;;
		*)
			if [ -z "$command" ]; then
				command=$argument
			fi
			;;
	esac
done

[ "$command" = clean ] || exit 0
exec "@ncr@" --warm-only
