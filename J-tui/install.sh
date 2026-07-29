#!/bin/sh
set -eu

repository="z-chenhao/J"
install_dir="${J_TUI_INSTALL_DIR:-${HOME}/.local/bin}"
release_url="https://github.com/${repository}/releases/latest/download"

for command_name in curl tar awk install; do
	if ! command -v "$command_name" >/dev/null 2>&1; then
		printf >&2 'j-tui installer requires %s\n' "$command_name"
		exit 1
	fi
done

case "$(uname -s)" in
	Darwin) operating_system="darwin" ;;
	Linux) operating_system="linux" ;;
	*)
		printf >&2 'j-tui does not publish binaries for %s\n' "$(uname -s)"
		exit 1
		;;
esac

case "$(uname -m)" in
	x86_64 | amd64) architecture="amd64" ;;
	arm64 | aarch64) architecture="arm64" ;;
	*)
		printf >&2 'j-tui does not publish binaries for architecture %s\n' "$(uname -m)"
		exit 1
		;;
esac

archive="j-tui_${operating_system}_${architecture}.tar.gz"
temporary_dir="$(mktemp -d "${TMPDIR:-/tmp}/j-tui-install.XXXXXX")"
trap 'rm -rf "$temporary_dir"' EXIT HUP INT TERM

curl --proto '=https' --tlsv1.2 --fail --location --silent --show-error \
	"${release_url}/${archive}" \
	--output "${temporary_dir}/${archive}"
curl --proto '=https' --tlsv1.2 --fail --location --silent --show-error \
	"${release_url}/checksums.txt" \
	--output "${temporary_dir}/checksums.txt"

expected_checksum="$(
	awk -v archive="$archive" '$2 == archive { print $1 }' \
		"${temporary_dir}/checksums.txt"
)"
if [ -z "$expected_checksum" ]; then
	printf >&2 'release checksum does not contain %s\n' "$archive"
	exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
	actual_checksum="$(sha256sum "${temporary_dir}/${archive}" | awk '{ print $1 }')"
elif command -v shasum >/dev/null 2>&1; then
	actual_checksum="$(shasum -a 256 "${temporary_dir}/${archive}" | awk '{ print $1 }')"
else
	printf >&2 'j-tui installer requires sha256sum or shasum\n'
	exit 1
fi
if [ "$actual_checksum" != "$expected_checksum" ]; then
	printf >&2 'checksum verification failed for %s\n' "$archive"
	exit 1
fi

tar -xzf "${temporary_dir}/${archive}" -C "$temporary_dir"
mkdir -p "$install_dir"
install -m 0755 "${temporary_dir}/j-tui" "${install_dir}/j-tui"
install -m 0755 "${temporary_dir}/j" "${install_dir}/j"

printf 'Installed j-tui to %s/j-tui\n' "$install_dir"
printf 'Installed j package manager to %s/j\n' "$install_dir"
case ":${PATH}:" in
	*":${install_dir}:"*) ;;
	*)
		printf 'Add %s to PATH, then run: j-tui --init-config\n' "$install_dir"
		;;
esac
