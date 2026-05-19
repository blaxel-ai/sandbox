#!/bin/sh
set -eu

APP_ROOT="${APP_RUNNER_WORKDIR:-/workspace}"
SOURCE_URI="${APP_RUNNER_SOURCE_URI:-}"
SOURCE_TYPE="${APP_RUNNER_SOURCE_TYPE:-auto}"
SOURCE_REF="${APP_RUNNER_SOURCE_REF:-}"
CONTEXT_PATH="${APP_RUNNER_CONTEXT_PATH:-}"
PRE_START_COMMAND="${APP_RUNNER_PRE_START_COMMAND:-}"
START_COMMAND="${APP_RUNNER_START_COMMAND:-}"
SETUP_SCRIPT_B64="${APP_RUNNER_SETUP_SCRIPT_B64:-}"

case "$APP_ROOT" in
  ""|"/"|"/bin"|"/etc"|"/usr"|"/var"|"/tmp"|"/blaxel")
    echo "Unsafe APP_RUNNER_WORKDIR: $APP_ROOT" >&2
    exit 1
    ;;
esac

if [ -z "$SOURCE_URI" ]; then
  echo "APP_RUNNER_SOURCE_URI is required" >&2
  exit 1
fi

if [ -z "$START_COMMAND" ]; then
  echo "APP_RUNNER_START_COMMAND is required" >&2
  exit 1
fi

rm -rf "$APP_ROOT"
mkdir -p "$APP_ROOT"
SOURCE_DIR="$APP_ROOT/source"

run_setup_script() {
  if [ -z "$SETUP_SCRIPT_B64" ]; then
    return 0
  fi

  setup_script=$(mktemp)
  if ! printf "%s" "$SETUP_SCRIPT_B64" | base64 -d > "$setup_script"; then
    rm -f "$setup_script"
    echo "APP_RUNNER_SETUP_SCRIPT_B64 is not valid base64" >&2
    exit 1
  fi

  chmod +x "$setup_script"
  echo "Running app runner setup script..."
  if ! (cd "$APP_ROOT" && /bin/sh "$setup_script"); then
    rm -f "$setup_script"
    echo "App runner setup script failed" >&2
    exit 1
  fi
  rm -f "$setup_script"
}

download_https() {
  url="$1"
  dest="$2"
  python3 - "$url" "$dest" <<'PY'
import sys
import urllib.request

url, dest = sys.argv[1], sys.argv[2]
with urllib.request.urlopen(url) as response, open(dest, "wb") as out:
    out.write(response.read())
PY
}

extract_or_copy() {
  file="$1"
  target="$2"
  mkdir -p "$target"
  case "$file" in
    *.zip)
      python3 - "$file" "$target" <<'PY'
import os
import sys
import zipfile

target = os.path.realpath(sys.argv[2])
with zipfile.ZipFile(sys.argv[1]) as archive:
    for member in archive.infolist():
        dest = os.path.realpath(os.path.join(target, member.filename))
        if dest != target and not dest.startswith(target + os.sep):
            raise SystemExit(f"Unsafe archive path: {member.filename}")
    archive.extractall(target)
PY
      ;;
    *.tar|*.tar.gz|*.tgz|*.tar.bz2|*.tar.xz)
      python3 - "$file" "$target" <<'PY'
import os
import sys
import tarfile

target = os.path.realpath(sys.argv[2])
with tarfile.open(sys.argv[1]) as archive:
    for member in archive.getmembers():
        dest = os.path.realpath(os.path.join(target, member.name))
        if dest != target and not dest.startswith(target + os.sep):
            raise SystemExit(f"Unsafe archive path: {member.name}")
    archive.extractall(target, filter="data")
PY
      ;;
    *)
      cp "$file" "$target/$(basename "$file")"
      ;;
  esac
}

clone_git() {
  uri="$1"
  target="$2"
  if [ -n "$SOURCE_REF" ]; then
    if git clone --depth 1 --branch "$SOURCE_REF" "$uri" "$target"; then
      return 0
    fi
  fi
  git clone "$uri" "$target"
  if [ -n "$SOURCE_REF" ]; then
    (cd "$target" && git checkout "$SOURCE_REF")
  fi
}

copy_with_rclone() {
  kind="$1"
  uri="$2"
  target="$3"
  remote_path=$(printf "%s" "$uri" | sed "s|^[^:]*://||")
  case "$kind" in
    s3)
      provider="${RCLONE_S3_PROVIDER:-AWS}"
      region="${RCLONE_S3_REGION:-${AWS_DEFAULT_REGION:-us-east-1}}"
      rclone copy ":s3,provider=$provider,region=$region,env_auth=true:$remote_path" "$target"
      ;;
    gcs)
      rclone copy ":gcs:$remote_path" "$target"
      ;;
    azure|azure_blob)
      rclone copy ":azureblob:$remote_path" "$target"
      ;;
    *)
      echo "Unsupported rclone source type: $kind" >&2
      exit 1
      ;;
  esac
}

run_setup_script

if [ "$SOURCE_TYPE" = "auto" ]; then
  case "$SOURCE_URI" in
    *.git|*github.com*|*gitlab.com*) SOURCE_TYPE="git" ;;
    s3://*) SOURCE_TYPE="s3" ;;
    gs://*|gcs://*) SOURCE_TYPE="gcs" ;;
    azure://*|azureblob://*|azure_blob://*) SOURCE_TYPE="azure_blob" ;;
    https://*) SOURCE_TYPE="https" ;;
    *)
      echo "Unable to infer source type for $SOURCE_URI" >&2
      exit 1
      ;;
  esac
fi

case "$SOURCE_TYPE" in
  git)
    clone_git "$SOURCE_URI" "$SOURCE_DIR"
    ;;
  https)
    download_name=$(basename "${SOURCE_URI%%\?*}")
    if [ -z "$download_name" ]; then
      download_name="source-download"
    fi
    tmp_file="$APP_ROOT/$download_name"
    download_https "$SOURCE_URI" "$tmp_file"
    extract_or_copy "$tmp_file" "$SOURCE_DIR"
    ;;
  s3|gcs|azure|azure_blob)
    mkdir -p "$SOURCE_DIR"
    copy_with_rclone "$SOURCE_TYPE" "$SOURCE_URI" "$SOURCE_DIR"
    ;;
  *)
    echo "Unsupported source type: $SOURCE_TYPE" >&2
    exit 1
    ;;
esac

RUN_DIR="$SOURCE_DIR"
if [ -n "$CONTEXT_PATH" ]; then
  RUN_DIR="$SOURCE_DIR/$CONTEXT_PATH"
fi

if [ ! -d "$RUN_DIR" ]; then
  echo "App context path does not exist: $RUN_DIR" >&2
  exit 1
fi

cd "$RUN_DIR"

if [ -n "$PRE_START_COMMAND" ]; then
  /bin/sh -lc "$PRE_START_COMMAND"
fi

exec /bin/sh -lc "$START_COMMAND"
