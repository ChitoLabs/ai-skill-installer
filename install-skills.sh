#!/usr/bin/env bash

set -euo pipefail

readonly DEFAULT_REPOSITORY_URL="https://github.com/ChitoLabs/ai-skill-pack"
readonly DEFAULT_BRANCH="main"
readonly DEFAULT_MIN_SKILL_COUNT="100"
readonly SCRIPT_DIR="$(realpath "$(dirname "${BASH_SOURCE[0]}")")"
readonly SCRIPT_PATH="$SCRIPT_DIR/${BASH_SOURCE[0]##*/}"

REPOSITORY_URL="${SKILL_PACK_REPOSITORY_URL:-$DEFAULT_REPOSITORY_URL}"
BRANCH="${SKILL_PACK_BRANCH:-$DEFAULT_BRANCH}"
MIN_SKILL_COUNT="${SKILL_PACK_MIN_SKILL_COUNT:-$DEFAULT_MIN_SKILL_COUNT}"
OPENCODE_TARGET_INPUT=""
CLAUDE_TARGET_INPUT=""
AGY_TARGET_INPUT=""
OPENCODE_TARGET_ORIGIN=""
CLAUDE_TARGET_ORIGIN=""
AGY_TARGET_ORIGIN=""
BACKUP_ROOT_INPUT="${SKILL_BACKUP_DIR:-$SCRIPT_DIR/BKOld}"
LOCK_PATH_INPUT="${SKILL_INSTALLER_LOCK_FILE:-$SCRIPT_DIR/.install-skills.lock}"
STATE_PATH_INPUT="${SKILL_INSTALLER_STATE_FILE:-$SCRIPT_DIR/.installed-skills.state}"
TEST_FAILPOINT="${SKILL_INSTALLER_TEST_FAILPOINT:-}"

ACTION=""
RESTORE_ID=""
DRY_RUN=0
FORCE=0
STAGING_DIR=""
TEMPORARY_ROOT=""
SNAPSHOT_TEMP=""
SOURCE_SKILLS_DIR=""
SOURCE_COMMIT="unknown"
SOURCE_SKILL_COUNT=0
INSTALLED_COMMIT=""
INSTALLED_REPOSITORY=""
INSTALLED_BRANCH=""
INSTALLED_SKILL_COUNT=""
INSTALLED_UTC=""
REMOTE_COMMIT="unknown"
LAST_SKILL_COUNT=0
LAST_SNAPSHOT_ID=""
LOCK_FD=""

DESIRED_OPENCODE_PREPARED=""
DESIRED_CLAUDE_PREPARED=""
DESIRED_AGY_PREPARED=""
DESIRED_OPENCODE_SOURCE=""
DESIRED_CLAUDE_SOURCE=""
DESIRED_AGY_SOURCE=""
ROLLBACK_OPENCODE_SOURCE=""
ROLLBACK_CLAUDE_SOURCE=""
ROLLBACK_AGY_SOURCE=""
DESIRED_OPENCODE_EXISTED=0
DESIRED_CLAUDE_EXISTED=0
DESIRED_AGY_EXISTED=0
DESIRED_OPENCODE_CAPTURED=0
DESIRED_CLAUDE_CAPTURED=0
DESIRED_AGY_CAPTURED=0
ROLLBACK_OPENCODE_EXISTED=0
ROLLBACK_CLAUDE_EXISTED=0
ROLLBACK_AGY_EXISTED=0

TRANSACTION_STARTED=0
COMMITTED=0
OPENCODE_QUARANTINE=""
CLAUDE_QUARANTINE=""
AGY_QUARANTINE=""
OPENCODE_QUARANTINE_READY=0
CLAUDE_QUARANTINE_READY=0
AGY_QUARANTINE_READY=0
OPENCODE_TRANSACTION_PARTICIPATED=0
CLAUDE_TRANSACTION_PARTICIPATED=0
AGY_TRANSACTION_PARTICIPATED=0

META_FORMAT_VERSION=""
META_ID=""
META_CREATED_UTC=""
META_REASON=""
META_SOURCE_COMMIT=""
META_OPENCODE_CAPTURED=0
META_CLAUDE_CAPTURED=0
META_OPENCODE_EXISTED=""
META_CLAUDE_EXISTED=""
META_AGY_CAPTURED=0
META_AGY_EXISTED=""
META_OPENCODE_SKILL_COUNT=""
META_CLAUDE_SKILL_COUNT=""
META_AGY_SKILL_COUNT=""

COLOR_BOLD=""
COLOR_BLUE=""
COLOR_GREEN=""
COLOR_YELLOW=""
COLOR_RED=""
COLOR_RESET=""

configure_colors() {
  if [[ -t 1 && -z "${NO_COLOR+x}" && "${TERM:-dumb}" != "dumb" ]]; then
    COLOR_BOLD=$'\033[1m'
    COLOR_BLUE=$'\033[34m'
    COLOR_GREEN=$'\033[32m'
    COLOR_YELLOW=$'\033[33m'
    COLOR_RED=$'\033[31m'
    COLOR_RESET=$'\033[0m'
  fi
}

log() {
  printf '%s\n' "$*"
}

info() {
  printf '%s%s%s\n' "$COLOR_BLUE" "$*" "$COLOR_RESET"
}

success() {
  printf '%s%s%s\n' "$COLOR_GREEN" "$*" "$COLOR_RESET"
}

warn() {
  printf '%sWARNING: %s%s\n' "$COLOR_YELLOW" "$*" "$COLOR_RESET" >&2
}

post_commit_success() {
  success "$@" || true
  return 0
}

post_commit_warn() {
  warn "$@" || true
  return 0
}

fail() {
  printf '%sERROR: %s%s\n' "$COLOR_RED" "$*" "$COLOR_RESET" >&2
  exit 1
}

resolve_skill_targets() {
  : "${HOME:?HOME must be set}"

  if [[ -n "${OPENCODE_SKILLS_DIR:-}" ]]; then
    OPENCODE_TARGET_INPUT="$OPENCODE_SKILLS_DIR"
    OPENCODE_TARGET_ORIGIN="OPENCODE_SKILLS_DIR"
  elif [[ -n "${OPENCODE_CONFIG_DIR:-}" ]]; then
    OPENCODE_TARGET_INPUT="$OPENCODE_CONFIG_DIR/skills"
    OPENCODE_TARGET_ORIGIN="OPENCODE_CONFIG_DIR"
  elif [[ -n "${XDG_CONFIG_HOME:-}" ]]; then
    OPENCODE_TARGET_INPUT="$XDG_CONFIG_HOME/opencode/skills"
    OPENCODE_TARGET_ORIGIN="XDG_CONFIG_HOME"
  else
    OPENCODE_TARGET_INPUT="$HOME/.config/opencode/skills"
    OPENCODE_TARGET_ORIGIN="HOME default"
  fi

  if [[ -n "${CLAUDE_SKILLS_DIR:-}" ]]; then
    CLAUDE_TARGET_INPUT="$CLAUDE_SKILLS_DIR"
    CLAUDE_TARGET_ORIGIN="CLAUDE_SKILLS_DIR"
  elif [[ -n "${CLAUDE_CONFIG_DIR:-}" ]]; then
    CLAUDE_TARGET_INPUT="$CLAUDE_CONFIG_DIR/skills"
    CLAUDE_TARGET_ORIGIN="CLAUDE_CONFIG_DIR"
  else
    CLAUDE_TARGET_INPUT="$HOME/.claude/skills"
    CLAUDE_TARGET_ORIGIN="HOME default"
  fi

  if [[ -n "${AGY_SKILLS_DIR:-}" ]]; then
    AGY_TARGET_INPUT="$AGY_SKILLS_DIR"
    AGY_TARGET_ORIGIN="AGY_SKILLS_DIR"
  else
    AGY_TARGET_INPUT="$HOME/.gemini/config/skills"
    AGY_TARGET_ORIGIN="HOME default"
  fi
}

usage() {
  cat <<'EOF'
Usage: install-skills.sh COMMAND [ARGUMENT]

Commands:
  install [--force]      Install/update all three skill targets (noninteractive).
                         Without --force, a repeated install of the same source
                         commit is a no-op that reports "already up to date".
  dry-run, --dry-run   Clone and validate without changing targets or backups.
  status, check         Compare the installed skill-pack version against the
                         remote source and summarize new/modified/removed skills.
  list                 List managed backup snapshots.
  restore [BACKUP_ID]  Restore a snapshot; ID is required without a TTY.
  menu                 Open the interactive terminal menu.
  help                 Show this help.

With no arguments, a TTY opens the menu. Without a TTY, no action is taken and
an explicit command is required.
EOF
}

parse_arguments() {
  if (($# == 0)); then
    if [[ -t 0 && -t 1 ]]; then
      ACTION="menu"
      return 0
    fi
    usage
    return 2
  fi

  case "$1" in
    install)
      if (($# == 1)); then
        ACTION="install"
      elif (($# == 2)) && [[ "$2" == "--force" ]]; then
        ACTION="install"
        FORCE=1
      else
        fail "install accepts only an optional --force argument"
      fi
      ;;
    dry-run|--dry-run)
      (($# == 1)) || fail "$1 does not accept arguments"
      ACTION="install"
      DRY_RUN=1
      ;;
    status|check)
      (($# == 1)) || fail "$1 does not accept arguments"
      ACTION="status"
      ;;
    list)
      (($# == 1)) || fail "list does not accept arguments"
      ACTION="list"
      ;;
    restore)
      (($# <= 2)) || fail "restore accepts at most one BACKUP_ID"
      ACTION="restore"
      RESTORE_ID="${2:-}"
      ;;
    menu)
      (($# == 1)) || fail "menu does not accept arguments"
      ACTION="menu"
      ;;
    help|--help|-h)
      (($# == 1)) || fail "$1 does not accept arguments"
      ACTION="help"
      ;;
    *)
      fail "Unknown command: $1"
      ;;
  esac
}

check_dependencies() {
  local command_name
  local -a required_commands=(git mktemp cp mv rm mkdir date realpath diff find grep cut sort head)

  for command_name in "${required_commands[@]}"; do
    command -v "$command_name" >/dev/null 2>&1 || fail "Required command not found: $command_name"
  done
}

expand_home() {
  local path="$1"

  case "$path" in
    "~") printf '%s\n' "$HOME" ;;
    "~/"*) printf '%s/%s\n' "$HOME" "${path#\~/}" ;;
    *) printf '%s\n' "$path" ;;
  esac
}

normalize_path() {
  local input="$1"
  local label="$2"
  local path

  path="$(expand_home "$input")"
  [[ "$path" == /* ]] || fail "$label must be absolute: $input"
  while [[ "$path" != "/" && "$path" == */ ]]; do
    path="${path%/}"
  done
  [[ ! -L "$path" ]] || fail "$label cannot be a symbolic link: $path"
  path="$(realpath -m -- "$path")"
  [[ "$path" != "/" ]] || fail "$label cannot be the filesystem root"
  printf '%s\n' "$path"
}

paths_overlap() {
  local first="$1"
  local second="$2"

  [[ "$first" == "$second" || "$first" == "$second/"* || "$second" == "$first/"* ]]
}

path_is_equal_or_nested() {
  local candidate="$1"
  local root="$2"

  [[ "$candidate" == "$root" || "$candidate" == "$root/"* ]]
}

assert_no_protected_codegraph() {
  local root="$1"
  local match=""

  [[ "/$root/" != *"/.codegraph/"* ]] || fail "Path cannot be inside a .codegraph directory: $root"
  [[ -d "$root" ]] || return 0

  match="$(find "$root" -type d -name .codegraph -print -quit)"
  [[ -z "$match" ]] || fail "Path contains a protected .codegraph directory: $match"
}

assert_no_symlinks() {
  local root="$1"
  local match

  match="$(find "$root" -type l -print -quit)"
  [[ -z "$match" ]] || fail "Incoming skill tree contains a symbolic link: $match"
}

assert_safe_target() {
  local target="$1"
  local protected_path
  local -a protected_paths=(
    "$(realpath -m -- "$HOME/.config/opencode/commands")"
    "$(realpath -m -- "$HOME/.engram")"
    "$(realpath -m -- "$HOME/.agents/skills")"
    "$(realpath -m -- "$HOME/.gemini/antigravity-cli/skills")"
  )

  [[ "${target##*/}" == "skills" ]] || fail "Target must end in /skills: $target"
  [[ ! -L "$target" ]] || fail "Refusing to replace a symbolic-link target: $target"
  if [[ -e "$target" && ! -d "$target" ]]; then
    fail "Existing target is not a directory: $target"
  fi
  assert_no_protected_codegraph "$target"

  for protected_path in "${protected_paths[@]}"; do
    paths_overlap "$target" "$protected_path" && fail "Target overlaps protected path: $protected_path"
  done
  return 0
}

assert_safe_backup_root() {
  local protected_path
  local -a protected_paths=(
    "$OPENCODE_TARGET"
    "$CLAUDE_TARGET"
    "$AGY_TARGET"
    "$(realpath -m -- "$HOME/.config/opencode/commands")"
    "$(realpath -m -- "$HOME/.engram")"
    "$(realpath -m -- "$HOME/.agents/skills")"
    "$(realpath -m -- "$HOME/.gemini/antigravity-cli/skills")"
  )

  [[ ! -L "$BACKUP_ROOT" ]] || fail "Backup root cannot be a symbolic link: $BACKUP_ROOT"
  if [[ -e "$BACKUP_ROOT" && ! -d "$BACKUP_ROOT" ]]; then
    fail "Backup root is not a directory: $BACKUP_ROOT"
  fi
  assert_no_protected_codegraph "$BACKUP_ROOT"

  for protected_path in "${protected_paths[@]}"; do
    paths_overlap "$BACKUP_ROOT" "$protected_path" && fail "Backup root overlaps protected path: $protected_path"
  done
  return 0
}

assert_safe_lock_path() {
  local protected_path
  local -a protected_paths=(
    "$OPENCODE_TARGET"
    "$CLAUDE_TARGET"
    "$AGY_TARGET"
    "$BACKUP_ROOT"
    "$(realpath -m -- "$HOME/.config/opencode/commands")"
    "$(realpath -m -- "$HOME/.engram")"
    "$(realpath -m -- "$HOME/.agents/skills")"
    "$(realpath -m -- "$HOME/.gemini/antigravity-cli/skills")"
  )

  assert_no_protected_codegraph "$LOCK_PATH"
  if [[ -e "$LOCK_PATH" && ! -f "$LOCK_PATH" ]]; then
    fail "Installer lock path is not a regular file: $LOCK_PATH"
  fi
  for protected_path in "${protected_paths[@]}"; do
    paths_overlap "$LOCK_PATH" "$protected_path" && fail "Installer lock overlaps protected path: $protected_path"
  done
  return 0
}

assert_safe_state_path() {
  local protected_path
  local -a protected_paths=(
    "$OPENCODE_TARGET"
    "$CLAUDE_TARGET"
    "$AGY_TARGET"
    "$BACKUP_ROOT"
    "$(realpath -m -- "$HOME/.config/opencode/commands")"
    "$(realpath -m -- "$HOME/.engram")"
    "$(realpath -m -- "$HOME/.agents/skills")"
    "$(realpath -m -- "$HOME/.gemini/antigravity-cli/skills")"
  )

  assert_no_protected_codegraph "$STATE_PATH"
  if [[ -e "$STATE_PATH" && ! -f "$STATE_PATH" ]]; then
    fail "Installer state path is not a regular file: $STATE_PATH"
  fi
  for protected_path in "${protected_paths[@]}"; do
    paths_overlap "$STATE_PATH" "$protected_path" && fail "Installer state overlaps protected path: $protected_path"
  done
  return 0
}

validate_configuration() {
  [[ -n "$REPOSITORY_URL" ]] || fail "SKILL_PACK_REPOSITORY_URL cannot be empty"
  [[ "$REPOSITORY_URL" != -* ]] || fail "Repository URL cannot begin with a dash"
  [[ -n "$BRANCH" ]] || fail "SKILL_PACK_BRANCH cannot be empty"
  [[ "$MIN_SKILL_COUNT" =~ ^[1-9][0-9]*$ ]] || fail "SKILL_PACK_MIN_SKILL_COUNT must be a positive integer"

  resolve_skill_targets
  assert_no_protected_codegraph "$(expand_home "$LOCK_PATH_INPUT")"
  assert_no_protected_codegraph "$(expand_home "$STATE_PATH_INPUT")"
  OPENCODE_TARGET="$(normalize_path "$OPENCODE_TARGET_INPUT" "OpenCode target")"
  CLAUDE_TARGET="$(normalize_path "$CLAUDE_TARGET_INPUT" "Claude target")"
  AGY_TARGET="$(normalize_path "$AGY_TARGET_INPUT" "AGY target")"
  BACKUP_ROOT="$(normalize_path "$BACKUP_ROOT_INPUT" "Backup root")"
  LOCK_PATH="$(normalize_path "$LOCK_PATH_INPUT" "Installer lock path")"
  STATE_PATH="$(normalize_path "$STATE_PATH_INPUT" "Installer state path")"

  assert_safe_target "$OPENCODE_TARGET"
  assert_safe_target "$CLAUDE_TARGET"
  assert_safe_target "$AGY_TARGET"
  paths_overlap "$OPENCODE_TARGET" "$CLAUDE_TARGET" && fail "All skill targets must be distinct and non-overlapping"
  paths_overlap "$OPENCODE_TARGET" "$AGY_TARGET" && fail "All skill targets must be distinct and non-overlapping"
  paths_overlap "$CLAUDE_TARGET" "$AGY_TARGET" && fail "All skill targets must be distinct and non-overlapping"
  assert_safe_backup_root
  assert_safe_lock_path
  assert_safe_state_path

  case "$TEST_FAILPOINT" in
    ""|after-opencode-install|after-claude-install|after-agy-install|after-opencode-restore|after-claude-restore|after-agy-restore|quarantine-fidelity-install|quarantine-fidelity-restore|quarantine-cleanup|post-commit-report) ;;
    *) fail "Unsupported SKILL_INSTALLER_TEST_FAILPOINT: $TEST_FAILPOINT" ;;
  esac
}

acquire_installer_lock() {
  local lock_parent="${LOCK_PATH%/*}"

  command -v flock >/dev/null 2>&1 || fail "Required command not found for install/restore: flock"
  mkdir -p -- "$lock_parent"
  [[ ! -L "$LOCK_PATH" ]] || fail "Installer lock path became a symbolic link: $LOCK_PATH"
  exec {LOCK_FD}>"$LOCK_PATH" || fail "Could not open installer lock: $LOCK_PATH"
  flock --exclusive --nonblock "$LOCK_FD" || \
    fail "Another install or restore operation is already running (lock: $LOCK_PATH)"
}

is_safe_backup_id() {
  [[ "$1" =~ ^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$ ]]
}

require_safe_backup_id() {
  is_safe_backup_id "$1" || fail "Unsafe backup ID: $1"
}

git_without_user_configuration() {
  GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null \
    git -c core.hooksPath=/dev/null "$@"
}

validate_temporary_root() {
  local input="${TMPDIR:-/tmp}"
  local temporary_root
  local protected_path
  local -a protected_paths=(
    "$OPENCODE_TARGET"
    "$CLAUDE_TARGET"
    "$AGY_TARGET"
    "$BACKUP_ROOT"
    "$(realpath -m -- "$HOME/.config/opencode/commands")"
    "$(realpath -m -- "$HOME/.engram")"
    "$(realpath -m -- "$HOME/.agents/skills")"
    "$(realpath -m -- "$HOME/.gemini/antigravity-cli/skills")"
  )

  temporary_root="$(expand_home "$input")"
  [[ "$temporary_root" == /* ]] || fail "Temporary root must be absolute: $input"
  [[ -d "$temporary_root" ]] || fail "Temporary directory does not exist: $temporary_root"
  temporary_root="$(realpath -- "$temporary_root")"
  [[ "$temporary_root" != "/" ]] || fail "Temporary root cannot be the filesystem root"
  [[ "/$temporary_root/" != *"/.codegraph/"* ]] || \
    fail "Temporary root cannot be inside a .codegraph directory: $temporary_root"

  for protected_path in "${protected_paths[@]}"; do
    path_is_equal_or_nested "$temporary_root" "$protected_path" && \
      fail "Temporary root overlaps managed or protected destination: $protected_path"
  done
  TEMPORARY_ROOT="$temporary_root"
}

clone_repository() {
  validate_temporary_root
  STAGING_DIR="$(mktemp -d "$TEMPORARY_ROOT/chito-skill-installer.XXXXXX")"
  info "Cloning $REPOSITORY_URL (branch: $BRANCH) into temporary staging..."
  git_without_user_configuration clone --quiet --depth 1 --single-branch \
    --branch "$BRANCH" "$REPOSITORY_URL" "$STAGING_DIR/repository"

  SOURCE_SKILLS_DIR="$STAGING_DIR/repository/skills"
  SOURCE_COMMIT="$(git_without_user_configuration -C "$STAGING_DIR/repository" rev-parse HEAD)"
  [[ "$SOURCE_COMMIT" =~ ^[0-9a-f]{40}$ ]] || fail "Unexpected source commit: $SOURCE_COMMIT"
}

load_installed_state() {
  local line key value
  local -A seen=()

  INSTALLED_COMMIT=""
  INSTALLED_REPOSITORY=""
  INSTALLED_BRANCH=""
  INSTALLED_SKILL_COUNT=""
  INSTALLED_UTC=""

  [[ -f "$STATE_PATH" && ! -L "$STATE_PATH" ]] || return 1
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ "$line" == *=* ]] || return 1
    key="${line%%=*}"
    value="${line#*=}"
    case "$key" in
      repository_url|branch|source_commit|skill_count|installed_utc) ;;
      *) return 1 ;;
    esac
    [[ -z "${seen[$key]+x}" ]] || return 1
    seen["$key"]=1
    case "$key" in
      repository_url) INSTALLED_REPOSITORY="$value" ;;
      branch) INSTALLED_BRANCH="$value" ;;
      source_commit) INSTALLED_COMMIT="$value" ;;
      skill_count) INSTALLED_SKILL_COUNT="$value" ;;
      installed_utc) INSTALLED_UTC="$value" ;;
    esac
  done <"$STATE_PATH"

  [[ -n "${seen[repository_url]+x}" && -n "${seen[branch]+x}" && -n "${seen[source_commit]+x}" ]] || return 1
  [[ "$INSTALLED_COMMIT" =~ ^[0-9a-f]{40}$ ]] || return 1
  [[ -n "$INSTALLED_REPOSITORY" && -n "$INSTALLED_BRANCH" ]] || return 1
  return 0
}

find_latest_install_commit() {
  local snapshot_path snapshot_id latest_id=""
  local commit=""

  [[ -d "$BACKUP_ROOT" ]] || return 1
  shopt -s nullglob
  for snapshot_path in "$BACKUP_ROOT"/*; do
    [[ -d "$snapshot_path" && ! -L "$snapshot_path" ]] || continue
    snapshot_id="${snapshot_path##*/}"
    is_safe_backup_id "$snapshot_id" || continue
    # Prefer the newest install snapshot; IDs sort chronologically.
    if [[ ! -f "$snapshot_path/metadata" || -L "$snapshot_path/metadata" ]]; then
      continue
    fi
    if grep -q '^reason=install$' "$snapshot_path/metadata" 2>/dev/null; then
      if [[ -z "$latest_id" || "$snapshot_id" > "$latest_id" ]]; then
        latest_id="$snapshot_id"
      fi
    fi
  done
  shopt -u nullglob
  [[ -n "$latest_id" ]] || return 1
  commit="$(grep '^source_commit=' "$BACKUP_ROOT/$latest_id/metadata" 2>/dev/null | cut -d= -f2-)"
  [[ "$commit" =~ ^[0-9a-f]{40}$ ]] || return 1
  printf '%s\n' "$commit"
}

resolve_installed_commit() {
  if load_installed_state; then
    return 0
  fi
  INSTALLED_COMMIT=""
  INSTALLED_REPOSITORY=""
  INSTALLED_BRANCH=""
  INSTALLED_SKILL_COUNT=""
  INSTALLED_UTC=""
  if INSTALLED_COMMIT="$(find_latest_install_commit)"; then
    return 0
  fi
  INSTALLED_COMMIT=""
  return 1
}

get_remote_head() {
  local remote_output

  remote_output="$(git_without_user_configuration ls-remote "$REPOSITORY_URL" "$BRANCH" 2>/dev/null)" || \
    fail "Could not reach source repository to check for updates: $REPOSITORY_URL (branch: $BRANCH)"
  REMOTE_COMMIT="${remote_output%%[[:space:]]*}"
  [[ "$REMOTE_COMMIT" =~ ^[0-9a-f]{40}$ ]] || \
    fail "Could not resolve remote HEAD for branch '$BRANCH' in $REPOSITORY_URL"
}

list_skill_names() {
  local root="$1"
  local manifest
  local -a names=()

  [[ -d "$root" ]] || return 0
  shopt -s nullglob
  for manifest in "$root"/*/SKILL.md; do
    [[ -f "$manifest" && ! -L "$manifest" ]] || continue
    names+=("$(basename "$(dirname "$manifest")")")
  done
  shopt -u nullglob
  ((${#names[@]} == 0)) || printf '%s\n' "${names[@]}" | LC_ALL=C sort -u
}

reference_target() {
  if [[ -d "$OPENCODE_TARGET" && ! -L "$OPENCODE_TARGET" ]]; then
    printf '%s\n' "$OPENCODE_TARGET"
  elif [[ -d "$CLAUDE_TARGET" && ! -L "$CLAUDE_TARGET" ]]; then
    printf '%s\n' "$CLAUDE_TARGET"
  elif [[ -d "$AGY_TARGET" && ! -L "$AGY_TARGET" ]]; then
    printf '%s\n' "$AGY_TARGET"
  fi
}

summarize_skill_changes() {
  local old_root="$1"
  local new_root="$2"
  local old_list new_list
  local name
  local -a added=() removed=() modified=()
  local -A old_names=() new_names=()

  old_list="$(list_skill_names "$old_root")"
  new_list="$(list_skill_names "$new_root")"
  while IFS= read -r name || [[ -n "$name" ]]; do
    [[ -n "$name" ]] || continue
    old_names["$name"]=1
  done <<<"$old_list"
  while IFS= read -r name || [[ -n "$name" ]]; do
    [[ -n "$name" ]] || continue
    new_names["$name"]=1
  done <<<"$new_list"

  for name in "${!new_names[@]}"; do
    [[ -n "${old_names[$name]+x}" ]] || added+=("$name")
  done
  for name in "${!old_names[@]}"; do
    [[ -n "${new_names[$name]+x}" ]] || removed+=("$name")
  done
  for name in "${!new_names[@]}"; do
    if [[ -n "${old_names[$name]+x}" ]]; then
      if ! diff -qr --no-dereference -- "$old_root/$name" "$new_root/$name" >/dev/null 2>&1; then
        modified+=("$name")
      fi
    fi
  done

  local added_count="${#added[@]}"
  local removed_count="${#removed[@]}"
  local modified_count="${#modified[@]}"
  log "Skills: +$added_count new / ~$modified_count modified / -$removed_count removed"
  if ((added_count > 0)); then
    log "New skills:"
    printf '%s\n' "${added[@]}" | LC_ALL=C sort -u | head -20 | while IFS= read -r name; do log "  + $name"; done
  fi
  if ((modified_count > 0)); then
    log "Modified skills:"
    printf '%s\n' "${modified[@]}" | LC_ALL=C sort -u | head -20 | while IFS= read -r name; do log "  ~ $name"; done
  fi
  if ((removed_count > 0)); then
    log "Removed skills:"
    printf '%s\n' "${removed[@]}" | LC_ALL=C sort -u | head -20 | while IFS= read -r name; do log "  - $name"; done
  fi
  if ((added_count == 0 && removed_count == 0 && modified_count == 0)); then
    log "No skill content differences detected."
  fi
}

targets_match_source() {
  local reference="$1"

  [[ -n "$reference" && -d "$reference" ]] || return 1
  diff -qr --no-dereference -- "$SOURCE_SKILLS_DIR" "$reference" >/dev/null 2>&1
}

write_installed_state() {
  local commit="$1"
  local skill_count="$2"
  local state_parent="${STATE_PATH%/*}"
  local state_temp

  [[ "$commit" =~ ^[0-9a-f]{40}$ ]] || fail "Cannot record invalid installed commit"
  mkdir -p -- "$state_parent"
  [[ ! -L "$STATE_PATH" ]] || fail "Installer state path became a symbolic link: $STATE_PATH"
  state_temp="$(mktemp "$state_parent/.installed-skills-state.XXXXXX")"
  printf '%s\n' \
    "repository_url=$REPOSITORY_URL" \
    "branch=$BRANCH" \
    "source_commit=$commit" \
    "skill_count=$skill_count" \
    "installed_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    >"$state_temp"
  mv -- "$state_temp" "$STATE_PATH"
}

count_skill_manifests() {
  local root="$1"
  local manifest
  local count=0
  local -a manifests=()

  if [[ -d "$root" ]]; then
    shopt -s nullglob
    manifests=("$root"/*/SKILL.md)
    shopt -u nullglob
  fi
  for manifest in "${manifests[@]}"; do
    [[ -f "$manifest" && ! -L "$manifest" ]] && ((count += 1))
  done
  printf '%s\n' "$count"
}

validate_skill_tree() {
  local root="$1"
  local expected_count="${2:-}"
  local manifest
  local -a manifests=()

  [[ -d "$root" && ! -L "$root" ]] || fail "Skill directory not found or unsafe: $root"
  assert_no_symlinks "$root"
  assert_no_protected_codegraph "$root"
  shopt -s nullglob
  manifests=("$root"/*/SKILL.md)
  shopt -u nullglob
  for manifest in "${manifests[@]}"; do
    [[ -f "$manifest" && ! -L "$manifest" ]] || \
      fail "Skill manifest is not a regular non-symlink file: $manifest"
  done
  LAST_SKILL_COUNT="${#manifests[@]}"
  ((LAST_SKILL_COUNT >= MIN_SKILL_COUNT)) || \
    fail "Skill validation found $LAST_SKILL_COUNT manifests; minimum is $MIN_SKILL_COUNT in $root"
  if [[ -n "$expected_count" ]]; then
    ((LAST_SKILL_COUNT == expected_count)) || \
      fail "Skill validation expected $expected_count manifests but found $LAST_SKILL_COUNT in $root"
  fi
}

prepare_tree() {
  local source="$1"
  local target="$2"
  local output_variable="$3"
  local parent="${target%/*}"
  local basename="${target##*/}"
  local prepared

  [[ -d "$source" && ! -L "$source" ]] || fail "Replacement source is missing or unsafe: $source"
  mkdir -p -- "$parent"
  prepared="$(mktemp -d "$parent/.${basename}.replacement.XXXXXX")"
  cp -a -- "$source/." "$prepared/"
  diff -qr --no-dereference -- "$source" "$prepared" >/dev/null || \
    fail "Prepared replacement differs from its source: $target"
  printf -v "$output_variable" '%s' "$prepared"
}

generate_snapshot_id() {
  local timestamp
  local suffix
  local candidate

  timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
  while :; do
    suffix="$(printf '%04x%04x' "$RANDOM" "$RANDOM")"
    candidate="$timestamp-$suffix"
    if [[ ! -e "$BACKUP_ROOT/$candidate" && ! -L "$BACKUP_ROOT/$candidate" ]]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
}

create_snapshot() {
  local reason="$1"
  local source_commit="$2"
  local snapshot_id
  local created_utc
  local opencode_existed=0
  local claude_existed=0
  local agy_existed=0
  local opencode_count=0
  local claude_count=0
  local agy_count=0

  [[ "$reason" == "install" || "$reason" == "pre-restore" ]] || fail "Invalid snapshot reason: $reason"
  [[ "$source_commit" == "unknown" || "$source_commit" =~ ^[0-9a-f]{40}$ ]] || fail "Invalid snapshot source commit"
  mkdir -p -- "$BACKUP_ROOT"
  [[ -d "$BACKUP_ROOT" && ! -L "$BACKUP_ROOT" ]] || fail "Backup root became unsafe: $BACKUP_ROOT"

  snapshot_id="$(generate_snapshot_id)"
  created_utc="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  SNAPSHOT_TEMP="$(mktemp -d "$BACKUP_ROOT/.snapshot.${snapshot_id}.XXXXXX")"

  if [[ -e "$OPENCODE_TARGET" ]]; then
    opencode_existed=1
    opencode_count="$(count_skill_manifests "$OPENCODE_TARGET")"
    cp -a -- "$OPENCODE_TARGET" "$SNAPSHOT_TEMP/opencode"
  fi
  if [[ -e "$CLAUDE_TARGET" ]]; then
    claude_existed=1
    claude_count="$(count_skill_manifests "$CLAUDE_TARGET")"
    cp -a -- "$CLAUDE_TARGET" "$SNAPSHOT_TEMP/claude"
  fi
  if [[ -e "$AGY_TARGET" ]]; then
    agy_existed=1
    agy_count="$(count_skill_manifests "$AGY_TARGET")"
    cp -a -- "$AGY_TARGET" "$SNAPSHOT_TEMP/agy"
  fi

  printf '%s\n' \
    'format_version=2' \
    "id=$snapshot_id" \
    "created_utc=$created_utc" \
    "reason=$reason" \
    "source_commit=$source_commit" \
    'opencode_captured=1' \
    'claude_captured=1' \
    'agy_captured=1' \
    "opencode_existed=$opencode_existed" \
    "claude_existed=$claude_existed" \
    "agy_existed=$agy_existed" \
    "opencode_skill_count=$opencode_count" \
    "claude_skill_count=$claude_count" \
    "agy_skill_count=$agy_count" \
    >"$SNAPSHOT_TEMP/metadata"

  mv -- "$SNAPSHOT_TEMP" "$BACKUP_ROOT/$snapshot_id"
  SNAPSHOT_TEMP=""
  LAST_SNAPSHOT_ID="$snapshot_id"
}

validate_target_metadata_consistency() {
  local label="$1"
  local captured="$2"
  local existed="$3"
  local skill_count="$4"

  [[ "$captured" =~ ^[01]$ ]] || fail "Invalid $label snapshot capture metadata"
  [[ "$existed" =~ ^[01]$ ]] || fail "Invalid $label snapshot existence metadata"
  [[ "$skill_count" =~ ^[0-9]+$ ]] || fail "Invalid $label snapshot skill count metadata"
  if ((captured == 0 && (existed != 0 || skill_count != 0))); then
    fail "Inconsistent $label snapshot metadata: an uncaptured target must be absent with zero skills"
  fi
  if ((existed == 0 && skill_count != 0)); then
    fail "Inconsistent $label snapshot metadata: an absent target must have zero skills"
  fi
}

load_metadata() {
  local metadata_file="$1"
  local line
  local key
  local value
  local format_version=""
  local opencode_captured=""
  local claude_captured=""
  local agy_captured=""
  local required_key
  local forbidden_key
  local -A seen=()
  local -a common_required_keys=(format_version id created_utc reason source_commit opencode_existed claude_existed opencode_skill_count claude_skill_count)
  local -a version_one_forbidden_keys=(opencode_captured claude_captured agy_captured agy_existed agy_skill_count)
  local -a version_two_required_target_keys=(
    opencode_captured opencode_existed opencode_skill_count
    claude_captured claude_existed claude_skill_count
    agy_captured agy_existed agy_skill_count
  )

  META_ID=""
  META_FORMAT_VERSION=""
  META_CREATED_UTC=""
  META_REASON=""
  META_SOURCE_COMMIT=""
  META_OPENCODE_CAPTURED=0
  META_CLAUDE_CAPTURED=0
  META_OPENCODE_EXISTED=""
  META_CLAUDE_EXISTED=""
  META_AGY_CAPTURED=0
  META_AGY_EXISTED=""
  META_OPENCODE_SKILL_COUNT=""
  META_CLAUDE_SKILL_COUNT=""
  META_AGY_SKILL_COUNT=""

  [[ -f "$metadata_file" && ! -L "$metadata_file" ]] || fail "Snapshot metadata is missing or unsafe: $metadata_file"
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ "$line" == *=* ]] || fail "Malformed snapshot metadata: $metadata_file"
    key="${line%%=*}"
    value="${line#*=}"
    case "$key" in
      format_version|id|created_utc|reason|source_commit|opencode_captured|claude_captured|agy_captured|opencode_existed|claude_existed|agy_existed|opencode_skill_count|claude_skill_count|agy_skill_count) ;;
      *) fail "Unknown metadata key '$key': $metadata_file" ;;
    esac
    [[ -z "${seen[$key]+x}" ]] || fail "Duplicate metadata key '$key': $metadata_file"
    seen["$key"]=1

    case "$key" in
      format_version) format_version="$value" ;;
      id) META_ID="$value" ;;
      created_utc) META_CREATED_UTC="$value" ;;
      reason) META_REASON="$value" ;;
      source_commit) META_SOURCE_COMMIT="$value" ;;
      opencode_captured) opencode_captured="$value" ;;
      claude_captured) claude_captured="$value" ;;
      agy_captured) agy_captured="$value" ;;
      opencode_existed) META_OPENCODE_EXISTED="$value" ;;
      claude_existed) META_CLAUDE_EXISTED="$value" ;;
      agy_existed) META_AGY_EXISTED="$value" ;;
      opencode_skill_count) META_OPENCODE_SKILL_COUNT="$value" ;;
      claude_skill_count) META_CLAUDE_SKILL_COUNT="$value" ;;
      agy_skill_count) META_AGY_SKILL_COUNT="$value" ;;
    esac
  done <"$metadata_file"

  [[ -n "${seen[format_version]+x}" ]] || fail "Missing required metadata key 'format_version'"
  [[ "$format_version" == "1" || "$format_version" == "2" ]] || fail "Unsupported snapshot metadata version: $format_version"
  for required_key in "${common_required_keys[@]}"; do
    [[ -n "${seen[$required_key]+x}" ]] || \
      fail "Missing required metadata key '$required_key' for version $format_version"
  done
  if [[ "$format_version" == "1" ]]; then
    for forbidden_key in "${version_one_forbidden_keys[@]}"; do
      [[ -z "${seen[$forbidden_key]+x}" ]] || \
        fail "Version 1 snapshot contains unsupported key '$forbidden_key'"
    done
    opencode_captured=1
    claude_captured=1
    agy_captured=0
    META_AGY_EXISTED=0
    META_AGY_SKILL_COUNT=0
  else
    for required_key in "${version_two_required_target_keys[@]}"; do
      [[ -n "${seen[$required_key]+x}" ]] || \
        fail "Missing required metadata key '$required_key' for version 2"
    done
  fi

  META_FORMAT_VERSION="$format_version"
  is_safe_backup_id "$META_ID" || fail "Invalid snapshot ID in metadata: $META_ID"
  [[ "$META_CREATED_UTC" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]] || fail "Invalid snapshot UTC date"
  [[ "$META_REASON" == "install" || "$META_REASON" == "pre-restore" ]] || fail "Invalid snapshot reason"
  [[ "$META_SOURCE_COMMIT" == "unknown" || "$META_SOURCE_COMMIT" =~ ^[0-9a-f]{40}$ ]] || fail "Invalid snapshot source commit"
  validate_target_metadata_consistency "OpenCode" "$opencode_captured" "$META_OPENCODE_EXISTED" "$META_OPENCODE_SKILL_COUNT"
  validate_target_metadata_consistency "Claude" "$claude_captured" "$META_CLAUDE_EXISTED" "$META_CLAUDE_SKILL_COUNT"
  validate_target_metadata_consistency "AGY" "$agy_captured" "$META_AGY_EXISTED" "$META_AGY_SKILL_COUNT"
  META_OPENCODE_CAPTURED="$opencode_captured"
  META_CLAUDE_CAPTURED="$claude_captured"
  META_AGY_CAPTURED="$agy_captured"
}

validate_snapshot() {
  local snapshot_id="$1"
  local snapshot_path
  local actual_count

  require_safe_backup_id "$snapshot_id"
  snapshot_path="$BACKUP_ROOT/$snapshot_id"
  [[ -d "$snapshot_path" && ! -L "$snapshot_path" ]] || fail "Backup snapshot not found or unsafe: $snapshot_id"
  load_metadata "$snapshot_path/metadata"
  [[ "$META_ID" == "$snapshot_id" ]] || fail "Snapshot directory and metadata IDs do not match: $snapshot_id"

  if ((META_OPENCODE_CAPTURED)); then
    if ((META_OPENCODE_EXISTED)); then
      [[ -d "$snapshot_path/opencode" && ! -L "$snapshot_path/opencode" ]] || fail "OpenCode snapshot tree is missing or unsafe: $snapshot_id"
      assert_no_protected_codegraph "$snapshot_path/opencode"
      actual_count="$(count_skill_manifests "$snapshot_path/opencode")"
      [[ "$actual_count" == "$META_OPENCODE_SKILL_COUNT" ]] || fail "OpenCode snapshot skill count does not match metadata: $snapshot_id"
    else
      [[ ! -e "$snapshot_path/opencode" && ! -L "$snapshot_path/opencode" ]] || fail "Unexpected OpenCode tree in absent snapshot state: $snapshot_id"
    fi
  else
    [[ ! -e "$snapshot_path/opencode" && ! -L "$snapshot_path/opencode" ]] || fail "Uncaptured OpenCode snapshot contains a target tree: $snapshot_id"
  fi

  if ((META_CLAUDE_CAPTURED)); then
    if ((META_CLAUDE_EXISTED)); then
      [[ -d "$snapshot_path/claude" && ! -L "$snapshot_path/claude" ]] || fail "Claude snapshot tree is missing or unsafe: $snapshot_id"
      assert_no_protected_codegraph "$snapshot_path/claude"
      actual_count="$(count_skill_manifests "$snapshot_path/claude")"
      [[ "$actual_count" == "$META_CLAUDE_SKILL_COUNT" ]] || fail "Claude snapshot skill count does not match metadata: $snapshot_id"
    else
      [[ ! -e "$snapshot_path/claude" && ! -L "$snapshot_path/claude" ]] || fail "Unexpected Claude tree in absent snapshot state: $snapshot_id"
    fi
  else
    [[ ! -e "$snapshot_path/claude" && ! -L "$snapshot_path/claude" ]] || fail "Uncaptured Claude snapshot contains a target tree: $snapshot_id"
  fi
  if ((META_AGY_CAPTURED)); then
    if ((META_AGY_EXISTED)); then
      [[ -d "$snapshot_path/agy" && ! -L "$snapshot_path/agy" ]] || fail "AGY snapshot tree is missing or unsafe: $snapshot_id"
      assert_no_protected_codegraph "$snapshot_path/agy"
      actual_count="$(count_skill_manifests "$snapshot_path/agy")"
      [[ "$actual_count" == "$META_AGY_SKILL_COUNT" ]] || fail "AGY snapshot skill count does not match metadata: $snapshot_id"
    else
      [[ ! -e "$snapshot_path/agy" && ! -L "$snapshot_path/agy" ]] || fail "Unexpected AGY tree in absent snapshot state: $snapshot_id"
    fi
  else
    [[ ! -e "$snapshot_path/agy" && ! -L "$snapshot_path/agy" ]] || fail "Uncaptured AGY snapshot contains a target tree: $snapshot_id"
  fi
}

prepare_snapshot_state() {
  local snapshot_id="$1"
  local opencode_captured="$2"
  local opencode_existed="$3"
  local claude_captured="$4"
  local claude_existed="$5"
  local agy_captured="$6"
  local agy_existed="$7"
  local opencode_output_variable="$8"
  local claude_output_variable="$9"
  local agy_output_variable="${10}"

  if ((opencode_captured && opencode_existed)); then
    prepare_tree "$BACKUP_ROOT/$snapshot_id/opencode" "$OPENCODE_TARGET" "$opencode_output_variable"
  else
    printf -v "$opencode_output_variable" '%s' ""
  fi
  if ((claude_captured && claude_existed)); then
    prepare_tree "$BACKUP_ROOT/$snapshot_id/claude" "$CLAUDE_TARGET" "$claude_output_variable"
  else
    printf -v "$claude_output_variable" '%s' ""
  fi
  if ((agy_captured && agy_existed)); then
    prepare_tree "$BACKUP_ROOT/$snapshot_id/agy" "$AGY_TARGET" "$agy_output_variable"
  else
    printf -v "$agy_output_variable" '%s' ""
  fi
}

state_matches() {
  local label="$1"
  local target="$2"
  local expected_source="$3"
  local expected_existed="$4"

  if ((expected_existed)); then
    if [[ ! -d "$target" || -L "$target" ]]; then
      printf 'ERROR: %s target is missing after the transaction: %s\n' "$label" "$target" >&2
      return 1
    fi
    if ! diff -qr --no-dereference -- "$expected_source" "$target" >/dev/null; then
      printf 'ERROR: %s target differs from the prepared source: %s\n' "$label" "$target" >&2
      return 1
    fi
  elif [[ -e "$target" || -L "$target" ]]; then
    printf 'ERROR: %s target should be absent after the transaction: %s\n' "$label" "$target" >&2
    return 1
  fi
  return 0
}

remove_path_for_rollback() {
  local label="$1"
  local target="$2"

  if [[ -e "$target" || -L "$target" ]]; then
    rm -rf -- "$target" || {
      printf 'ERROR: Could not remove failed %s target: %s\n' "$label" "$target" >&2
      return 1
    }
  fi
}

restore_quarantined_target() {
  local label="$1"
  local target="$2"
  local quarantine="$3"
  local quarantine_ready="$4"
  local originally_existed="$5"

  ((quarantine_ready)) || return 0
  if ((originally_existed)) && [[ ! -e "$quarantine" && ! -L "$quarantine" ]]; then
    if ((quarantine_ready == 1)) && [[ -d "$target" && ! -L "$target" ]]; then
      return 0
    fi
    printf 'ERROR: Quarantined %s originals are missing: %s\n' "$label" "$quarantine" >&2
    return 1
  fi

  remove_path_for_rollback "$label" "$target" || return 1
  if ((originally_existed)); then
    if ! mv -- "$quarantine" "$target"; then
      printf 'ERROR: Could not restore quarantined %s originals.\n' "$label" >&2
      return 1
    fi
    [[ -d "$target" && ! -L "$target" ]] || {
      printf 'ERROR: Restored %s target is missing or unsafe: %s\n' "$label" "$target" >&2
      return 1
    }
  elif [[ -e "$target" || -L "$target" ]]; then
    printf 'ERROR: %s target should be absent after rollback: %s\n' "$label" "$target" >&2
    return 1
  fi
  return 0
}

rollback_transaction() {
  local result=0

  printf 'Operation failed; restoring all participating targets from quarantined originals...\n' >&2
  if ((OPENCODE_TRANSACTION_PARTICIPATED)); then
    restore_quarantined_target "OpenCode" "$OPENCODE_TARGET" "$OPENCODE_QUARANTINE" \
      "$OPENCODE_QUARANTINE_READY" "$ROLLBACK_OPENCODE_EXISTED" || result=1
  fi
  if ((CLAUDE_TRANSACTION_PARTICIPATED)); then
    restore_quarantined_target "Claude" "$CLAUDE_TARGET" "$CLAUDE_QUARANTINE" \
      "$CLAUDE_QUARANTINE_READY" "$ROLLBACK_CLAUDE_EXISTED" || result=1
  fi
  if ((AGY_TRANSACTION_PARTICIPATED)); then
    restore_quarantined_target "AGY" "$AGY_TARGET" "$AGY_QUARANTINE" \
      "$AGY_QUARANTINE_READY" "$ROLLBACK_AGY_EXISTED" || result=1
  fi

  if ((result == 0)); then
    printf 'Rollback completed. All original targets were restored.\n' >&2
  else
    printf 'ERROR: Rollback was incomplete; retain and inspect the snapshot and transaction paths.\n' >&2
  fi
  return "$result"
}

inject_quarantine_fidelity_state() {
  local operation="$1"

  [[ "$TEST_FAILPOINT" == "quarantine-fidelity-$operation" ]] || return 0
  if ((OPENCODE_TRANSACTION_PARTICIPATED)); then
    [[ ! -d "$OPENCODE_TARGET" ]] || printf 'changed-after-snapshot\n' >"$OPENCODE_TARGET/.quarantine-fidelity"
  fi
  if ((CLAUDE_TRANSACTION_PARTICIPATED)); then
    [[ ! -d "$CLAUDE_TARGET" ]] || printf 'changed-after-snapshot\n' >"$CLAUDE_TARGET/.quarantine-fidelity"
  fi
  if ((AGY_TRANSACTION_PARTICIPATED)); then
    [[ ! -d "$AGY_TARGET" ]] || printf 'changed-after-snapshot\n' >"$AGY_TARGET/.quarantine-fidelity"
  fi
}

cleanup_quarantine_path() {
  local label="$1"
  local path="$2"

  [[ -z "$path" || ! -e "$path" ]] && return 0
  if [[ "$TEST_FAILPOINT" == "quarantine-cleanup" ]]; then
    post_commit_warn "Injected quarantine cleanup failure retained $label originals at: $path"
    return 1
  fi
  if ! rm -rf -- "$path"; then
    post_commit_warn "Could not remove quarantined $label originals: $path"
    return 1
  fi
  return 0
}

run_transaction() {
  local operation="$1"
  local transaction_token="${LAST_SNAPSHOT_ID:-$(date -u +%Y%m%dT%H%M%SZ)}-$$"
  local cleanup_failed=0

  OPENCODE_TRANSACTION_PARTICIPATED="$DESIRED_OPENCODE_CAPTURED"
  CLAUDE_TRANSACTION_PARTICIPATED="$DESIRED_CLAUDE_CAPTURED"
  AGY_TRANSACTION_PARTICIPATED="$DESIRED_AGY_CAPTURED"
  if ((OPENCODE_TRANSACTION_PARTICIPATED)); then
    OPENCODE_QUARANTINE="${OPENCODE_TARGET}.transaction.$transaction_token"
    [[ ! -e "$OPENCODE_QUARANTINE" && ! -L "$OPENCODE_QUARANTINE" ]] || fail "Transaction path already exists: $OPENCODE_QUARANTINE"
  fi
  if ((CLAUDE_TRANSACTION_PARTICIPATED)); then
    CLAUDE_QUARANTINE="${CLAUDE_TARGET}.transaction.$transaction_token"
    [[ ! -e "$CLAUDE_QUARANTINE" && ! -L "$CLAUDE_QUARANTINE" ]] || fail "Transaction path already exists: $CLAUDE_QUARANTINE"
  fi
  if ((AGY_TRANSACTION_PARTICIPATED)); then
    AGY_QUARANTINE="${AGY_TARGET}.transaction.$transaction_token"
    [[ ! -e "$AGY_QUARANTINE" && ! -L "$AGY_QUARANTINE" ]] || fail "Transaction path already exists: $AGY_QUARANTINE"
  fi

  TRANSACTION_STARTED=1
  inject_quarantine_fidelity_state "$operation"
  if ((OPENCODE_TRANSACTION_PARTICIPATED)); then
    OPENCODE_QUARANTINE_READY=1
    if [[ -e "$OPENCODE_TARGET" ]]; then
      ROLLBACK_OPENCODE_EXISTED=1
      mv -- "$OPENCODE_TARGET" "$OPENCODE_QUARANTINE"
    else
      ROLLBACK_OPENCODE_EXISTED=0
    fi
    OPENCODE_QUARANTINE_READY=2
  fi
  if ((CLAUDE_TRANSACTION_PARTICIPATED)); then
    CLAUDE_QUARANTINE_READY=1
    if [[ -e "$CLAUDE_TARGET" ]]; then
      ROLLBACK_CLAUDE_EXISTED=1
      mv -- "$CLAUDE_TARGET" "$CLAUDE_QUARANTINE"
    else
      ROLLBACK_CLAUDE_EXISTED=0
    fi
    CLAUDE_QUARANTINE_READY=2
  fi
  if ((AGY_TRANSACTION_PARTICIPATED)) && [[ -e "$AGY_TARGET" ]]; then
    AGY_QUARANTINE_READY=1
    ROLLBACK_AGY_EXISTED=1
    mv -- "$AGY_TARGET" "$AGY_QUARANTINE"
    AGY_QUARANTINE_READY=2
  elif ((AGY_TRANSACTION_PARTICIPATED)); then
    AGY_QUARANTINE_READY=2
    ROLLBACK_AGY_EXISTED=0
  fi

  if ((DESIRED_OPENCODE_CAPTURED && DESIRED_OPENCODE_EXISTED)); then
    mv -- "$DESIRED_OPENCODE_PREPARED" "$OPENCODE_TARGET"
    DESIRED_OPENCODE_PREPARED=""
  fi

  if [[ "$operation" == "install" && "$TEST_FAILPOINT" == "after-opencode-install" ]]; then
    fail "Injected test failure after OpenCode installation"
  fi
  if [[ "$operation" == "restore" && "$TEST_FAILPOINT" == "after-opencode-restore" ]]; then
    fail "Injected test failure after OpenCode restoration"
  fi

  if ((DESIRED_CLAUDE_CAPTURED && DESIRED_CLAUDE_EXISTED)); then
    mv -- "$DESIRED_CLAUDE_PREPARED" "$CLAUDE_TARGET"
    DESIRED_CLAUDE_PREPARED=""
  fi
  if [[ "$operation" == "install" && "$TEST_FAILPOINT" == "after-claude-install" ]]; then
    fail "Injected test failure after Claude installation"
  fi
  if [[ "$operation" == "restore" && "$TEST_FAILPOINT" == "after-claude-restore" ]]; then
    fail "Injected test failure after Claude restoration"
  fi

  if ((DESIRED_AGY_CAPTURED && DESIRED_AGY_EXISTED)); then
    mv -- "$DESIRED_AGY_PREPARED" "$AGY_TARGET"
    DESIRED_AGY_PREPARED=""
  fi
  if [[ "$operation" == "install" && "$TEST_FAILPOINT" == "after-agy-install" ]]; then
    fail "Injected test failure after AGY installation"
  fi
  if [[ "$operation" == "restore" && "$TEST_FAILPOINT" == "after-agy-restore" ]]; then
    fail "Injected test failure after AGY restoration"
  fi
  if [[ "$TEST_FAILPOINT" == "quarantine-fidelity-$operation" ]]; then
    fail "Injected test failure after all quarantined originals were replaced"
  fi

  if ((DESIRED_OPENCODE_CAPTURED)); then
    state_matches "OpenCode" "$OPENCODE_TARGET" "$DESIRED_OPENCODE_SOURCE" "$DESIRED_OPENCODE_EXISTED" || fail "OpenCode post-transaction validation failed"
  fi
  if ((DESIRED_CLAUDE_CAPTURED)); then
    state_matches "Claude" "$CLAUDE_TARGET" "$DESIRED_CLAUDE_SOURCE" "$DESIRED_CLAUDE_EXISTED" || fail "Claude post-transaction validation failed"
  fi
  if ((DESIRED_AGY_CAPTURED)); then
    state_matches "AGY" "$AGY_TARGET" "$DESIRED_AGY_SOURCE" "$DESIRED_AGY_EXISTED" || fail "AGY post-transaction validation failed"
  fi
  COMMITTED=1

  if ((OPENCODE_TRANSACTION_PARTICIPATED)); then
    if cleanup_quarantine_path "OpenCode" "$OPENCODE_QUARANTINE"; then
      OPENCODE_QUARANTINE=""
    else
      cleanup_failed=1
    fi
  fi
  if ((CLAUDE_TRANSACTION_PARTICIPATED)); then
    if cleanup_quarantine_path "Claude" "$CLAUDE_QUARANTINE"; then
      CLAUDE_QUARANTINE=""
    else
      cleanup_failed=1
    fi
  fi
  if ((AGY_TRANSACTION_PARTICIPATED)); then
    if cleanup_quarantine_path "AGY" "$AGY_QUARANTINE"; then
      AGY_QUARANTINE=""
    else
      cleanup_failed=1
    fi
  fi
  if ((cleanup_failed)); then
    post_commit_warn "Transaction committed successfully, but quarantine cleanup was incomplete. The new targets and BKOld snapshot are valid; inspect the retained transaction paths above."
  fi
  [[ "$TEST_FAILPOINT" != "post-commit-report" ]] || return 75
  return 0
}

list_backups() {
  local snapshot_path
  local snapshot_id
  local found=0
  local opencode_label
  local claude_label
  local agy_label
  local opencode_count_label
  local claude_count_label
  local agy_count_label

  if [[ ! -d "$BACKUP_ROOT" ]]; then
    log "No backups found in $BACKUP_ROOT."
    return 0
  fi

  printf '%sManaged backups in %s%s\n' "$COLOR_BOLD" "$BACKUP_ROOT" "$COLOR_RESET"
  shopt -s nullglob
  for snapshot_path in "$BACKUP_ROOT"/*; do
    [[ -d "$snapshot_path" ]] || continue
    if [[ -L "$snapshot_path" ]]; then
      warn "Skipping symbolic-link snapshot: ${snapshot_path##*/}"
      continue
    fi
    snapshot_id="${snapshot_path##*/}"
    is_safe_backup_id "$snapshot_id" || {
      warn "Skipping directory with an unsafe backup ID: $snapshot_id"
      continue
    }
    validate_snapshot "$snapshot_id"
    ((found += 1))
    opencode_label="not captured"
    claude_label="not captured"
    agy_label="not captured"
    opencode_count_label="n/a"
    claude_count_label="n/a"
    agy_count_label="n/a"
    if ((META_OPENCODE_CAPTURED)); then
      opencode_label="no"
      ((META_OPENCODE_EXISTED)) && opencode_label="yes"
      opencode_count_label="$META_OPENCODE_SKILL_COUNT"
    fi
    if ((META_CLAUDE_CAPTURED)); then
      claude_label="no"
      ((META_CLAUDE_EXISTED)) && claude_label="yes"
      claude_count_label="$META_CLAUDE_SKILL_COUNT"
    fi
    if ((META_AGY_CAPTURED)); then
      agy_label="no"
      ((META_AGY_EXISTED)) && agy_label="yes"
      agy_count_label="$META_AGY_SKILL_COUNT"
    fi
    printf '%s\n' "$META_ID"
    printf '  UTC: %s | Reason: %s | Targets: OpenCode %s, Claude %s, AGY %s\n' \
      "$META_CREATED_UTC" "$META_REASON" "$opencode_label" "$claude_label" "$agy_label"
    printf '  Skills: %s/%s/%s | Source commit: %s\n' \
      "$opencode_count_label" "$claude_count_label" "$agy_count_label" "$META_SOURCE_COMMIT"
  done
  shopt -u nullglob

  ((found > 0)) || log "No valid backups found."
}

run_status() {
  local reference=""

  get_remote_head
  if ! resolve_installed_commit; then
    log "Installed version: none (no previous install recorded)"
    log "Remote version: $REMOTE_COMMIT (branch: $BRANCH)"
    log "Run './install-skills.sh install' to install the current skill-pack."
    return 0
  fi

  log "Installed version: $INSTALLED_COMMIT"
  if [[ -n "$INSTALLED_REPOSITORY" ]]; then
    log "Installed source: $INSTALLED_REPOSITORY (branch: ${INSTALLED_BRANCH:-unknown})"
  fi
  if [[ -n "$INSTALLED_SKILL_COUNT" ]]; then
    log "Installed skills: $INSTALLED_SKILL_COUNT"
  fi
  log "Remote version: $REMOTE_COMMIT (branch: $BRANCH)"

  if [[ -n "$INSTALLED_REPOSITORY" && "$INSTALLED_REPOSITORY" != "$REPOSITORY_URL" ]]; then
    warn "Configured source differs from the last install; comparison is by commit only."
  fi
  if [[ -n "$INSTALLED_BRANCH" && "$INSTALLED_BRANCH" != "$BRANCH" ]]; then
    warn "Configured branch differs from the last install; comparison is by commit only."
  fi

  if [[ "$REMOTE_COMMIT" == "$INSTALLED_COMMIT" ]]; then
    reference="$(reference_target)"
    if [[ -n "$reference" ]]; then
      log "Skill diff against $reference:"
      clone_repository >/dev/null
      validate_skill_tree "$SOURCE_SKILLS_DIR"
      summarize_skill_changes "$reference" "$SOURCE_SKILLS_DIR"
      if targets_match_source "$reference"; then
        success "Already up to date: installed version matches the remote version."
      else
        warn "Same commit as installed, but local targets differ (manual drift). Re-run install to restore the canonical tree."
      fi
    else
      success "Already up to date: installed version matches the remote version."
    fi
    return 0
  fi

  log "A newer skill-pack version is available: $INSTALLED_COMMIT -> $REMOTE_COMMIT"
  clone_repository >/dev/null
  validate_skill_tree "$SOURCE_SKILLS_DIR"
  log "Source commit: $SOURCE_COMMIT"
  log "Validated skills: $LAST_SKILL_COUNT"
  reference="$(reference_target)"
  if [[ -n "$reference" ]]; then
    log "Skill diff against $reference:"
    summarize_skill_changes "$reference" "$SOURCE_SKILLS_DIR"
  else
    log "No installed targets found; all $LAST_SKILL_COUNT skills would be new."
  fi
  log "Run './install-skills.sh install' to update."
}

run_install() {
  local reference=""
  ((DRY_RUN)) || acquire_installer_lock
  clone_repository
  validate_skill_tree "$SOURCE_SKILLS_DIR"
  SOURCE_SKILL_COUNT="$LAST_SKILL_COUNT"

  log "Source commit: $SOURCE_COMMIT"
  log "Validated skills: $SOURCE_SKILL_COUNT"
  log "OpenCode target: $OPENCODE_TARGET (source: $OPENCODE_TARGET_ORIGIN)"
  log "Claude target: $CLAUDE_TARGET (source: $CLAUDE_TARGET_ORIGIN)"
  log "AGY target: $AGY_TARGET (source: $AGY_TARGET_ORIGIN)"
  log "Backup root: $BACKUP_ROOT"

  if resolve_installed_commit; then
    log "Installed version: $INSTALLED_COMMIT"
    if [[ "$INSTALLED_COMMIT" == "$SOURCE_COMMIT" && "$INSTALLED_REPOSITORY" == "$REPOSITORY_URL" && "$INSTALLED_BRANCH" == "$BRANCH" ]]; then
      reference="$(reference_target)"
      if [[ -n "$reference" ]] && targets_match_source "$reference" && ((FORCE == 0)); then
        success "Already up to date: version $SOURCE_COMMIT is installed in all targets."
        log "Use './install-skills.sh install --force' to reinstall the same version."
        return 0
      fi
      if [[ -n "$reference" ]] && ! targets_match_source "$reference"; then
        warn "Same commit as installed, but local targets differ (manual drift). Proceeding with reinstall."
      elif ((FORCE == 0)); then
        # State matches but reference check was inconclusive (e.g. missing
        # targets on first run with a stale state file); fall through to install.
        :
      fi
    else
      reference="$(reference_target)"
      if [[ -n "$reference" ]]; then
        log "Skill changes vs $reference:"
        summarize_skill_changes "$reference" "$SOURCE_SKILLS_DIR"
      fi
    fi
  else
    log "Installed version: none (fresh install)"
  fi

  if ((DRY_RUN)); then
    success "Dry run complete; no target, replacement, or backup directory was created."
    warn "A real replacement removes existing skills from all three targets. Reinstall Gentle AI afterward, then restart OpenCode, Claude, and AGY."
    return 0
  fi

  if ((FORCE)); then
    log "Force reinstall requested; proceeding even if the version is unchanged."
  fi

  prepare_tree "$SOURCE_SKILLS_DIR" "$OPENCODE_TARGET" DESIRED_OPENCODE_PREPARED
  validate_skill_tree "$DESIRED_OPENCODE_PREPARED" "$SOURCE_SKILL_COUNT"
  prepare_tree "$SOURCE_SKILLS_DIR" "$CLAUDE_TARGET" DESIRED_CLAUDE_PREPARED
  validate_skill_tree "$DESIRED_CLAUDE_PREPARED" "$SOURCE_SKILL_COUNT"
  prepare_tree "$SOURCE_SKILLS_DIR" "$AGY_TARGET" DESIRED_AGY_PREPARED
  validate_skill_tree "$DESIRED_AGY_PREPARED" "$SOURCE_SKILL_COUNT"

  create_snapshot "install" "$SOURCE_COMMIT"
  validate_snapshot "$LAST_SNAPSHOT_ID"
  ROLLBACK_OPENCODE_EXISTED="$META_OPENCODE_EXISTED"
  ROLLBACK_CLAUDE_EXISTED="$META_CLAUDE_EXISTED"
  ROLLBACK_AGY_EXISTED="$META_AGY_EXISTED"
  ROLLBACK_OPENCODE_SOURCE="$BACKUP_ROOT/$LAST_SNAPSHOT_ID/opencode"
  ROLLBACK_CLAUDE_SOURCE="$BACKUP_ROOT/$LAST_SNAPSHOT_ID/claude"
  ROLLBACK_AGY_SOURCE="$BACKUP_ROOT/$LAST_SNAPSHOT_ID/agy"

  DESIRED_OPENCODE_EXISTED=1
  DESIRED_CLAUDE_EXISTED=1
  DESIRED_OPENCODE_CAPTURED=1
  DESIRED_CLAUDE_CAPTURED=1
  DESIRED_AGY_CAPTURED=1
  DESIRED_AGY_EXISTED=1
  DESIRED_OPENCODE_SOURCE="$SOURCE_SKILLS_DIR"
  DESIRED_CLAUDE_SOURCE="$SOURCE_SKILLS_DIR"
  DESIRED_AGY_SOURCE="$SOURCE_SKILLS_DIR"
  run_transaction "install"

  write_installed_state "$SOURCE_COMMIT" "$SOURCE_SKILL_COUNT"
  post_commit_success "Backup snapshot: $LAST_SNAPSHOT_ID"
  post_commit_success "Installed $SOURCE_SKILL_COUNT skills from commit $SOURCE_COMMIT into OpenCode, Claude, and AGY."
  post_commit_warn "This replacement removed existing skills from all three directories. Reinstall Gentle AI, then restart OpenCode, Claude, and AGY."
}

run_restore() {
  local selected_id="$1"
  local selected_commit
  local selected_opencode_captured
  local selected_opencode_existed
  local selected_claude_captured
  local selected_claude_existed
  local selected_agy_captured
  local selected_agy_existed
  local safety_id

  acquire_installer_lock
  validate_snapshot "$selected_id"
  selected_commit="$META_SOURCE_COMMIT"
  selected_opencode_captured="$META_OPENCODE_CAPTURED"
  selected_opencode_existed="$META_OPENCODE_EXISTED"
  selected_claude_captured="$META_CLAUDE_CAPTURED"
  selected_claude_existed="$META_CLAUDE_EXISTED"
  selected_agy_captured="$META_AGY_CAPTURED"
  selected_agy_existed="${META_AGY_EXISTED:-0}"

  DESIRED_OPENCODE_CAPTURED="$selected_opencode_captured"
  DESIRED_OPENCODE_EXISTED="$selected_opencode_existed"
  DESIRED_CLAUDE_CAPTURED="$selected_claude_captured"
  DESIRED_CLAUDE_EXISTED="$selected_claude_existed"
  DESIRED_AGY_CAPTURED="$selected_agy_captured"
  DESIRED_AGY_EXISTED="$selected_agy_existed"
  DESIRED_OPENCODE_SOURCE="$BACKUP_ROOT/$selected_id/opencode"
  DESIRED_CLAUDE_SOURCE="$BACKUP_ROOT/$selected_id/claude"
  DESIRED_AGY_SOURCE="$BACKUP_ROOT/$selected_id/agy"
  prepare_snapshot_state "$selected_id" "$DESIRED_OPENCODE_CAPTURED" "$DESIRED_OPENCODE_EXISTED" \
    "$DESIRED_CLAUDE_CAPTURED" "$DESIRED_CLAUDE_EXISTED" "$DESIRED_AGY_CAPTURED" "$DESIRED_AGY_EXISTED" \
    DESIRED_OPENCODE_PREPARED DESIRED_CLAUDE_PREPARED DESIRED_AGY_PREPARED

  create_snapshot "pre-restore" "$selected_commit"
  safety_id="$LAST_SNAPSHOT_ID"
  validate_snapshot "$safety_id"
  ROLLBACK_OPENCODE_EXISTED="$META_OPENCODE_EXISTED"
  ROLLBACK_CLAUDE_EXISTED="$META_CLAUDE_EXISTED"
  ROLLBACK_AGY_EXISTED="$META_AGY_EXISTED"
  ROLLBACK_OPENCODE_SOURCE="$BACKUP_ROOT/$safety_id/opencode"
  ROLLBACK_CLAUDE_SOURCE="$BACKUP_ROOT/$safety_id/claude"
  ROLLBACK_AGY_SOURCE="$BACKUP_ROOT/$safety_id/agy"

  if (( ! DESIRED_OPENCODE_CAPTURED )); then
    DESIRED_OPENCODE_EXISTED="$ROLLBACK_OPENCODE_EXISTED"
    DESIRED_OPENCODE_SOURCE="$ROLLBACK_OPENCODE_SOURCE"
    info "OpenCode was not captured and its current target will remain unchanged."
  fi
  if (( ! DESIRED_CLAUDE_CAPTURED )); then
    DESIRED_CLAUDE_EXISTED="$ROLLBACK_CLAUDE_EXISTED"
    DESIRED_CLAUDE_SOURCE="$ROLLBACK_CLAUDE_SOURCE"
    info "Claude was not captured and its current target will remain unchanged."
  fi
  if (( ! DESIRED_AGY_CAPTURED )); then
    DESIRED_AGY_EXISTED="$ROLLBACK_AGY_EXISTED"
    DESIRED_AGY_SOURCE="$ROLLBACK_AGY_SOURCE"
    info "AGY was not captured and its current target will remain unchanged."
  fi

  info "Restoring snapshot $selected_id across all captured targets..."
  run_transaction "restore"
  if [[ "$selected_commit" =~ ^[0-9a-f]{40}$ ]]; then
    write_installed_state "$selected_commit" "$(count_skill_manifests "$OPENCODE_TARGET")"
  fi
  post_commit_success "Restored snapshot: $selected_id"
  post_commit_success "Pre-restore safety snapshot: $safety_id"
  if ((selected_opencode_captured && selected_claude_captured && selected_agy_captured)); then
    post_commit_warn "Restart OpenCode, Claude, and AGY so they reload the restored skills. Reinstall Gentle AI if the restored snapshot does not contain it."
  else
    post_commit_warn "Restart applications whose captured skill targets were restored. Uncaptured targets were left unchanged."
  fi
}

confirm() {
  local prompt="$1"
  local answer

  printf '%s [y/N] ' "$prompt"
  IFS= read -r answer || return 1
  [[ "$answer" == "y" || "$answer" == "Y" || "$answer" == "yes" || "$answer" == "YES" ]]
}

interactive_restore() {
  local selected_id

  list_backups
  printf 'Backup ID to restore: '
  IFS= read -r selected_id || return 0
  [[ -n "$selected_id" ]] || {
    log "Restore cancelled."
    return 0
  }
  require_safe_backup_id "$selected_id"
  validate_snapshot "$selected_id"
  warn "Restore replaces all captured target states and creates a new three-target pre-restore safety snapshot."
  if confirm "Restore $selected_id?"; then
    "$SCRIPT_PATH" restore "$selected_id"
  else
    log "Restore cancelled."
  fi
}

interactive_menu() {
  local choice

  [[ -t 0 && -t 1 ]] || fail "The interactive menu requires a TTY"
  while :; do
    printf '\n%sChito Skill Installer%s\n' "$COLOR_BOLD" "$COLOR_RESET"
    printf '  1) Install/update all three targets\n'
    printf '  2) Dry run\n'
    printf '  3) Check for updates (status)\n'
    printf '  4) List backups\n'
    printf '  5) Restore backup\n'
    printf '  6) Help\n'
    printf '  7) Exit\n'
    printf 'Choose [1-7]: '
    IFS= read -r choice || return 0

    case "$choice" in
      1)
        warn "Install fully replaces all three skill directories after creating a managed snapshot."
        if confirm "Continue with install/update?"; then
          "$SCRIPT_PATH" install
        else
          log "Installation cancelled."
        fi
        ;;
      2) "$SCRIPT_PATH" dry-run ;;
      3) "$SCRIPT_PATH" status ;;
      4) list_backups ;;
      5) interactive_restore ;;
      6) usage ;;
      7) return 0 ;;
      *) warn "Enter a number from 1 to 7." ;;
    esac
  done
}

cleanup_path() {
  local path="$1"

  [[ -z "$path" || ! -e "$path" ]] || rm -rf -- "$path"
}

cleanup() {
  cleanup_path "$DESIRED_OPENCODE_PREPARED"
  cleanup_path "$DESIRED_CLAUDE_PREPARED"
  cleanup_path "$DESIRED_AGY_PREPARED"
  cleanup_path "$SNAPSHOT_TEMP"
  cleanup_path "$STAGING_DIR"
}

on_exit() {
  local status="$1"

  trap - EXIT
  set +e
  if ((COMMITTED)); then
    status=0
  elif ((status != 0 && TRANSACTION_STARTED)); then
    rollback_transaction || status=1
  fi
  cleanup
  exit "$status"
}

on_signal() {
  local status="$1"
  local signal_name="$2"

  if ((COMMITTED)); then
    post_commit_warn "$signal_name received after commit; committed targets and the BKOld snapshot were retained."
    exit 0
  fi
  exit "$status"
}

main() {
  configure_colors
  parse_arguments "$@"
  if [[ "$ACTION" == "help" ]]; then
    usage
    return 0
  fi

  check_dependencies
  validate_configuration

  case "$ACTION" in
    install) run_install ;;
    status) run_status ;;
    list) list_backups ;;
    restore)
      if [[ -z "$RESTORE_ID" ]]; then
        [[ -t 0 && -t 1 ]] || fail "restore requires BACKUP_ID without a TTY"
        interactive_restore
      else
        require_safe_backup_id "$RESTORE_ID"
        run_restore "$RESTORE_ID"
      fi
      ;;
    menu) interactive_menu ;;
    *) fail "Internal error: unsupported action '$ACTION'" ;;
  esac
}

trap 'on_exit $?' EXIT
trap 'on_signal 130 INT' INT
trap 'on_signal 143 TERM' TERM

main "$@"
