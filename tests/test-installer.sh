#!/usr/bin/env bash

set -euo pipefail

readonly PROJECT_ROOT="$(realpath "$(dirname "${BASH_SOURCE[0]}")/..")"
readonly INSTALLER="$PROJECT_ROOT/install-skills.sh"
TEST_ROOT=""

unset OPENCODE_SKILLS_DIR OPENCODE_CONFIG_DIR XDG_CONFIG_HOME
unset CLAUDE_SKILLS_DIR CLAUDE_CONFIG_DIR AGY_SKILLS_DIR

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_file() {
  [[ -f "$1" ]] || fail "Expected file: $1"
}

assert_dir() {
  [[ -d "$1" && ! -L "$1" ]] || fail "Expected directory: $1"
}

assert_absent() {
  [[ ! -e "$1" && ! -L "$1" ]] || fail "Expected path to be absent: $1"
}

assert_contains() {
  local value="$1"
  local expected="$2"

  [[ "$value" == *"$expected"* ]] || fail "Expected output to contain: $expected"
}

create_skill() {
  local repository="$1"
  local name="$2"

  mkdir -p -- "$repository/skills/$name"
  printf -- '---\nname: %s\ndescription: Fixture skill\n---\n\n# %s\n' \
    "$name" "$name" >"$repository/skills/$name/SKILL.md"
}

prepare_original_target() {
  local target="$1"
  local marker="$2"

  mkdir -p -- "$target/old-skill/nested"
  printf '%s\n' "$marker" >"$target/old-skill/SKILL.md"
  printf '%s\n' "$marker" >"$target/old-skill/nested/complete-tree.txt"
  printf '%s\n' "$marker" >"$target/stale-file.txt"
}

run_installer() {
  local repository="$1"
  local opencode_target="$2"
  local claude_target="$3"
  local backup_root="$4"
  local agy_target="${backup_root%/*}/agy/skills"
  shift 4

  env \
    HOME="${backup_root%/*}/home" \
    OPENCODE_CONFIG_DIR= \
    XDG_CONFIG_HOME= \
    CLAUDE_CONFIG_DIR= \
    SKILL_PACK_REPOSITORY_URL="file://$repository" \
    SKILL_PACK_BRANCH="main" \
    SKILL_PACK_MIN_SKILL_COUNT="2" \
    OPENCODE_SKILLS_DIR="$opencode_target" \
    CLAUDE_SKILLS_DIR="$claude_target" \
    AGY_SKILLS_DIR="$agy_target" \
    SKILL_BACKUP_DIR="$backup_root" \
    SKILL_INSTALLER_LOCK_FILE="${backup_root%/*}/installer.lock" \
    SKILL_INSTALLER_STATE_FILE="${backup_root%/*}/installer.state" \
    "$INSTALLER" "$@"
}

run_installer_with_failpoint() {
  local failpoint="$1"
  shift

  SKILL_INSTALLER_TEST_FAILPOINT="$failpoint" run_installer "$@"
}

run_installer_with_tmpdir() {
  local repository="$1"
  local opencode_target="$2"
  local claude_target="$3"
  local agy_target="$4"
  local backup_root="$5"
  local temporary_root="$6"
  local home="$7"
  shift 7

  env \
    HOME="$home" \
    TMPDIR="$temporary_root" \
    OPENCODE_CONFIG_DIR= \
    XDG_CONFIG_HOME= \
    CLAUDE_CONFIG_DIR= \
    SKILL_PACK_REPOSITORY_URL="file://$repository" \
    SKILL_PACK_BRANCH="main" \
    SKILL_PACK_MIN_SKILL_COUNT="2" \
    OPENCODE_SKILLS_DIR="$opencode_target" \
    CLAUDE_SKILLS_DIR="$claude_target" \
    AGY_SKILLS_DIR="$agy_target" \
    SKILL_BACKUP_DIR="$backup_root" \
    SKILL_INSTALLER_LOCK_FILE="${backup_root%/*}/installer.lock" \
    SKILL_INSTALLER_STATE_FILE="${backup_root%/*}/installer.state" \
    "$INSTALLER" "$@"
}

run_autodetect_dry_run() {
  local repository="$1"
  local home="$2"
  local backup_root="$3"
  shift 3

  env \
    -u OPENCODE_SKILLS_DIR \
    -u OPENCODE_CONFIG_DIR \
    -u XDG_CONFIG_HOME \
    -u CLAUDE_SKILLS_DIR \
    -u CLAUDE_CONFIG_DIR \
    -u AGY_SKILLS_DIR \
    HOME="$home" \
    TMPDIR="$TEST_ROOT" \
    SKILL_PACK_REPOSITORY_URL="file://$repository" \
    SKILL_PACK_BRANCH="main" \
    SKILL_PACK_MIN_SKILL_COUNT="2" \
    SKILL_BACKUP_DIR="$backup_root" \
    SKILL_INSTALLER_LOCK_FILE="${backup_root%/*}/installer.lock" \
    SKILL_INSTALLER_STATE_FILE="${backup_root%/*}/installer.state" \
    "$@" \
    "$INSTALLER" dry-run
}

metadata_value() {
  local metadata_file="$1"
  local requested_key="$2"
  local key
  local value

  while IFS='=' read -r key value; do
    if [[ "$key" == "$requested_key" ]]; then
      printf '%s\n' "$value"
      return 0
    fi
  done <"$metadata_file"
  return 1
}

set_metadata_value() {
  local metadata_file="$1"
  local requested_key="$2"
  local requested_value="$3"
  local rewritten_file="$metadata_file.rewrite"
  local line
  local key
  local found=0

  : >"$rewritten_file"
  while IFS= read -r line || [[ -n "$line" ]]; do
    key="${line%%=*}"
    if [[ "$key" == "$requested_key" ]]; then
      printf '%s=%s\n' "$requested_key" "$requested_value" >>"$rewritten_file"
      found=1
    else
      printf '%s\n' "$line" >>"$rewritten_file"
    fi
  done <"$metadata_file"
  ((found)) || fail "Metadata key not found: $requested_key"
  mv -- "$rewritten_file" "$metadata_file"
}

remove_metadata_key() {
  local metadata_file="$1"
  local requested_key="$2"
  local rewritten_file="$metadata_file.rewrite"
  local line
  local key
  local found=0

  : >"$rewritten_file"
  while IFS= read -r line || [[ -n "$line" ]]; do
    key="${line%%=*}"
    if [[ "$key" == "$requested_key" ]]; then
      found=1
      continue
    fi
    printf '%s\n' "$line" >>"$rewritten_file"
  done <"$metadata_file"
  ((found)) || fail "Metadata key not found: $requested_key"
  mv -- "$rewritten_file" "$metadata_file"
}

assert_no_staging_directory() {
  local temporary_root="$1"
  local -a staging_paths=()

  shopt -s nullglob
  staging_paths=("$temporary_root"/chito-skill-installer.*)
  shopt -u nullglob
  ((${#staging_paths[@]} == 0)) || fail "Temporary staging was created under unsafe TMPDIR: $temporary_root"
}

snapshot_ids() {
  local backup_root="$1"
  local path

  [[ -d "$backup_root" ]] || return 0
  shopt -s nullglob
  for path in "$backup_root"/*; do
    [[ -d "$path" && ! -L "$path" ]] || continue
    printf '%s\n' "${path##*/}"
  done
}

only_snapshot_id() {
  local backup_root="$1"
  local -a ids=()

  mapfile -t ids < <(snapshot_ids "$backup_root")
  ((${#ids[@]} == 1)) || fail "Expected one snapshot in $backup_root, found ${#ids[@]}"
  printf '%s\n' "${ids[0]}"
}

assert_snapshot_count() {
  local backup_root="$1"
  local expected="$2"
  local -a ids=()

  mapfile -t ids < <(snapshot_ids "$backup_root")
  ((${#ids[@]} == expected)) || \
    fail "Expected $expected snapshots in $backup_root, found ${#ids[@]}"
}

find_snapshot_by_reason() {
  local backup_root="$1"
  local reason="$2"
  local id

  while IFS= read -r id; do
    if [[ "$(metadata_value "$backup_root/$id/metadata" reason)" == "$reason" ]]; then
      printf '%s\n' "$id"
      return 0
    fi
  done < <(snapshot_ids "$backup_root")
  return 1
}

clone_fixture_repository() {
  local source_repository="$1"
  local destination="$2"

  git clone --quiet "$source_repository" "$destination"
}

commit_fixture_change() {
  local repository="$1"
  local message="$2"

  git -C "$repository" add -A
  git -C "$repository" -c user.name="Installer Test" -c user.email="installer@example.invalid" \
    commit --quiet -m "$message"
}

create_absent_snapshot() {
  local backup_root="$1"
  local snapshot_id="$2"
  local created_utc="$3"

  mkdir -p -- "$backup_root/$snapshot_id"
  printf '%s\n' \
    'format_version=1' \
    "id=$snapshot_id" \
    "created_utc=$created_utc" \
    'reason=install' \
    'source_commit=unknown' \
    'opencode_existed=0' \
    'claude_existed=0' \
    'opencode_skill_count=0' \
    'claude_skill_count=0' \
    >"$backup_root/$snapshot_id/metadata"
}

create_legacy_snapshot() {
  local backup_root="$1"
  local snapshot_id="$2"
  local opencode_source="$3"
  local claude_source="$4"

  mkdir -p -- "$backup_root/$snapshot_id"
  cp -a -- "$opencode_source" "$backup_root/$snapshot_id/opencode"
  cp -a -- "$claude_source" "$backup_root/$snapshot_id/claude"
  printf '%s\n' \
    'format_version=1' \
    "id=$snapshot_id" \
    'created_utc=2026-08-31T12:00:00Z' \
    'reason=install' \
    'source_commit=unknown' \
    'opencode_existed=1' \
    'claude_existed=1' \
    'opencode_skill_count=1' \
    'claude_skill_count=1' \
    >"$backup_root/$snapshot_id/metadata"
}

test_non_tty_no_argument_safety() {
  local repository="$1"
  local fixture="$TEST_ROOT/no-args"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local agy_target="$fixture/agy/skills"
  local backup_root="$fixture/BKOld"
  local output

  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"
  prepare_original_target "$agy_target" "original-agy"

  if output="$(run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" 2>&1)"; then
    fail "No-argument non-TTY invocation unexpectedly performed an action"
  fi

  assert_contains "$output" "Usage:"
  assert_file "$opencode_target/stale-file.txt"
  assert_file "$claude_target/stale-file.txt"
  assert_file "$agy_target/stale-file.txt"
  assert_absent "$backup_root"
}

test_install_creates_bkold_snapshot_and_list_metadata() {
  local repository="$1"
  local fixture="$TEST_ROOT/install"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local agy_target="$fixture/agy/skills"
  local backup_root="$fixture/BKOld"
  local output
  local list_output
  local snapshot_id
  local source_commit
  local created_utc

  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"
  prepare_original_target "$agy_target" "original-agy"
  source_commit="$(git -C "$repository" rev-parse HEAD)"

  if ! output="$(run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" install 2>&1)"; then
    fail "Install command failed unexpectedly: $output"
  fi
  snapshot_id="$(only_snapshot_id "$backup_root")"
  created_utc="$(metadata_value "$backup_root/$snapshot_id/metadata" created_utc)"

  assert_dir "$backup_root/$snapshot_id"
  assert_file "$backup_root/$snapshot_id/metadata"
  assert_file "$backup_root/$snapshot_id/opencode/stale-file.txt"
  assert_file "$backup_root/$snapshot_id/opencode/old-skill/nested/complete-tree.txt"
  assert_file "$backup_root/$snapshot_id/claude/stale-file.txt"
  assert_file "$backup_root/$snapshot_id/agy/stale-file.txt"
  [[ "$snapshot_id" =~ ^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$ ]] || fail "Unsafe snapshot ID: $snapshot_id"
  [[ "$created_utc" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]] || \
    fail "Unexpected UTC timestamp: $created_utc"
  [[ "$(metadata_value "$backup_root/$snapshot_id/metadata" reason)" == "install" ]] || fail "Missing install reason"
  [[ "$(metadata_value "$backup_root/$snapshot_id/metadata" format_version)" == "2" ]] || fail "New snapshot schema version is wrong"
  [[ "$(metadata_value "$backup_root/$snapshot_id/metadata" opencode_captured)" == "1" ]] || fail "OpenCode capture metadata is wrong"
  [[ "$(metadata_value "$backup_root/$snapshot_id/metadata" claude_captured)" == "1" ]] || fail "Claude capture metadata is wrong"
  [[ "$(metadata_value "$backup_root/$snapshot_id/metadata" agy_captured)" == "1" ]] || fail "AGY capture metadata is wrong"
  [[ "$(metadata_value "$backup_root/$snapshot_id/metadata" opencode_existed)" == "1" ]] || fail "OpenCode existence metadata is wrong"
  [[ "$(metadata_value "$backup_root/$snapshot_id/metadata" claude_existed)" == "1" ]] || fail "Claude existence metadata is wrong"
  [[ "$(metadata_value "$backup_root/$snapshot_id/metadata" opencode_skill_count)" == "1" ]] || fail "OpenCode skill count is wrong"
  [[ "$(metadata_value "$backup_root/$snapshot_id/metadata" claude_skill_count)" == "1" ]] || fail "Claude skill count is wrong"
  [[ "$(metadata_value "$backup_root/$snapshot_id/metadata" agy_existed)" == "1" ]] || fail "AGY existence metadata is wrong"
  [[ "$(metadata_value "$backup_root/$snapshot_id/metadata" agy_skill_count)" == "1" ]] || fail "AGY skill count is wrong"
  [[ "$(metadata_value "$backup_root/$snapshot_id/metadata" source_commit)" == "$source_commit" ]] || fail "Source commit metadata is wrong"

  assert_file "$opencode_target/alpha/SKILL.md"
  assert_file "$opencode_target/beta/SKILL.md"
  assert_file "$claude_target/alpha/SKILL.md"
  assert_file "$claude_target/beta/SKILL.md"
  assert_file "$agy_target/alpha/SKILL.md"
  assert_file "$agy_target/beta/SKILL.md"
  assert_absent "$opencode_target/stale-file.txt"
  assert_absent "$claude_target/stale-file.txt"
  assert_absent "$agy_target/stale-file.txt"
  assert_contains "$output" "Backup snapshot: $snapshot_id"
  assert_contains "$output" "Source commit: $source_commit"
  assert_contains "$output" "OpenCode target: $opencode_target (source: OPENCODE_SKILLS_DIR)"
  assert_contains "$output" "Claude target: $claude_target (source: CLAUDE_SKILLS_DIR)"
  assert_contains "$output" "AGY target: $agy_target (source: AGY_SKILLS_DIR)"

  list_output="$(run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" list 2>&1)"
  assert_contains "$list_output" "$snapshot_id"
  assert_contains "$list_output" "$created_utc"
  assert_contains "$list_output" "install"
  assert_contains "$list_output" "yes"
  assert_contains "$list_output" "AGY yes"
  assert_contains "$list_output" "1/1/1"
  assert_contains "$list_output" "$source_commit"
}

test_successful_restore_and_pre_restore_snapshot() {
  local repository="$1"
  local fixture="$TEST_ROOT/restore"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local agy_target="$fixture/agy/skills"
  local backup_root="$fixture/BKOld"
  local selected_id
  local safety_id
  local output

  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"
  prepare_original_target "$agy_target" "original-agy"
  run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" install >/dev/null
  selected_id="$(only_snapshot_id "$backup_root")"

  printf 'current-opencode\n' >"$opencode_target/current-file.txt"
  printf 'current-claude\n' >"$claude_target/current-file.txt"
  printf 'current-agy\n' >"$agy_target/current-file.txt"
  output="$(run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" restore "$selected_id" 2>&1)"

  assert_file "$opencode_target/old-skill/SKILL.md"
  assert_file "$opencode_target/old-skill/nested/complete-tree.txt"
  assert_file "$claude_target/old-skill/SKILL.md"
  assert_file "$agy_target/old-skill/SKILL.md"
  assert_absent "$opencode_target/alpha"
  assert_absent "$claude_target/alpha"
  assert_absent "$agy_target/alpha"
  assert_absent "$opencode_target/current-file.txt"
  assert_absent "$claude_target/current-file.txt"
  assert_absent "$agy_target/current-file.txt"
  assert_snapshot_count "$backup_root" 2
  safety_id="$(find_snapshot_by_reason "$backup_root" "pre-restore")"
  [[ "$safety_id" != "$selected_id" ]] || fail "Restore safety snapshot reused the selected snapshot"
  assert_file "$backup_root/$safety_id/opencode/alpha/SKILL.md"
  assert_file "$backup_root/$safety_id/opencode/current-file.txt"
  assert_file "$backup_root/$safety_id/claude/current-file.txt"
  assert_file "$backup_root/$safety_id/agy/alpha/SKILL.md"
  assert_file "$backup_root/$safety_id/agy/current-file.txt"
  assert_contains "$output" "Restored snapshot: $selected_id"
  assert_contains "$output" "Pre-restore safety snapshot: $safety_id"
}

test_restore_recreates_absent_target_state() {
  local repository="$1"
  local fixture="$TEST_ROOT/restore-absent"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local agy_target="$fixture/agy/skills"
  local backup_root="$fixture/BKOld"
  local selected_id

  prepare_original_target "$opencode_target" "original-opencode"
  assert_absent "$claude_target"
  assert_absent "$agy_target"
  run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" install >/dev/null
  selected_id="$(only_snapshot_id "$backup_root")"
  [[ "$(metadata_value "$backup_root/$selected_id/metadata" claude_existed)" == "0" ]] || fail "Absent Claude target was not recorded"
  [[ "$(metadata_value "$backup_root/$selected_id/metadata" agy_existed)" == "0" ]] || fail "Absent AGY target was not recorded"

  run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" restore "$selected_id" >/dev/null

  assert_file "$opencode_target/stale-file.txt"
  assert_absent "$opencode_target/alpha"
  assert_absent "$claude_target"
  assert_absent "$agy_target"
  assert_snapshot_count "$backup_root" 2
}

test_install_rollback_retains_snapshot() {
  local repository="$1"
  local fixture="$TEST_ROOT/install-rollback"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local agy_target="$fixture/agy/skills"
  local backup_root="$fixture/BKOld"
  local output
  local snapshot_id

  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"
  prepare_original_target "$agy_target" "original-agy"

  if output="$(run_installer_with_failpoint "after-opencode-install" \
    "$repository" "$opencode_target" "$claude_target" "$backup_root" install 2>&1)"; then
    fail "Injected installation failure unexpectedly succeeded"
  fi

  assert_file "$opencode_target/stale-file.txt"
  assert_file "$claude_target/stale-file.txt"
  assert_file "$agy_target/stale-file.txt"
  assert_absent "$opencode_target/alpha"
  assert_absent "$claude_target/alpha"
  assert_absent "$agy_target/alpha"
  snapshot_id="$(only_snapshot_id "$backup_root")"
  assert_file "$backup_root/$snapshot_id/opencode/stale-file.txt"
  assert_file "$backup_root/$snapshot_id/claude/stale-file.txt"
  assert_file "$backup_root/$snapshot_id/agy/stale-file.txt"
  assert_contains "$output" "Rollback completed. All original targets were restored."
}

test_restore_rollback_retains_current_state_and_safety_snapshot() {
  local repository="$1"
  local fixture="$TEST_ROOT/restore-rollback"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local agy_target="$fixture/agy/skills"
  local backup_root="$fixture/BKOld"
  local selected_id
  local safety_id
  local output

  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"
  prepare_original_target "$agy_target" "original-agy"
  run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" install >/dev/null
  selected_id="$(only_snapshot_id "$backup_root")"
  printf 'keep-opencode\n' >"$opencode_target/current-file.txt"
  printf 'keep-claude\n' >"$claude_target/current-file.txt"
  printf 'keep-agy\n' >"$agy_target/current-file.txt"

  if output="$(run_installer_with_failpoint "after-opencode-restore" \
    "$repository" "$opencode_target" "$claude_target" "$backup_root" restore "$selected_id" 2>&1)"; then
    fail "Injected restore failure unexpectedly succeeded"
  fi

  assert_file "$opencode_target/alpha/SKILL.md"
  assert_file "$claude_target/alpha/SKILL.md"
  assert_file "$agy_target/alpha/SKILL.md"
  assert_file "$opencode_target/current-file.txt"
  assert_file "$claude_target/current-file.txt"
  assert_file "$agy_target/current-file.txt"
  assert_absent "$opencode_target/old-skill"
  assert_absent "$claude_target/old-skill"
  assert_absent "$agy_target/old-skill"
  assert_snapshot_count "$backup_root" 2
  safety_id="$(find_snapshot_by_reason "$backup_root" "pre-restore")"
  assert_file "$backup_root/$safety_id/opencode/current-file.txt"
  assert_file "$backup_root/$safety_id/claude/current-file.txt"
  assert_file "$backup_root/$safety_id/agy/current-file.txt"
  assert_contains "$output" "Rollback completed. All original targets were restored."
}

test_install_rollback_uses_quarantined_originals() {
  local repository="$1"
  local fixture="$TEST_ROOT/install-quarantine-fidelity"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local agy_target="$fixture/agy/skills"
  local backup_root="$fixture/BKOld"
  local snapshot_id
  local output

  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"
  prepare_original_target "$agy_target" "original-agy"

  if output="$(run_installer_with_failpoint "quarantine-fidelity-install" \
    "$repository" "$opencode_target" "$claude_target" "$backup_root" install 2>&1)"; then
    fail "Injected quarantine-fidelity install failure unexpectedly succeeded"
  fi

  snapshot_id="$(only_snapshot_id "$backup_root")"
  assert_absent "$backup_root/$snapshot_id/opencode/.quarantine-fidelity"
  assert_absent "$backup_root/$snapshot_id/claude/.quarantine-fidelity"
  assert_absent "$backup_root/$snapshot_id/agy/.quarantine-fidelity"
  assert_file "$opencode_target/.quarantine-fidelity"
  assert_file "$claude_target/.quarantine-fidelity"
  assert_file "$agy_target/.quarantine-fidelity"
  assert_file "$opencode_target/stale-file.txt"
  assert_file "$claude_target/stale-file.txt"
  assert_file "$agy_target/stale-file.txt"
  assert_contains "$output" "Rollback completed. All original targets were restored."
}

test_restore_rollback_uses_quarantined_originals() {
  local repository="$1"
  local fixture="$TEST_ROOT/restore-quarantine-fidelity"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local agy_target="$fixture/agy/skills"
  local backup_root="$fixture/BKOld"
  local selected_id
  local safety_id
  local output

  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"
  prepare_original_target "$agy_target" "original-agy"
  run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" install >/dev/null
  selected_id="$(only_snapshot_id "$backup_root")"
  printf 'current-opencode\n' >"$opencode_target/current-file.txt"
  printf 'current-claude\n' >"$claude_target/current-file.txt"
  printf 'current-agy\n' >"$agy_target/current-file.txt"

  if output="$(run_installer_with_failpoint "quarantine-fidelity-restore" \
    "$repository" "$opencode_target" "$claude_target" "$backup_root" restore "$selected_id" 2>&1)"; then
    fail "Injected quarantine-fidelity restore failure unexpectedly succeeded"
  fi

  safety_id="$(find_snapshot_by_reason "$backup_root" "pre-restore")"
  assert_absent "$backup_root/$safety_id/opencode/.quarantine-fidelity"
  assert_absent "$backup_root/$safety_id/claude/.quarantine-fidelity"
  assert_absent "$backup_root/$safety_id/agy/.quarantine-fidelity"
  assert_file "$opencode_target/.quarantine-fidelity"
  assert_file "$claude_target/.quarantine-fidelity"
  assert_file "$agy_target/.quarantine-fidelity"
  assert_file "$opencode_target/current-file.txt"
  assert_file "$claude_target/current-file.txt"
  assert_file "$agy_target/current-file.txt"
  assert_contains "$output" "Rollback completed. All original targets were restored."
}

test_install_rollback_preserves_absent_agy_after_boundary() {
  local repository="$1"
  local fixture="$TEST_ROOT/install-rollback-absent-agy"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local agy_target="$fixture/agy/skills"
  local backup_root="$fixture/BKOld"
  local snapshot_id
  local output

  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"
  assert_absent "$agy_target"

  if output="$(run_installer_with_failpoint "after-agy-install" \
    "$repository" "$opencode_target" "$claude_target" "$backup_root" install 2>&1)"; then
    fail "Injected failure with originally absent AGY unexpectedly succeeded"
  fi

  snapshot_id="$(only_snapshot_id "$backup_root")"
  [[ "$(metadata_value "$backup_root/$snapshot_id/metadata" agy_existed)" == "0" ]] || fail "Snapshot did not retain absent AGY state"
  assert_file "$opencode_target/stale-file.txt"
  assert_file "$claude_target/stale-file.txt"
  assert_absent "$agy_target"
  assert_contains "$output" "Rollback completed. All original targets were restored."
}

test_dry_run_aliases_have_no_mutation() {
  local repository="$1"
  local fixture="$TEST_ROOT/dry-run"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local agy_target="$fixture/agy/skills"
  local backup_root="$fixture/BKOld"
  local output

  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"
  prepare_original_target "$agy_target" "original-agy"

  output="$(run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" dry-run 2>&1)"
  assert_contains "$output" "Dry run complete"
  output="$(run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" --dry-run 2>&1)"

  assert_file "$opencode_target/stale-file.txt"
  assert_file "$claude_target/stale-file.txt"
  assert_file "$agy_target/stale-file.txt"
  assert_absent "$opencode_target/alpha"
  assert_absent "$claude_target/alpha"
  assert_absent "$agy_target/alpha"
  assert_absent "$backup_root"
  assert_contains "$output" "Dry run complete"
}

assert_unsafe_tmpdir_rejected_without_mutation() {
  local repository="$1"
  local fixture="$2"
  local temporary_root="$3"
  local home="$4"
  local expected_message="$5"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local agy_target="$fixture/agy/skills"
  local backup_root="$fixture/BKOld"
  local output

  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"
  prepare_original_target "$agy_target" "original-agy"
  mkdir -p -- "$temporary_root" "$home"
  printf 'temporary-root-marker\n' >"$temporary_root/temporary-root-marker.txt"

  if output="$(run_installer_with_tmpdir "$repository" "$opencode_target" "$claude_target" "$agy_target" \
    "$backup_root" "$temporary_root" "$home" dry-run 2>&1)"; then
    fail "Unsafe TMPDIR unexpectedly accepted: $temporary_root"
  fi

  assert_contains "$output" "$expected_message"
  assert_file "$opencode_target/stale-file.txt"
  assert_file "$claude_target/stale-file.txt"
  assert_file "$agy_target/stale-file.txt"
  assert_file "$temporary_root/temporary-root-marker.txt"
  assert_absent "$opencode_target/alpha"
  assert_absent "$claude_target/alpha"
  assert_absent "$agy_target/alpha"
  assert_no_staging_directory "$temporary_root"
}

test_rejects_tmpdir_equal_to_agy_target() {
  local repository="$1"
  local fixture="$TEST_ROOT/tmpdir-agy"

  assert_unsafe_tmpdir_rejected_without_mutation "$repository" "$fixture" \
    "$fixture/agy/skills" "$fixture/home" "Temporary root overlaps managed or protected destination"
}

test_rejects_tmpdir_equal_to_backup_root() {
  local repository="$1"
  local fixture="$TEST_ROOT/tmpdir-backup"

  assert_unsafe_tmpdir_rejected_without_mutation "$repository" "$fixture" \
    "$fixture/BKOld" "$fixture/home" "Temporary root overlaps managed or protected destination"
}

test_rejects_tmpdir_inside_protected_antigravity_path() {
  local repository="$1"
  local fixture="$TEST_ROOT/tmpdir-antigravity"
  local home="$fixture/home"

  assert_unsafe_tmpdir_rejected_without_mutation "$repository" "$fixture" \
    "$home/.gemini/antigravity-cli/skills/nested" "$home" \
    "Temporary root overlaps managed or protected destination"
}

test_rejects_tmpdir_inside_codegraph() {
  local repository="$1"
  local fixture="$TEST_ROOT/tmpdir-codegraph"

  assert_unsafe_tmpdir_rejected_without_mutation "$repository" "$fixture" \
    "$fixture/cache/.codegraph/tmp" "$fixture/home" "Temporary root cannot be inside a .codegraph directory"
}

test_rejects_unsafe_backup_id() {
  local repository="$1"
  local fixture="$TEST_ROOT/unsafe-id"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local backup_root="$fixture/BKOld"
  local output

  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"

  if output="$(run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" restore "../escape" 2>&1)"; then
    fail "Unsafe backup ID unexpectedly succeeded"
  fi

  assert_contains "$output" "Unsafe backup ID"
  assert_file "$opencode_target/stale-file.txt"
  assert_file "$claude_target/stale-file.txt"
  assert_absent "$backup_root"
}

test_rejects_unvalidated_source_before_snapshot() {
  local repository="$1"
  local fixture="$TEST_ROOT/source-validation"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local backup_root="$fixture/BKOld"
  local output

  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"

  if output="$(env \
    SKILL_PACK_REPOSITORY_URL="file://$repository" \
    SKILL_PACK_BRANCH="main" \
    SKILL_PACK_MIN_SKILL_COUNT="3" \
    OPENCODE_SKILLS_DIR="$opencode_target" \
    CLAUDE_SKILLS_DIR="$claude_target" \
    AGY_SKILLS_DIR="$fixture/agy/skills" \
    SKILL_BACKUP_DIR="$backup_root" \
    SKILL_INSTALLER_LOCK_FILE="$fixture/installer.lock" \
    SKILL_INSTALLER_STATE_FILE="$fixture/installer.state" \
    "$INSTALLER" install 2>&1)"; then
    fail "An undersized source unexpectedly passed validation"
  fi

  assert_contains "$output" "minimum is 3"
  assert_file "$opencode_target/stale-file.txt"
  assert_file "$claude_target/stale-file.txt"
  assert_absent "$backup_root"
}

test_committed_cleanup_failure_is_nonfatal() {
  local repository="$1"
  local fixture="$TEST_ROOT/cleanup-boundary"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local agy_target="$fixture/agy/skills"
  local backup_root="$fixture/BKOld"
  local output
  local snapshot_id
  local -a quarantines=()

  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"
  prepare_original_target "$agy_target" "original-agy"

  if ! output="$(run_installer_with_failpoint "quarantine-cleanup" \
    "$repository" "$opencode_target" "$claude_target" "$backup_root" install 2>&1)"; then
    fail "Committed install reported failure during non-fatal quarantine cleanup: $output"
  fi

  assert_file "$opencode_target/alpha/SKILL.md"
  assert_file "$claude_target/alpha/SKILL.md"
  assert_file "$agy_target/alpha/SKILL.md"
  snapshot_id="$(only_snapshot_id "$backup_root")"
  assert_file "$backup_root/$snapshot_id/opencode/stale-file.txt"
  assert_file "$backup_root/$snapshot_id/claude/stale-file.txt"
  assert_file "$backup_root/$snapshot_id/agy/stale-file.txt"
  assert_contains "$output" "committed successfully"
  assert_contains "$output" "quarantine cleanup"
  shopt -s nullglob
  quarantines=("$opencode_target".transaction.* "$claude_target".transaction.* "$agy_target".transaction.*)
  shopt -u nullglob
  ((${#quarantines[@]} == 3)) || fail "Expected all three retained quarantine paths after injected cleanup failure"
}

test_post_commit_reporting_failure_preserves_success() {
  local repository="$1"
  local fixture="$TEST_ROOT/post-commit-report"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local agy_target="$fixture/agy/skills"
  local backup_root="$fixture/BKOld"
  local output

  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"
  prepare_original_target "$agy_target" "original-agy"

  if ! output="$(run_installer_with_failpoint "post-commit-report" \
    "$repository" "$opencode_target" "$claude_target" "$backup_root" install 2>&1)"; then
    fail "Post-commit reporting failure produced a fatal command status: $output"
  fi

  assert_file "$opencode_target/alpha/SKILL.md"
  assert_file "$claude_target/alpha/SKILL.md"
  assert_file "$agy_target/alpha/SKILL.md"
  assert_absent "$opencode_target/stale-file.txt"
  assert_absent "$claude_target/stale-file.txt"
  assert_absent "$agy_target/stale-file.txt"
  assert_snapshot_count "$backup_root" 1
  [[ "$output" != *"Rollback completed"* ]] || fail "Committed targets were unexpectedly rolled back"
}

test_rejects_source_symlink_manifest() {
  local repository="$1"
  local fixture="$TEST_ROOT/source-symlink-manifest"
  local malicious_repository="$fixture/repository"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local backup_root="$fixture/BKOld"
  local output

  mkdir -p -- "$fixture"
  clone_fixture_repository "$repository" "$malicious_repository"
  rm -- "$malicious_repository/skills/alpha/SKILL.md"
  ln -s ../beta/SKILL.md "$malicious_repository/skills/alpha/SKILL.md"
  commit_fixture_change "$malicious_repository" "test: add symlink manifest"
  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"

  if output="$(run_installer "$malicious_repository" "$opencode_target" "$claude_target" "$backup_root" install 2>&1)"; then
    fail "Source symlink manifest unexpectedly installed"
  fi

  assert_contains "$output" "symbolic link"
  assert_file "$opencode_target/stale-file.txt"
  assert_file "$claude_target/stale-file.txt"
  assert_absent "$backup_root"
}

test_rejects_source_internal_symlink() {
  local repository="$1"
  local fixture="$TEST_ROOT/source-internal-symlink"
  local malicious_repository="$fixture/repository"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local backup_root="$fixture/BKOld"
  local output

  mkdir -p -- "$fixture"
  clone_fixture_repository "$repository" "$malicious_repository"
  ln -s /etc/passwd "$malicious_repository/skills/alpha/reference-link"
  commit_fixture_change "$malicious_repository" "test: add internal symlink"
  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"

  if output="$(run_installer "$malicious_repository" "$opencode_target" "$claude_target" "$backup_root" install 2>&1)"; then
    fail "Source internal symlink unexpectedly installed"
  fi

  assert_contains "$output" "symbolic link"
  assert_file "$opencode_target/stale-file.txt"
  assert_file "$claude_target/stale-file.txt"
  assert_absent "$backup_root"
}

test_rejects_source_codegraph_content() {
  local repository="$1"
  local fixture="$TEST_ROOT/source-codegraph"
  local malicious_repository="$fixture/repository"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local backup_root="$fixture/BKOld"
  local output

  mkdir -p -- "$fixture"
  clone_fixture_repository "$repository" "$malicious_repository"
  mkdir -p -- "$malicious_repository/skills/alpha/nested/.codegraph"
  printf 'protected\n' >"$malicious_repository/skills/alpha/nested/.codegraph/index"
  commit_fixture_change "$malicious_repository" "test: add protected codegraph content"
  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"

  if output="$(run_installer "$malicious_repository" "$opencode_target" "$claude_target" "$backup_root" install 2>&1)"; then
    fail "Source .codegraph content unexpectedly installed"
  fi

  assert_contains "$output" ".codegraph"
  assert_file "$opencode_target/stale-file.txt"
  assert_file "$claude_target/stale-file.txt"
  assert_absent "$backup_root"
}

test_rejects_non_regular_direct_manifest() {
  local repository="$1"
  local fixture="$TEST_ROOT/source-non-regular-manifest"
  local malicious_repository="$fixture/repository"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local backup_root="$fixture/BKOld"
  local output

  mkdir -p -- "$fixture"
  clone_fixture_repository "$repository" "$malicious_repository"
  mkdir -p -- "$malicious_repository/skills/gamma/SKILL.md"
  printf 'not-a-manifest\n' >"$malicious_repository/skills/gamma/SKILL.md/marker"
  commit_fixture_change "$malicious_repository" "test: add non-regular direct manifest"
  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"

  if output="$(run_installer "$malicious_repository" "$opencode_target" "$claude_target" "$backup_root" install 2>&1)"; then
    fail "Directory-valued direct manifest unexpectedly installed"
  fi

  assert_contains "$output" "not a regular non-symlink file"
  assert_file "$opencode_target/stale-file.txt"
  assert_file "$claude_target/stale-file.txt"
  assert_absent "$backup_root"
}

test_rejects_root_after_backup_normalization() {
  local repository="$1"
  local fixture="$TEST_ROOT/root-backup"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local output

  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"

  if output="$(env \
    SKILL_PACK_REPOSITORY_URL="file://$repository" \
    SKILL_PACK_BRANCH="main" \
    SKILL_PACK_MIN_SKILL_COUNT="2" \
    OPENCODE_SKILLS_DIR="$opencode_target" \
    CLAUDE_SKILLS_DIR="$claude_target" \
    AGY_SKILLS_DIR="$fixture/agy/skills" \
    SKILL_BACKUP_DIR="///" \
    SKILL_INSTALLER_LOCK_FILE="$fixture/installer.lock" \
    SKILL_INSTALLER_STATE_FILE="$fixture/installer.state" \
    "$INSTALLER" list 2>&1)"; then
    fail "Backup path /// unexpectedly normalized to an accepted root"
  fi

  assert_contains "$output" "filesystem root"
  assert_file "$opencode_target/stale-file.txt"
  assert_file "$claude_target/stale-file.txt"
}

test_install_refuses_held_lock() {
  local repository="$1"
  local fixture="$TEST_ROOT/install-lock"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local backup_root="$fixture/BKOld"
  local lock_path="$fixture/installer.lock"
  local output
  local lock_fd

  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"
  exec {lock_fd}>"$lock_path"
  flock -n "$lock_fd" || fail "Could not acquire test lock"

  if output="$(run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" install 2>&1)"; then
    fail "Install unexpectedly ignored an exclusive installer lock"
  fi

  flock -u "$lock_fd"
  exec {lock_fd}>&-
  assert_contains "$output" "Another install or restore operation"
  assert_file "$opencode_target/stale-file.txt"
  assert_file "$claude_target/stale-file.txt"
  assert_absent "$backup_root"
}

test_restore_refuses_held_lock() {
  local repository="$1"
  local fixture="$TEST_ROOT/restore-lock"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local backup_root="$fixture/BKOld"
  local lock_path="$fixture/installer.lock"
  local selected_id
  local output
  local lock_fd

  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"
  run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" install >/dev/null
  selected_id="$(only_snapshot_id "$backup_root")"
  printf 'retain\n' >"$opencode_target/current-file.txt"
  printf 'retain\n' >"$claude_target/current-file.txt"
  exec {lock_fd}>"$lock_path"
  flock -n "$lock_fd" || fail "Could not acquire test lock"

  if output="$(run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" restore "$selected_id" 2>&1)"; then
    fail "Restore unexpectedly ignored an exclusive installer lock"
  fi

  flock -u "$lock_fd"
  exec {lock_fd}>&-
  assert_contains "$output" "Another install or restore operation"
  assert_file "$opencode_target/alpha/SKILL.md"
  assert_file "$claude_target/alpha/SKILL.md"
  assert_file "$opencode_target/current-file.txt"
  assert_file "$claude_target/current-file.txt"
  assert_snapshot_count "$backup_root" 1
}

test_rejects_lock_path_inside_codegraph() {
  local repository="$1"
  local fixture="$TEST_ROOT/codegraph-lock"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local backup_root="$fixture/BKOld"
  local lock_path="$fixture/cache/.codegraph/installer.lock"
  local output

  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"
  mkdir -p -- "${lock_path%/*}"

  if output="$(env \
    SKILL_PACK_REPOSITORY_URL="file://$repository" \
    SKILL_PACK_BRANCH="main" \
    SKILL_PACK_MIN_SKILL_COUNT="2" \
    OPENCODE_SKILLS_DIR="$opencode_target" \
    CLAUDE_SKILLS_DIR="$claude_target" \
    AGY_SKILLS_DIR="$fixture/agy/skills" \
    SKILL_BACKUP_DIR="$backup_root" \
    SKILL_INSTALLER_LOCK_FILE="$lock_path" \
    SKILL_INSTALLER_STATE_FILE="$fixture/installer.state" \
    "$INSTALLER" install 2>&1)"; then
    fail "Lock path inside .codegraph unexpectedly accepted"
  fi

  assert_contains "$output" ".codegraph"
  assert_file "$opencode_target/stale-file.txt"
  assert_file "$claude_target/stale-file.txt"
  assert_absent "$backup_root"
  assert_absent "$lock_path"
}

test_help_invalid_command_and_missing_restore_id() {
  local repository="$1"
  local fixture="$TEST_ROOT/cli-errors"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local backup_root="$fixture/BKOld"
  local output

  output="$("$INSTALLER" help 2>&1)"
  assert_contains "$output" "Usage:"
  if output="$("$INSTALLER" invalid-command 2>&1)"; then
    fail "Invalid command unexpectedly succeeded"
  fi
  assert_contains "$output" "Unknown command"

  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"
  if output="$(run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" restore 2>&1)"; then
    fail "Missing restore ID unexpectedly succeeded without a TTY"
  fi
  assert_contains "$output" "restore requires BACKUP_ID"
  assert_file "$opencode_target/stale-file.txt"
  assert_file "$claude_target/stale-file.txt"
  assert_absent "$backup_root"
}

test_rejects_malformed_metadata_without_mutation() {
  local repository="$1"
  local fixture="$TEST_ROOT/malformed-metadata"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local backup_root="$fixture/BKOld"
  local selected_id
  local output

  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"
  run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" install >/dev/null
  selected_id="$(only_snapshot_id "$backup_root")"
  printf 'unexpected_key=value\n' >>"$backup_root/$selected_id/metadata"

  if output="$(run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" restore "$selected_id" 2>&1)"; then
    fail "Malformed snapshot metadata unexpectedly restored"
  fi

  assert_contains "$output" "Unknown metadata key"
  assert_file "$opencode_target/alpha/SKILL.md"
  assert_file "$claude_target/alpha/SKILL.md"
  assert_snapshot_count "$backup_root" 1
}

test_v1_rejects_v2_keys_even_when_empty() {
  local repository="$1"
  local forbidden_key
  local -a forbidden_keys=(opencode_captured claude_captured agy_captured agy_existed agy_skill_count)

  for forbidden_key in "${forbidden_keys[@]}"; do
    local fixture="$TEST_ROOT/v1-empty-$forbidden_key"
    local opencode_target="$fixture/opencode/skills"
    local claude_target="$fixture/claude/skills"
    local agy_target="$fixture/agy/skills"
    local backup_root="$fixture/BKOld"
    local legacy_opencode="$fixture/legacy-opencode"
    local legacy_claude="$fixture/legacy-claude"
    local snapshot_id="20260831T120000Z-a1b2c3d4"
    local output

    prepare_original_target "$legacy_opencode" "legacy-opencode"
    prepare_original_target "$legacy_claude" "legacy-claude"
    create_legacy_snapshot "$backup_root" "$snapshot_id" "$legacy_opencode" "$legacy_claude"
    printf '%s=\n' "$forbidden_key" >>"$backup_root/$snapshot_id/metadata"
    prepare_original_target "$opencode_target" "current-opencode"
    prepare_original_target "$claude_target" "current-claude"
    prepare_original_target "$agy_target" "current-agy"

    if output="$(run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" restore "$snapshot_id" 2>&1)"; then
      fail "Version 1 snapshot containing empty $forbidden_key unexpectedly restored"
    fi

    assert_contains "$output" "Version 1 snapshot contains unsupported key '$forbidden_key'"
    [[ "$(<"$opencode_target/stale-file.txt")" == "current-opencode" ]] || fail "Rejected version 1 metadata changed OpenCode"
    [[ "$(<"$claude_target/stale-file.txt")" == "current-claude" ]] || fail "Rejected version 1 metadata changed Claude"
    [[ "$(<"$agy_target/stale-file.txt")" == "current-agy" ]] || fail "Rejected version 1 metadata changed AGY"
    assert_snapshot_count "$backup_root" 1
  done
}

test_v2_requires_every_mandatory_target_key() {
  local repository="$1"
  local base_fixture="$TEST_ROOT/v2-missing-base"
  local base_backup_root="$base_fixture/BKOld"
  local base_snapshot_id
  local required_key
  local -a required_keys=(
    opencode_captured opencode_existed opencode_skill_count
    claude_captured claude_existed claude_skill_count
    agy_captured agy_existed agy_skill_count
  )

  prepare_original_target "$base_fixture/opencode/skills" "original-opencode"
  prepare_original_target "$base_fixture/claude/skills" "original-claude"
  prepare_original_target "$base_fixture/agy/skills" "original-agy"
  run_installer "$repository" "$base_fixture/opencode/skills" "$base_fixture/claude/skills" "$base_backup_root" install >/dev/null
  base_snapshot_id="$(only_snapshot_id "$base_backup_root")"

  for required_key in "${required_keys[@]}"; do
    local fixture="$TEST_ROOT/v2-missing-$required_key"
    local opencode_target="$fixture/opencode/skills"
    local claude_target="$fixture/claude/skills"
    local agy_target="$fixture/agy/skills"
    local backup_root="$fixture/BKOld"
    local output

    mkdir -p -- "$fixture"
    cp -a -- "$base_backup_root" "$backup_root"
    remove_metadata_key "$backup_root/$base_snapshot_id/metadata" "$required_key"
    prepare_original_target "$opencode_target" "current-opencode"
    prepare_original_target "$claude_target" "current-claude"
    prepare_original_target "$agy_target" "current-agy"

    if output="$(run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" restore "$base_snapshot_id" 2>&1)"; then
      fail "Version 2 snapshot missing $required_key unexpectedly restored"
    fi

    assert_contains "$output" "Missing required metadata key '$required_key' for version 2"
    assert_file "$opencode_target/stale-file.txt"
    assert_file "$claude_target/stale-file.txt"
    assert_file "$agy_target/stale-file.txt"
    assert_snapshot_count "$backup_root" 1
  done
}

test_rejects_inconsistent_metadata_for_every_target() {
  local repository="$1"
  local base_fixture="$TEST_ROOT/inconsistent-base"
  local base_backup_root="$base_fixture/BKOld"
  local base_snapshot_id
  local case_definition
  local -a cases=(
    'OpenCode|opencode_captured|0|opencode_existed|1'
    'OpenCode|opencode_existed|0|opencode_skill_count|999'
    'Claude|claude_captured|0|claude_existed|1'
    'Claude|claude_existed|0|claude_skill_count|999'
    'AGY|agy_captured|0|agy_existed|1'
    'AGY|agy_existed|0|agy_skill_count|999'
  )

  prepare_original_target "$base_fixture/opencode/skills" "original-opencode"
  prepare_original_target "$base_fixture/claude/skills" "original-claude"
  prepare_original_target "$base_fixture/agy/skills" "original-agy"
  run_installer "$repository" "$base_fixture/opencode/skills" "$base_fixture/claude/skills" "$base_backup_root" install >/dev/null
  base_snapshot_id="$(only_snapshot_id "$base_backup_root")"

  for case_definition in "${cases[@]}"; do
    local label
    local first_key
    local first_value
    local second_key
    local second_value
    local fixture
    local opencode_target
    local claude_target
    local agy_target
    local backup_root
    local output

    IFS='|' read -r label first_key first_value second_key second_value <<<"$case_definition"
    fixture="$TEST_ROOT/inconsistent-$first_key-$second_key"
    opencode_target="$fixture/opencode/skills"
    claude_target="$fixture/claude/skills"
    agy_target="$fixture/agy/skills"
    backup_root="$fixture/BKOld"
    mkdir -p -- "$fixture"
    cp -a -- "$base_backup_root" "$backup_root"
    set_metadata_value "$backup_root/$base_snapshot_id/metadata" "$first_key" "$first_value"
    set_metadata_value "$backup_root/$base_snapshot_id/metadata" "$second_key" "$second_value"
    prepare_original_target "$opencode_target" "current-opencode"
    prepare_original_target "$claude_target" "current-claude"
    prepare_original_target "$agy_target" "current-agy"

    if output="$(run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" restore "$base_snapshot_id" 2>&1)"; then
      fail "Inconsistent $label metadata unexpectedly restored"
    fi

    assert_contains "$output" "Inconsistent $label snapshot metadata"
    assert_file "$opencode_target/stale-file.txt"
    assert_file "$claude_target/stale-file.txt"
    assert_file "$agy_target/stale-file.txt"
  done
}

test_v2_consistent_uncaptured_targets_remain_unchanged() {
  local repository="$1"
  local source_fixture="$TEST_ROOT/v2-uncaptured-source"
  local source_backup_root="$source_fixture/BKOld"
  local fixture="$TEST_ROOT/v2-uncaptured-restore"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local agy_target="$fixture/agy/skills"
  local backup_root="$fixture/BKOld"
  local snapshot_id
  local target_name
  local list_output

  prepare_original_target "$source_fixture/opencode/skills" "snapshot-opencode"
  prepare_original_target "$source_fixture/claude/skills" "snapshot-claude"
  prepare_original_target "$source_fixture/agy/skills" "snapshot-agy"
  run_installer "$repository" "$source_fixture/opencode/skills" "$source_fixture/claude/skills" "$source_backup_root" install >/dev/null
  snapshot_id="$(only_snapshot_id "$source_backup_root")"
  mkdir -p -- "$fixture"
  cp -a -- "$source_backup_root" "$backup_root"

  for target_name in opencode claude agy; do
    set_metadata_value "$backup_root/$snapshot_id/metadata" "${target_name}_captured" 0
    set_metadata_value "$backup_root/$snapshot_id/metadata" "${target_name}_existed" 0
    set_metadata_value "$backup_root/$snapshot_id/metadata" "${target_name}_skill_count" 0
    rm -rf -- "$backup_root/$snapshot_id/$target_name"
  done

  prepare_original_target "$opencode_target" "current-opencode"
  prepare_original_target "$claude_target" "current-claude"
  prepare_original_target "$agy_target" "current-agy"
  if ! list_output="$(run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" list 2>&1)"; then
    fail "Consistent version 2 uncaptured target metadata was rejected: $list_output"
  fi
  assert_contains "$list_output" "OpenCode not captured"
  assert_contains "$list_output" "Claude not captured"
  assert_contains "$list_output" "AGY not captured"
  assert_contains "$list_output" "n/a/n/a/n/a"

  run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" restore "$snapshot_id" >/dev/null

  [[ "$(<"$opencode_target/stale-file.txt")" == "current-opencode" ]] || fail "Uncaptured OpenCode target changed"
  [[ "$(<"$claude_target/stale-file.txt")" == "current-claude" ]] || fail "Uncaptured Claude target changed"
  [[ "$(<"$agy_target/stale-file.txt")" == "current-agy" ]] || fail "Uncaptured AGY target changed"
  assert_snapshot_count "$backup_root" 2
}

test_rejects_symlink_backup_root_and_snapshot() {
  local repository="$1"
  local fixture="$TEST_ROOT/symlink-backups"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local backup_root="$fixture/BKOld"
  local actual_backup="$fixture/actual-backup"
  local snapshot_id="20260831T120000Z-a1b2c3d4"
  local output

  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"
  mkdir -p -- "$actual_backup"
  ln -s "$actual_backup" "$backup_root"
  if output="$(run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" list 2>&1)"; then
    fail "Symbolic-link backup root unexpectedly accepted"
  fi
  assert_contains "$output" "symbolic link"
  assert_file "$opencode_target/stale-file.txt"
  assert_file "$claude_target/stale-file.txt"

  rm -- "$backup_root"
  mkdir -p -- "$backup_root" "$fixture/outside-snapshot"
  ln -s "$fixture/outside-snapshot" "$backup_root/$snapshot_id"
  if output="$(run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" restore "$snapshot_id" 2>&1)"; then
    fail "Symbolic-link snapshot unexpectedly accepted"
  fi
  assert_contains "$output" "not found or unsafe"
  assert_file "$opencode_target/stale-file.txt"
  assert_file "$claude_target/stale-file.txt"
}

test_lists_backups_in_sortable_id_order() {
  local repository="$1"
  local fixture="$TEST_ROOT/list-order"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local backup_root="$fixture/BKOld"
  local earlier_id="20260831T120000Z-00000001"
  local later_id="20260831T130000Z-00000001"
  local output

  create_absent_snapshot "$backup_root" "$later_id" "2026-08-31T13:00:00Z"
  create_absent_snapshot "$backup_root" "$earlier_id" "2026-08-31T12:00:00Z"
  output="$(run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" list 2>&1)"

  [[ "$output" == *"$earlier_id"*"$later_id"* ]] || fail "Backups were not listed in sortable ID order"
}

test_uses_agy_global_path_and_protects_antigravity_cli() {
  local repository="$1"
  local fixture="$TEST_ROOT/agy-global-path"
  local home="$fixture/home"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local agy_target="$home/.gemini/config/skills"
  local protected_target="$home/.gemini/antigravity-cli/skills"
  local backup_root="$fixture/BKOld"
  local output

  mkdir -p -- "$protected_target"
  printf 'protected\n' >"$protected_target/marker.txt"

  env \
    HOME="$home" \
    SKILL_PACK_REPOSITORY_URL="file://$repository" \
    SKILL_PACK_BRANCH="main" \
    SKILL_PACK_MIN_SKILL_COUNT="2" \
    OPENCODE_SKILLS_DIR="$opencode_target" \
    CLAUDE_SKILLS_DIR="$claude_target" \
    SKILL_BACKUP_DIR="$backup_root" \
    SKILL_INSTALLER_LOCK_FILE="$fixture/installer.lock" \
    SKILL_INSTALLER_STATE_FILE="$fixture/installer.state" \
    "$INSTALLER" install >/dev/null

  assert_file "$agy_target/alpha/SKILL.md"
  [[ "$(<"$protected_target/marker.txt")" == "protected" ]] || fail "Default AGY install changed the protected antigravity-cli path"
  assert_absent "$protected_target/alpha"

  if output="$(env \
    HOME="$home" \
    SKILL_PACK_REPOSITORY_URL="file://$repository" \
    SKILL_PACK_BRANCH="main" \
    SKILL_PACK_MIN_SKILL_COUNT="2" \
    OPENCODE_SKILLS_DIR="$opencode_target" \
    CLAUDE_SKILLS_DIR="$claude_target" \
    AGY_SKILLS_DIR="$protected_target" \
    SKILL_BACKUP_DIR="$backup_root" \
    SKILL_INSTALLER_LOCK_FILE="$fixture/installer.lock" \
    SKILL_INSTALLER_STATE_FILE="$fixture/installer.state" \
    "$INSTALLER" install 2>&1)"; then
    fail "Protected antigravity-cli AGY target unexpectedly accepted"
  fi

  assert_contains "$output" "protected path"
  [[ "$(<"$protected_target/marker.txt")" == "protected" ]] || fail "Rejected AGY target changed the protected path"
  assert_absent "$protected_target/alpha"
}

test_rejects_agy_target_overlap() {
  local repository="$1"
  local fixture="$TEST_ROOT/agy-overlap"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local backup_root="$fixture/BKOld"
  local output

  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"

  if output="$(env \
    SKILL_PACK_REPOSITORY_URL="file://$repository" \
    SKILL_PACK_BRANCH="main" \
    SKILL_PACK_MIN_SKILL_COUNT="2" \
    OPENCODE_SKILLS_DIR="$opencode_target" \
    CLAUDE_SKILLS_DIR="$claude_target" \
    AGY_SKILLS_DIR="$opencode_target" \
    SKILL_BACKUP_DIR="$backup_root" \
    SKILL_INSTALLER_LOCK_FILE="$fixture/installer.lock" \
    SKILL_INSTALLER_STATE_FILE="$fixture/installer.state" \
    "$INSTALLER" install 2>&1)"; then
    fail "Overlapping AGY and OpenCode targets unexpectedly accepted"
  fi

  assert_contains "$output" "distinct and non-overlapping"
  assert_file "$opencode_target/stale-file.txt"
  assert_file "$claude_target/stale-file.txt"
  assert_absent "$backup_root"
}

test_install_rollback_after_agy_boundary() {
  local repository="$1"
  local fixture="$TEST_ROOT/install-agy-rollback"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local agy_target="$fixture/agy/skills"
  local backup_root="$fixture/BKOld"
  local snapshot_id
  local output

  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"
  prepare_original_target "$agy_target" "original-agy"

  if output="$(run_installer_with_failpoint "after-agy-install" \
    "$repository" "$opencode_target" "$claude_target" "$backup_root" install 2>&1)"; then
    fail "Injected failure after AGY installation unexpectedly succeeded"
  fi

  assert_file "$opencode_target/stale-file.txt"
  assert_file "$claude_target/stale-file.txt"
  assert_file "$agy_target/stale-file.txt"
  assert_absent "$opencode_target/alpha"
  assert_absent "$claude_target/alpha"
  assert_absent "$agy_target/alpha"
  snapshot_id="$(only_snapshot_id "$backup_root")"
  assert_file "$backup_root/$snapshot_id/agy/stale-file.txt"
  assert_contains "$output" "Rollback completed. All original targets were restored."
}

test_restore_rollback_after_agy_boundary() {
  local repository="$1"
  local fixture="$TEST_ROOT/restore-agy-rollback"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local agy_target="$fixture/agy/skills"
  local backup_root="$fixture/BKOld"
  local selected_id
  local safety_id
  local output

  prepare_original_target "$opencode_target" "original-opencode"
  prepare_original_target "$claude_target" "original-claude"
  prepare_original_target "$agy_target" "original-agy"
  run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" install >/dev/null
  selected_id="$(only_snapshot_id "$backup_root")"
  printf 'keep-opencode\n' >"$opencode_target/current-file.txt"
  printf 'keep-claude\n' >"$claude_target/current-file.txt"
  printf 'keep-agy\n' >"$agy_target/current-file.txt"

  if output="$(run_installer_with_failpoint "after-agy-restore" \
    "$repository" "$opencode_target" "$claude_target" "$backup_root" restore "$selected_id" 2>&1)"; then
    fail "Injected failure after AGY restoration unexpectedly succeeded"
  fi

  assert_file "$opencode_target/alpha/SKILL.md"
  assert_file "$claude_target/alpha/SKILL.md"
  assert_file "$agy_target/alpha/SKILL.md"
  assert_file "$opencode_target/current-file.txt"
  assert_file "$claude_target/current-file.txt"
  assert_file "$agy_target/current-file.txt"
  safety_id="$(find_snapshot_by_reason "$backup_root" "pre-restore")"
  assert_file "$backup_root/$safety_id/agy/current-file.txt"
  assert_contains "$output" "Rollback completed. All original targets were restored."
}

test_legacy_snapshot_leaves_agy_not_captured() {
  local repository="$1"
  local fixture="$TEST_ROOT/legacy-snapshot"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local agy_target="$fixture/agy/skills"
  local backup_root="$fixture/BKOld"
  local legacy_opencode="$fixture/legacy-opencode"
  local legacy_claude="$fixture/legacy-claude"
  local selected_id="20260831T120000Z-a1b2c3d4"
  local safety_id
  local list_output

  prepare_original_target "$legacy_opencode" "legacy-opencode"
  prepare_original_target "$legacy_claude" "legacy-claude"
  create_legacy_snapshot "$backup_root" "$selected_id" "$legacy_opencode" "$legacy_claude"
  prepare_original_target "$opencode_target" "current-opencode"
  prepare_original_target "$claude_target" "current-claude"
  prepare_original_target "$agy_target" "current-agy"
  printf 'agy-must-remain\n' >"$agy_target/unchanged.txt"

  list_output="$(run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" list 2>&1)"
  assert_contains "$list_output" "AGY not captured"
  assert_contains "$list_output" "1/1/n/a"
  run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" restore "$selected_id" >/dev/null

  assert_file "$opencode_target/old-skill/SKILL.md"
  assert_file "$claude_target/old-skill/SKILL.md"
  [[ "$(<"$opencode_target/stale-file.txt")" == "legacy-opencode" ]] || fail "Legacy OpenCode state was not restored"
  [[ "$(<"$claude_target/stale-file.txt")" == "legacy-claude" ]] || fail "Legacy Claude state was not restored"
  [[ "$(<"$agy_target/stale-file.txt")" == "current-agy" ]] || fail "Legacy restore changed current AGY state"
  assert_file "$agy_target/unchanged.txt"
  safety_id="$(find_snapshot_by_reason "$backup_root" "pre-restore")"
  [[ "$(metadata_value "$backup_root/$safety_id/metadata" agy_existed)" == "1" ]] || fail "Pre-restore snapshot did not capture AGY"
  assert_file "$backup_root/$safety_id/agy/unchanged.txt"
}

test_runtime_targets_prefer_explicit_overrides() {
  local repository="$1"
  local fixture="$TEST_ROOT/runtime-explicit-overrides"
  local home="$fixture/home"
  local backup_root="$fixture/BKOld"
  local opencode_target="$fixture/explicit-opencode/skills"
  local claude_target="$fixture/explicit-claude/skills"
  local agy_target="$fixture/explicit-agy/skills"
  local output

  output="$(run_autodetect_dry_run "$repository" "$home" "$backup_root" \
    OPENCODE_SKILLS_DIR="$opencode_target" \
    OPENCODE_CONFIG_DIR="$fixture/ignored-opencode-config" \
    XDG_CONFIG_HOME="$fixture/ignored-xdg" \
    CLAUDE_SKILLS_DIR="$claude_target" \
    CLAUDE_CONFIG_DIR="$fixture/ignored-claude-config" \
    AGY_SKILLS_DIR="$agy_target" 2>&1)"

  assert_contains "$output" "OpenCode target: $opencode_target (source: OPENCODE_SKILLS_DIR)"
  assert_contains "$output" "Claude target: $claude_target (source: CLAUDE_SKILLS_DIR)"
  assert_contains "$output" "AGY target: $agy_target (source: AGY_SKILLS_DIR)"
  [[ "$output" != *"$fixture/ignored-opencode-config/skills"* ]] || fail "OpenCode explicit override did not win"
  [[ "$output" != *"$fixture/ignored-xdg/opencode/skills"* ]] || fail "OpenCode XDG path unexpectedly won"
  [[ "$output" != *"$fixture/ignored-claude-config/skills"* ]] || fail "Claude explicit override did not win"
  assert_absent "$opencode_target"
  assert_absent "$claude_target"
  assert_absent "$agy_target"
  assert_absent "$backup_root"
}

test_runtime_targets_prefer_opencode_config_before_xdg() {
  local repository="$1"
  local fixture="$TEST_ROOT/runtime-opencode-config"
  local home="$fixture/home"
  local backup_root="$fixture/BKOld"
  local config_root="$fixture/opencode-config"
  local xdg_root="$fixture/xdg"
  local output

  output="$(run_autodetect_dry_run "$repository" "$home" "$backup_root" \
    OPENCODE_CONFIG_DIR="$config_root" \
    XDG_CONFIG_HOME="$xdg_root" 2>&1)"

  assert_contains "$output" "OpenCode target: $config_root/skills (source: OPENCODE_CONFIG_DIR)"
  [[ "$output" != *"$xdg_root/opencode/skills"* ]] || fail "XDG path unexpectedly won over OPENCODE_CONFIG_DIR"
  assert_absent "$config_root"
  assert_absent "$xdg_root"
  assert_absent "$backup_root"
}

test_runtime_targets_use_xdg_opencode_path() {
  local repository="$1"
  local fixture="$TEST_ROOT/runtime-opencode-xdg"
  local home="$fixture/home"
  local backup_root="$fixture/BKOld"
  local xdg_root="$fixture/xdg"
  local output

  output="$(run_autodetect_dry_run "$repository" "$home" "$backup_root" \
    XDG_CONFIG_HOME="$xdg_root" 2>&1)"

  assert_contains "$output" "OpenCode target: $xdg_root/opencode/skills (source: XDG_CONFIG_HOME)"
  assert_absent "$xdg_root"
  assert_absent "$backup_root"
}

test_runtime_target_defaults_use_alternate_home() {
  local repository="$1"
  local fixture="$TEST_ROOT/runtime-defaults"
  local home="$fixture/alternate-user-home"
  local backup_root="$fixture/BKOld"
  local output

  output="$(run_autodetect_dry_run "$repository" "$home" "$backup_root" 2>&1)"

  assert_contains "$output" "OpenCode target: $home/.config/opencode/skills (source: HOME default)"
  assert_contains "$output" "Claude target: $home/.claude/skills (source: HOME default)"
  assert_contains "$output" "AGY target: $home/.gemini/config/skills (source: HOME default)"
  assert_absent "$home/.config/opencode/skills"
  assert_absent "$home/.claude/skills"
  assert_absent "$home/.gemini/config/skills"
  assert_absent "$backup_root"
}

test_runtime_targets_use_claude_config_dir() {
  local repository="$1"
  local fixture="$TEST_ROOT/runtime-claude-config"
  local home="$fixture/home"
  local backup_root="$fixture/BKOld"
  local config_root="$fixture/claude-config"
  local output

  output="$(run_autodetect_dry_run "$repository" "$home" "$backup_root" \
    CLAUDE_CONFIG_DIR="$config_root" 2>&1)"

  assert_contains "$output" "Claude target: $config_root/skills (source: CLAUDE_CONFIG_DIR)"
  assert_absent "$config_root"
  assert_absent "$backup_root"
}

test_empty_runtime_variables_fall_back_to_defaults() {
  local repository="$1"
  local fixture="$TEST_ROOT/runtime-empty-fallback"
  local home="$fixture/home"
  local backup_root="$fixture/BKOld"
  local output

  output="$(run_autodetect_dry_run "$repository" "$home" "$backup_root" \
    OPENCODE_SKILLS_DIR= \
    OPENCODE_CONFIG_DIR= \
    XDG_CONFIG_HOME= \
    CLAUDE_SKILLS_DIR= \
    CLAUDE_CONFIG_DIR= \
    AGY_SKILLS_DIR= 2>&1)"

  assert_contains "$output" "OpenCode target: $home/.config/opencode/skills (source: HOME default)"
  assert_contains "$output" "Claude target: $home/.claude/skills (source: HOME default)"
  assert_contains "$output" "AGY target: $home/.gemini/config/skills (source: HOME default)"
  assert_absent "$backup_root"
}

test_relative_runtime_roots_fail_before_mutation() {
  local repository="$1"
  local fixture="$TEST_ROOT/runtime-relative-roots"
  local home="$fixture/home"
  local case_definition
  local -a cases=(
    'OPENCODE_SKILLS_DIR|OpenCode target|relative-opencode|relative-opencode'
    'OPENCODE_CONFIG_DIR|OpenCode target|relative-opencode|relative-opencode/skills'
    'XDG_CONFIG_HOME|OpenCode target|relative-xdg|relative-xdg/opencode/skills'
    'CLAUDE_SKILLS_DIR|Claude target|relative-claude|relative-claude'
    'CLAUDE_CONFIG_DIR|Claude target|relative-claude|relative-claude/skills'
    'AGY_SKILLS_DIR|AGY target|relative-agy|relative-agy'
  )

  for case_definition in "${cases[@]}"; do
    local variable
    local label
    local unsafe_input
    local unsafe_path
    local backup_root
    local output

    IFS='|' read -r variable label unsafe_input unsafe_path <<<"$case_definition"
    backup_root="$fixture/$variable/BKOld"
    if output="$(run_autodetect_dry_run "$repository" "$home" "$backup_root" \
      "$variable=$unsafe_input" 2>&1)"; then
      fail "Relative $variable unexpectedly succeeded"
    fi

    assert_contains "$output" "$label must be absolute: $unsafe_path"
    [[ "$output" != *"Cloning "* ]] || fail "Relative $variable reached source staging"
    assert_absent "$backup_root"
  done
}

state_value() {
  metadata_value "$1" "$2"
}

test_status_reports_never_installed() {
  local repository="$1"
  local fixture="$TEST_ROOT/version-never-installed"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local backup_root="$fixture/BKOld"
  local remote_commit
  local output

  remote_commit="$(git -C "$repository" rev-parse HEAD)"
  if ! output="$(run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" status 2>&1)"; then
    fail "Status command failed unexpectedly: $output"
  fi

  assert_contains "$output" "no previous install"
  assert_contains "$output" "$remote_commit"
  assert_absent "$backup_root"
  assert_absent "${backup_root%/*}/installer.state"
  assert_absent "$opencode_target"
  assert_absent "$claude_target"
}

test_install_writes_state_file() {
  local repository="$1"
  local fixture="$TEST_ROOT/version-state-file"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local backup_root="$fixture/BKOld"
  local state_file="${backup_root%/*}/installer.state"
  local source_commit
  local output

  source_commit="$(git -C "$repository" rev-parse HEAD)"
  if ! output="$(run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" install 2>&1)"; then
    fail "Install command failed unexpectedly: $output"
  fi

  assert_file "$state_file"
  [[ "$(state_value "$state_file" source_commit)" == "$source_commit" ]] || fail "State commit is wrong"
  [[ "$(state_value "$state_file" branch)" == "main" ]] || fail "State branch is wrong"
  [[ "$(state_value "$state_file" skill_count)" == "2" ]] || fail "State skill count is wrong"
  assert_contains "$(state_value "$state_file" repository_url)" "$repository"
}

test_install_is_idempotent_without_new_snapshot() {
  local repository="$1"
  local fixture="$TEST_ROOT/version-idempotent"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local backup_root="$fixture/BKOld"
  local output

  run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" install >/dev/null
  assert_snapshot_count "$backup_root" 1
  if ! output="$(run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" install 2>&1)"; then
    fail "Repeated install failed unexpectedly: $output"
  fi

  assert_contains "$output" "Already up to date"
  assert_snapshot_count "$backup_root" 1
  assert_file "$opencode_target/alpha/SKILL.md"
}

test_install_force_reinstalls_same_version() {
  local repository="$1"
  local fixture="$TEST_ROOT/version-force"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local backup_root="$fixture/BKOld"
  local output

  run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" install >/dev/null
  if ! output="$(run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" install --force 2>&1)"; then
    fail "Forced reinstall failed unexpectedly: $output"
  fi

  assert_contains "$output" "Force reinstall"
  assert_snapshot_count "$backup_root" 2
}

test_status_detects_newer_version_with_skill_diff() {
  local repository="$1"
  local fixture="$TEST_ROOT/version-status-diff"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local backup_root="$fixture/BKOld"
  local evolving_repository="$fixture/evolving-repo"
  local output

  run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" install >/dev/null
  clone_fixture_repository "$repository" "$evolving_repository"
  create_skill "$evolving_repository" "gamma"
  printf -- '---\nname: alpha\ndescription: Fixture skill modified\n---\n\n# alpha modified\n' \
    >"$evolving_repository/skills/alpha/SKILL.md"
  commit_fixture_change "$evolving_repository" "test: add gamma and modify alpha"

  if ! output="$(run_installer "$evolving_repository" "$opencode_target" "$claude_target" "$backup_root" status 2>&1)"; then
    fail "Status command failed unexpectedly: $output"
  fi

  assert_contains "$output" "newer skill-pack version"
  assert_contains "$output" "+ gamma"
  assert_contains "$output" "~ alpha"
  assert_snapshot_count "$backup_root" 1
  assert_absent "$opencode_target/gamma/SKILL.md"
}

test_install_upgrades_and_updates_state() {
  local repository="$1"
  local fixture="$TEST_ROOT/version-upgrade"
  local opencode_target="$fixture/opencode/skills"
  local claude_target="$fixture/claude/skills"
  local agy_target="$fixture/agy/skills"
  local backup_root="$fixture/BKOld"
  local state_file="${backup_root%/*}/installer.state"
  local evolving_repository="$fixture/evolving-repo"
  local new_commit
  local output
  local status_output

  run_installer "$repository" "$opencode_target" "$claude_target" "$backup_root" install >/dev/null
  clone_fixture_repository "$repository" "$evolving_repository"
  create_skill "$evolving_repository" "gamma"
  commit_fixture_change "$evolving_repository" "test: add gamma skill"
  new_commit="$(git -C "$evolving_repository" rev-parse HEAD)"

  if ! output="$(run_installer "$evolving_repository" "$opencode_target" "$claude_target" "$backup_root" install 2>&1)"; then
    fail "Upgrade install failed unexpectedly: $output"
  fi

  assert_snapshot_count "$backup_root" 2
  assert_file "$opencode_target/gamma/SKILL.md"
  assert_file "$claude_target/gamma/SKILL.md"
  assert_file "$agy_target/gamma/SKILL.md"
  [[ "$(state_value "$state_file" source_commit)" == "$new_commit" ]] || fail "State was not updated to the new commit"
  [[ "$(state_value "$state_file" skill_count)" == "3" ]] || fail "State skill count was not updated"

  status_output="$(run_installer "$evolving_repository" "$opencode_target" "$claude_target" "$backup_root" status 2>&1)"
  assert_contains "$status_output" "Already up to date"
}

main() {
  local repository
  local test_case
  local -a test_cases=(
    test_non_tty_no_argument_safety
    test_install_creates_bkold_snapshot_and_list_metadata
    test_successful_restore_and_pre_restore_snapshot
    test_restore_recreates_absent_target_state
    test_install_rollback_retains_snapshot
    test_restore_rollback_retains_current_state_and_safety_snapshot
    test_install_rollback_uses_quarantined_originals
    test_restore_rollback_uses_quarantined_originals
    test_install_rollback_preserves_absent_agy_after_boundary
    test_dry_run_aliases_have_no_mutation
    test_rejects_tmpdir_equal_to_agy_target
    test_rejects_tmpdir_equal_to_backup_root
    test_rejects_tmpdir_inside_protected_antigravity_path
    test_rejects_tmpdir_inside_codegraph
    test_rejects_unsafe_backup_id
    test_rejects_unvalidated_source_before_snapshot
    test_committed_cleanup_failure_is_nonfatal
    test_post_commit_reporting_failure_preserves_success
    test_rejects_source_symlink_manifest
    test_rejects_source_internal_symlink
    test_rejects_source_codegraph_content
    test_rejects_non_regular_direct_manifest
    test_rejects_root_after_backup_normalization
    test_install_refuses_held_lock
    test_restore_refuses_held_lock
    test_rejects_lock_path_inside_codegraph
    test_help_invalid_command_and_missing_restore_id
    test_rejects_malformed_metadata_without_mutation
    test_v1_rejects_v2_keys_even_when_empty
    test_v2_requires_every_mandatory_target_key
    test_rejects_inconsistent_metadata_for_every_target
    test_v2_consistent_uncaptured_targets_remain_unchanged
    test_rejects_symlink_backup_root_and_snapshot
    test_lists_backups_in_sortable_id_order
    test_uses_agy_global_path_and_protects_antigravity_cli
    test_rejects_agy_target_overlap
    test_install_rollback_after_agy_boundary
    test_restore_rollback_after_agy_boundary
    test_legacy_snapshot_leaves_agy_not_captured
    test_runtime_targets_prefer_explicit_overrides
    test_runtime_targets_prefer_opencode_config_before_xdg
    test_runtime_targets_use_xdg_opencode_path
    test_runtime_target_defaults_use_alternate_home
    test_runtime_targets_use_claude_config_dir
    test_empty_runtime_variables_fall_back_to_defaults
    test_relative_runtime_roots_fail_before_mutation
    test_status_reports_never_installed
    test_install_writes_state_file
    test_install_is_idempotent_without_new_snapshot
    test_install_force_reinstalls_same_version
    test_status_detects_newer_version_with_skill_diff
    test_install_upgrades_and_updates_state
  )

  TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/chito-skill-tests.XXXXXX")"
  trap 'rm -rf -- "$TEST_ROOT"' EXIT
  repository="$TEST_ROOT/source-repository"
  mkdir -p -- "$repository"
  git -C "$repository" init --quiet --initial-branch=main
  create_skill "$repository" "alpha"
  create_skill "$repository" "beta"
  git -C "$repository" add skills
  git -C "$repository" -c user.name="Installer Test" -c user.email="installer@example.invalid" \
    commit --quiet -m "test: add fixture skills"

  if [[ -n "${TEST_CASE:-}" ]]; then
    declare -F "$TEST_CASE" >/dev/null || fail "Unknown TEST_CASE: $TEST_CASE"
    "$TEST_CASE" "$repository"
    printf 'PASS: %s\n' "$TEST_CASE"
    return 0
  fi

  for test_case in "${test_cases[@]}"; do
    "$test_case" "$repository"
  done
  printf 'PASS: full isolated installer suite (%s cases)\n' "${#test_cases[@]}"
}

main "$@"
