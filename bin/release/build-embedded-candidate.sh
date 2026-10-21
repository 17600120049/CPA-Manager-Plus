#!/usr/bin/env bash
# Local-only R1-1 source/OCI evidence. No Registry, GitHub Release or Channel writes.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
if [[ $# -ne 1 ]]; then
  echo "Usage: bash bin/release/build-embedded-candidate.sh ABSOLUTE_EMPTY_OUTPUT_DIR" >&2
  exit 2
fi
output="$1"
if [[ "$output" != /* ]] || [[ -e "$output" && -n "$(ls -A "$output")" ]]; then
  echo "Use an absolute empty output directory outside the repository." >&2
  exit 2
fi
if [[ -n "$(git status --porcelain)" ]]; then
  echo "STOP: build requires a clean source tree." >&2
  exit 1
fi
source_sha="$(git rev-parse --verify HEAD)"
event="local-r1-$(python3 -c 'import uuid; print(uuid.uuid4().hex)')"
docker buildx version >/dev/null
mkdir -p "$output"

for role in manager ingress runtime; do
  case "$role" in
    manager) file=Dockerfile.manager-server; target=; ;;
    ingress) file=Dockerfile.ingress; target=; ;;
    runtime) file=Dockerfile.runtime; target=runtime; ;;
  esac
  args=(
    --platform linux/amd64
    --file "$file"
    --label "org.opencontainers.image.revision=$source_sha"
    --label "org.opencontainers.image.source=https://github.com/seakee/CPA-Manager-Plus"
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
  docker buildx build "${args[@]}" .
done

python3 bin/release/verify-embedded-candidate.py \
  --source-root . --source-commit "$source_sha" --build-event "$event" \
  --manager "$output/manager.oci.tar" \
  --ingress "$output/ingress.oci.tar" \
  --runtime "$output/runtime.oci.tar" \
  --out "$output/candidate-evidence.json"
echo "STOP: local OCI evidence is NOT a signed production receipt or release."
