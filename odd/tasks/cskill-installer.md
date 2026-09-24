# cskill Linux Terminal Installer — Task Plan

## Objective

Build `cskill`, an independent Go-based Linux terminal installer for the skill pack. It must safely install skills, inspect installation status, list and restore managed snapshots, and read and write the same backup metadata and installed-state formats as the existing Bash installer so either installer can restore snapshots created by the other.

## Why

The repository already has a Bash installer with mature path protections, transactional replacement, rollback, snapshot metadata, and state-file behavior. A separate Go terminal executable can provide a native terminal experience while preserving recoverability and interoperability. Reusing the established on-disk contracts avoids dividing users' backups or making rollback depend on which installer created them.

## Scope

- Build on the existing animation prototype and integrate it with the installer command flow.
- Validate configuration, destination paths, backup/state/lock paths, and the fetched skill source before mutation.
- Share Bash snapshot metadata format v1/v2 semantics and installed-state format; preserve v1 compatibility and write compatible current metadata.
- Add serialized install/restore transactions, complete snapshots, rollback, and lock behavior.
- Provide command-line UX for status, list, restore, and install, with safe noninteractive defaults.
- Verify cross-restore in both directions: Go-created snapshots restored by Bash, and Bash-created snapshots restored by Go. Verify Go state-file interoperability in both directions where applicable.
- Document Go installation/use and explicitly document interoperability and safety behavior.

## Constraints and authorized roots

- Work on branch `feat/cskill-installer`.
- Bash `install-skills.sh` is read-only for this work: do not edit it or any Bash tests. Preserve its current behavior and file bytes.
- The existing untracked `cmd/cskills` animation prototype and untracked `go.mod` are part of the authorized Go work; inspect and extend them rather than replacing the prototype wholesale.
- Authorized change roots only: new/changed Go files under `cmd/cskills/` and other new Go source/test files needed for `cskill`; `go.mod`; tests for the Go implementation and cross-compatibility; `README.md`; and this ODD task document. Do not modify unrelated files.
- The untracked `.codegraph/` directory is unrelated and must remain untouched.
- Do not run either installer as an install/restore operation during implementation. Use isolated fixtures for tests; do not mutate real skill directories or backups.
- Close each verified work unit with a Conventional Commit on the feature branch; never stage unrelated `.codegraph/` content. Push and PR remain separate user decisions.
- TDD policy is unknown: no explicit repository TDD configuration has been established for this plan. Do not assume or invent a mandatory TDD workflow. Use the ordinary applicable runners: `go test ./...` and `bash tests/test-installer.sh` for Bash regression checks when changes warrant them. Do not change Bash to make a test pass.
- Keep each implementation task reviewable. Treat approximately 400 changed lines per task as an advisory review-size ceiling, not a hard constraint; split a task if its diff is likely to exceed it.

## Acceptance criteria

1. `cskill` retains and integrates the existing animation prototype without animating in non-TTY or `NO_COLOR` contexts, and then dispatches the supported installer commands.
2. Invalid, ambiguous, overlapping, protected, root, and symlink paths are rejected before any target or backup mutation. Source validation rejects unsafe skill trees before snapshot creation.
3. Go reads valid Bash v1 and v2 snapshots, preserves v1's OpenCode/Claude-only capture semantics, and writes v2 snapshots and state that `install-skills.sh` accepts without changing the Bash implementation.
4. Cross-restore tests prove that each implementation can restore snapshots produced by the other, including absent-target semantics and unchanged uncaptured targets. State interoperability is tested for read/write behavior and fallback rules.
5. Install and restore are serialized; a failure before commit restores the exact pre-operation trees (including absent targets) and preserves recovery snapshots. Lock contention causes no mutation.
6. `status`, `list`, `restore`, and `install` have clear help, stable exit/error behavior, safe headless operation, and no implicit destructive action.
7. `go test ./...` passes. `bash tests/test-installer.sh` passes unchanged as the Bash regression/interoperability gate. README documents commands, safety boundaries, snapshot/state compatibility, and verification.

## Tasks

### CSK-001 — Preserve and integrate the terminal animation prototype ✅

- **Deliverable:** Keep the current `cmd/cskills` animation behavior and connect the executable entry point to an explicit command dispatcher. Noninteractive output remains static and animation is opt-in through actual terminal conditions; installer commands never accidentally animate their output.
- **Route/trigger evidence:** `cmd/cskills/main.go`: `main` → `run` parses `-static` and chooses `play` or plain output; `shouldAnimate` gates on terminal, `NO_COLOR`, and static flag. Existing unit coverage is in `cmd/cskills/main_test.go` (`TestShouldAnimateRequiresTTYAndOptInConditions`, `TestFrameTextRevealsLogoAndClamps`, `TestRenderFrameHasPlainAndResettablePinkVariants`). Bash precedent: `install-skills.sh` `parse_arguments` accepts explicit commands and only selects the menu on a TTY (`install-skills.sh:177-249`).
- **Checks:** `go test ./...`; add command-dispatch/TTY-gating tests using injected input/output and terminal/environment conditions.
- **Review estimate:** One focused task; target ≤~400 changed lines.

### CSK-002 — Validate configuration, paths, and source before mutation ✅

- **Deliverable:** Add Go configuration resolution and validation for skill targets, backup root, state and lock paths, protected locations, overlaps, symlinks, and fetched source content. All validation completes before snapshot or destination mutation. Follow-up before enabling operations: reject equal lock/state paths and source roots named `.codegraph` (non-blocking review warnings R3-001/R3-002).
- **Route/trigger evidence:** Bash route: `validate_configuration` resolves paths, then invokes `assert_safe_target`, `assert_safe_backup_root`, `assert_safe_lock_path`, and `assert_safe_state_path` (`install-skills.sh:260-442`). Source route: `clone_repository` → `validate_skill_tree` rejects symlinks, `.codegraph`, invalid manifests, and insufficient skill counts (`install-skills.sh:468-508`, `722-745`). Existing fixture patterns: `tests/test-installer.sh` path and source-validation tests, including unsafe TMPDIR, unsafe backup ID, undersized source, symlinks, `.codegraph`, non-regular manifest, and root normalization (`tests/test-installer.sh:674-991`).
- **Checks:** Unit/table tests for path normalization and overlap/protection cases; isolated source fixtures for valid and invalid trees. Run `go test ./...`.
- **Review estimate:** Split further if path validation and source validation together exceed the advisory ~400 changed lines.

### CSK-003 — Implement shared snapshot metadata v1/v2 and installed state

- **Deliverable:** Implement strict parsers/serializers matching Bash metadata keys, required/forbidden keys, duplicate/unknown-key rejection, version-specific captured/existed/count consistency, snapshot ID and timestamp constraints, and state-file validation/fallback behavior. Write compatible v2 snapshots and the shared key/value state format. Before restore integration, reject snapshot skill children that symlink outside the captured tree (review warning R3-001 on `685b00b`).
- **Route/trigger evidence:** Bash snapshot creation writes v2 metadata (`create_snapshot`, `install-skills.sh:780-837`); `load_metadata` enforces v1/v2 contracts (`install-skills.sh:856-955`); `validate_snapshot` verifies directory content and counts (`install-skills.sh:957-1005`). State load/write/fallback routes are `load_installed_state`, `resolve_installed_commit`, and `write_installed_state` (`install-skills.sh:510-587`, `685-703`). Existing tests construct v1/v2 fixtures and cover malformed, missing, forbidden, and inconsistent metadata (`tests/test-installer.sh:270-309`, `1114-1305`); README records legacy semantics (`README.md:91-101`, `179-213`).
- **Checks:** Golden/fixture tests for v1 and v2 parsing, invalid metadata, absent and uncaptured targets, state parsing/writing, and Bash-generated artifact compatibility. Run `go test ./...` and `bash tests/test-installer.sh` where integration fixtures are ready.
- **Review estimate:** Keep format contract and state handling as one cohesive task; split test fixture expansion if the diff approaches ~400 changed lines.

### CSK-004 — Add locking, transactional replacement, and rollback

- **Deliverable:** Serialize destructive operations with a nonblocking lock; stage and verify complete replacement trees; snapshot current state before replacement; commit replacements as a unit; roll back from quarantined originals on pre-commit errors; preserve snapshots and report incomplete cleanup honestly.
- **Route/trigger evidence:** Bash lock route `acquire_installer_lock` uses `flock --exclusive --nonblock` (`install-skills.sh:444-453`). Install stages and validates all targets, snapshots, and enters `run_transaction` (`install-skills.sh:1411-1477`, `1159-1286`); rollback restores quarantine (`install-skills.sh:1058-1126`). Existing failpoint scenarios verify install/restore rollback, quarantine fidelity, absent target preservation, lock contention, and post-commit cleanup (`tests/test-installer.sh:476-645`, `794-855`, `993-1086`).
- **Checks:** Isolated temporary-target tests for success, failures at each replacement boundary, absent-target rollback, lock contention, and post-commit cleanup. Assert full tree equality and snapshot retention. Run `go test ./...`.
- **Review estimate:** This is the largest risk area; divide transaction preparation/commit from rollback only if needed to keep each review slice near or below ~400 changed lines.

### CSK-005 — Deliver CLI commands, cross-compatibility tests, and documentation

- **Deliverable:** Complete `status`, `list`, `restore`, and `install` UX; implement snapshot selection and safe headless behavior; exercise cross-restore and state interoperability in both directions; document command use and format compatibility in README.
- **Route/trigger evidence:** Bash command routes are parsed in `parse_arguments` (`install-skills.sh:177-249`), list via `list_backups` (`1288-1349`), status via `run_status` (`1351-1409`), install via `run_install` (`1411-1493`), and restore via `main` → `run_restore` (`install-skills.sh:1673-1700`, `1495-1567`), where it validates the snapshot, preserves uncaptured targets, creates a pre-restore safety snapshot, and invokes the transaction. The README command matrix and v1 restore contract are at `README.md:103-120`, `179-213`. Cross-direction fixtures can follow `tests/test-installer.sh` local repository and snapshot fixture patterns (`55-138`, `270-309`).
- **Checks:** CLI table tests and end-to-end fixtures prove Go→Bash and Bash→Go snapshot restore and state interoperability without changing Bash. Run `go test ./...` and the unchanged `bash tests/test-installer.sh`; verify help and non-TTY commands are deterministic. Review README against actual command behavior.
- **Review estimate:** Keep CLI integration and cross-compatibility/documentation within the advisory ~400 changed lines; split documentation only if the combined review slice grows beyond that.

## Rough forecast and ask-on-risk strategy

Forecast: five implementation slices (CSK-001–CSK-005), each intended to be independently reviewable; approximately 2–4 focused engineering days overall, with transaction semantics and bidirectional compatibility the main uncertainty. The ~400-line guidance is per-task review advice, not a total project limit. Default strategy is **ask on risk**: pause and ask before broadening authorized roots, changing the Bash contract or implementation, adding dependencies, weakening a safety invariant, or choosing behavior not implied by the observed Bash format. For low-risk details already constrained by Bash behavior, preserve that behavior and record the test evidence rather than introducing a new contract.

## Progress evidence and next step

- **Planning evidence:** Current branch is `feat/cskill-installer`. Working tree contains untracked `cmd/`, `go.mod`, and `.codegraph/`; the latter is explicitly out of scope. CodeGraph inspection confirmed the current animation functions and their tests; direct read was used for Bash/docs because CodeGraph did not return those shell/documentation files.
- **Current status:** CSK-001 `fd19121`, CSK-002 `83dc3a1`, and partial CSK-003 format layer `685b00b` committed; both prior native candidate reviews acknowledged. Latest R3-001 snapshot child-symlink warning fixed with regression. Go snapshot creation now captures all three target states and publishes v2 metadata atomically; an isolated Go→Bash `list` test confirms Bash validates the snapshot, including absent AGY and a tampered-count rejection. `go test ./...`, `go vet ./...` passed. Full Bash `cp -a` metadata fidelity, Bash restore of Go snapshots, and Go restore of Bash snapshots remain unproven; CLI install/restore disabled. Commit/review of this new work unit pending.
- **Next step:** Complete CSK-004 locking and rollback, then prove bidirectional restore in isolated fixtures before enabling destructive CLI commands.
