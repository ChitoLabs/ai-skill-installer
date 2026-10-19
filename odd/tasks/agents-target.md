# Agents Target Support for Pi and Gentle Shell — Task Plan

## Objective

Add `~/.agents/skills` as the 4th target (`Agents`) to both the Bash installer (`install-skills.sh`) and Go installer (`cskill`). Support the Agent Skills open specification so Pi and Gentle Shell consume installed skills natively without duplication, advance snapshot metadata to format version 3 with full v1/v2/v3 backward compatibility, and maintain bidirectional transaction and restore parity.

## Specs

- **S1:** "es posible agregar tambien a pi y a gentle shell en este install ?" — Add Pi and Gentle Shell support to the installer.
- **S2:** "Opción 1: Target único ~/.agents/skills (value: agents_standard)" — Use the standard `~/.agents/skills` directory so Pi and Gentle Shell read skills natively without duplicate copies on disk.
- **S3:** Target resolution: `$HOME/.agents/skills` as default, overridable by `AGENTS_SKILLS_DIR`. Target must end in `/skills`, be an absolute canonical path, not be a symlink or filesystem root, and not overlap with `$HOME`, backup root, lock path, state path, or any other target (`opencode`, `claude`, `agy`).
- **S4:** Snapshot format version 3: Advance snapshot metadata to `format_version=3` capturing `opencode`, `claude`, `agy`, and `agents` (`agents_captured`, `agents_existed`, `agents_skill_count`).
- **S5:** Backward compatibility: Both Bash and Go must parse and restore `v1`, `v2`, and `v3` snapshots safely. For `v1` and `v2` snapshots where `agents` was not captured (`agents_captured=0` / `false`), `agents` target is preserved and not modified during restore.
- **S6:** Transactional rollback: Install and restore operations manage all 4 targets atomically with quarantine rename, staging, and pre-commit rollback.
- **S7:** Status and drift: `status` and `drift` inspect all 4 targets and report manual drift or sync status across all configured targets.
- **S8:** Documentation and CI: Update `README.md` to document Pi and Gentle Shell support via `~/.agents/skills` (`AGENTS_SKILLS_DIR`), and ensure all Go and Bash CI checks pass.

## Tasks

- [x] **AGT-001**: Bash installer 4th target `agents` and snapshot v3 format (`install-skills.sh` + `tests/test-installer.sh`) [S1, S2, S3, S4, S5, S6, S7] (Route: inline, Commit: ee2ed53)
- [x] **AGT-002**: Go installer `cskill` 4th target `Agents` and snapshot v3 parity (`cmd/cskills/` + tests) [S1, S2, S3, S4, S5, S6, S7] (Route: inline, Commit: 93a30d3)
- [x] **AGT-003**: Cross-restore v1/v2/v3 interoperability tests between Bash and Go [S4, S5] (Route: inline, Commit: 8427e93)
- [x] **AGT-004**: Documentation and verification (`README.md`, CI run) [S8] (Route: inline, Commit: 1fc78d3)

## Log

- **L1 (2026-10-19):** User asked: "es posible agregar tambien a pi y a gentle shell en este install ?"
- **L2 (2026-10-19):** Options presented. User chose: "Opción 1: Target único ~/.agents/skills (value: agents_standard)".
- **L3 (2026-10-19):** Created branch `feat/agents-target-pi-gentleshell` and initialized ODD plan.
- **L4 (2026-10-19):** Completed AGT-001 in commit ee2ed53: Added 4th target `agents` (`~/.agents/skills`), snapshot format version 3 with v1/v2/v3 compatibility, 4-target transaction engine and rollback in `install-skills.sh`. Extended `tests/test-installer.sh` to 58/58 passing cases.
- **L5 (2026-10-19):** Completed AGT-002 in commit 93a30d3: Implemented 4th target `AgentsTarget`, snapshot format version 3, v1/v2/v3 parser/serializer, 4-target transaction engine and restore in `cmd/cskills/`. Passed all Go tests, `gofmt -l cmd`, and `go vet ./...`.
- **L6 (2026-10-19):** Completed AGT-003 in commit 8427e93: Added full bidirectional v1/v2/v3 cross-restore tests between Go and Bash, proving both engines restore v3 snapshots across all 4 targets and preserve uncaptured targets during v1/v2 restores.
- **L7 (2026-10-19):** Completed AGT-004 in commit 1fc78d3: Documented Pi and Gentle Shell 4th target (`~/.agents/skills`), `AGENTS_SKILLS_DIR` override, snapshot format version 3, and v1/v2/v3 cross-compatibility in `README.md`. Verified full test suite with `gofmt`, `go vet`, `go test -count=1 ./...`, and `tests/test-installer.sh` (58/58 cases). All 4 tasks completed.
