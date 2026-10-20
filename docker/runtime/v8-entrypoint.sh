#!/bin/sh
set -eu

# Only the normal v8 target uses this bootstrap boundary. Private proofs and
# historical v7 lifecycle tests retain their own entrypoint and data contract.
root=/runtime
marker="$root/.cpamp-v8-bootstrap"
config="$root/gateway/config.yaml"
seed=/usr/local/share/cpamp/runtime/config.seed.yaml
identity="official-cpa-v8-bootstrap:$(sha256sum "$seed" | cut -d ' ' -f 1)"

refuse() {
  echo "Default CPA v8 refuses existing or unknown Runtime data: $*; preserve the volume and use its original image. No automatic migration or rollback." >&2
  exit 1
}

if [ -e "$root" ] || [ -L "$root" ]; then
  [ -d "$root" ] && [ ! -L "$root" ] || refuse 'invalid Runtime root'
  [ "$(stat -c '%u' "$root")" = 0 ] || refuse 'untrusted Runtime root owner'
fi

if [ -e "$marker" ] || [ -L "$marker" ]; then
  [ -f "$marker" ] && [ ! -L "$marker" ] || refuse 'invalid bootstrap identity'
  [ "$(stat -c '%u:%g:%a' "$marker")" = 0:0:444 ] || refuse 'untrusted bootstrap identity'
  [ "$(cat "$marker")" = "$identity" ] || refuse 'different bootstrap identity'
  [ -d "$root/gateway" ] && [ ! -L "$root/gateway" ] || refuse 'invalid Gateway directory'
  [ -f "$config" ] && [ ! -L "$config" ] || refuse 'missing or invalid bootstrap config'
  cmp -s "$seed" "$config" || refuse 'modified or incompatible bootstrap config'
else
  # Check before the shared entrypoint creates/chowns state or transport keys.
  # Even a v8-looking config without our bootstrap identity is not adopted.
  if [ -d "$root" ] && [ -n "$(find "$root" -mindepth 1 -maxdepth 1 -print -quit)" ]; then
    refuse 'nonempty volume without v8 bootstrap identity'
  fi
  mkdir -p "$root"
  umask 077
  printf '%s\n' "$identity" > "$marker"
  chmod 0444 "$marker"
fi

exec /usr/local/bin/cpamp-runtime-entrypoint "$@"
