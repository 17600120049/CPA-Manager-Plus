#!/usr/bin/env bash
# Local-only R1-1 source/OCI evidence. No Registry, GitHub Release or Channel writes.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
if [[ $# -ne 1 ]]; then
  echo "Usage: bash bin/release/build-embedded-candidate.sh ABSOLUTE_EMPTY_OUTPUT_DIR" >&2
  exit 2
fi
output="$1"
if [[ "$output" != /* ]]; then
  echo "Use an absolute empty output directory outside the repository." >&2
  exit 2
fi
if [[ -e "$output" ]]; then
  if [[ ! -d "$output" ]] || [[ -n "$(find "$output" -mindepth 1 -print -quit)" ]]; then
    echo "STOP: OCI output directory must be empty." >&2
    exit 2
  fi
fi
# Never allow the output to enter Docker build context on subsequent roles.
python3 - "$PWD" "$output" <<'PY'
import pathlib, sys
source = pathlib.Path(sys.argv[1]).resolve()
destination = pathlib.Path(sys.argv[2]).resolve()
if destination == source or source in destination.parents:
    raise SystemExit("STOP: OCI output must be outside the source tree")
if destination.exists() and not destination.is_dir():
    raise SystemExit("STOP: OCI output path is not a directory")
PY
if [[ -n "$(git status --porcelain)" ]]; then
  echo "STOP: build requires a clean source tree." >&2
  exit 1
fi
source_sha="$(git rev-parse --verify HEAD)"
event="local-r1-$(python3 -c 'import uuid; print(uuid.uuid4().hex)')"
docker buildx version >/dev/null

# Materialize one commit-addressed, disposable context. All three builds and
# the final verifier consume this same tree; the caller's working tree cannot
# change the bytes between Manager, Ingress and Runtime builds.
snapshot="$(mktemp -d "${TMPDIR:-/tmp}/cpamp-r1-context.XXXXXXXX")"
cleanup() {
  chmod -R u+w "$snapshot" >/dev/null 2>&1 || :
  rm -rf -- "$snapshot"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
python3 - "$PWD" "$snapshot" <<'PY'
import pathlib, sys
source = pathlib.Path(sys.argv[1]).resolve()
snapshot = pathlib.Path(sys.argv[2]).resolve()
if snapshot == source or source in snapshot.parents:
    raise SystemExit("STOP: candidate snapshot must be outside the source tree")
PY
git archive --format=tar "$source_sha" | tar -xf - -C "$snapshot"
chmod -R a-w "$snapshot"

# Stamped labels are candidate source claims, NOT signed producer provenance.
mkdir -p "$output"

for role in manager ingress runtime; do
  case "$role" in
    manager) file=Dockerfile.manager-server; target=; ;;
    ingress) file=Dockerfile.ingress; target=; ;;
    runtime) file=Dockerfile.runtime; target=runtime; ;;
  esac
  args=(
    --platform linux/amd64
    --label "org.opencontainers.image.revision=$source_sha"
    --label "org.opencontainers.image.source=https://github.com/seakee/CPA-Manager-Plus"
    --label "org.opencontainers.image.version=2.0.0-r1candidate"
    --label "io.cpamp.candidate.event=$event"
    --label "io.cpamp.candidate.role=$role"
    --provenance=false
    --sbom=false
    --output "type=oci,dest=$output/$role.oci.tar"
  )
  if [[ -n "$target" ]]; then args+=(--target "$target"); fi
  if [[ "$role" == manager ]]; then
    args+=(--build-arg "SOURCE_COMMIT=$source_sha" --build-arg VERSION=2.0.0-r1candidate)
  fi
  docker buildx build "${args[@]}" --file "$snapshot/$file" "$snapshot"
done

python3 "$snapshot/bin/release/verify-embedded-candidate.py" \
  --source-root "$snapshot" --source-commit "$source_sha" --build-event "$event" \
  --manager "$output/manager.oci.tar" \
  --ingress "$output/ingress.oci.tar" \
  --runtime "$output/runtime.oci.tar" \
  --out "$output/candidate-evidence.json"
echo "STOP: local OCI evidence is NOT a signed production receipt or release."
