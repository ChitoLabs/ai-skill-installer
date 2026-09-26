# Safely install `ai-skill-pack` in OpenCode, Claude, and AGY

`install-skills.sh` installs the complete
[`ChitoLabs/ai-skill-pack`](https://github.com/ChitoLabs/ai-skill-pack) skill
tree into OpenCode, Claude, and AGY. It checks the source commit before an
update, creates managed snapshots, and uses a transactional rollback if a
replacement fails.

> **Important:** `install` replaces **all three** skill directories in full.
> This includes Gentle AI skills where present. Reinstall Gentle AI after the
> update if you use it, then restart OpenCode, Claude, and AGY.

## Quick start

For a fresh Linux installation, download the repository, inspect the installer,
run a dry run, and only then install:

```bash
git clone --depth 1 https://github.com/ChitoLabs/ai-skill-installer.git
cd ai-skill-installer

less install-skills.sh
chmod u+x install-skills.sh

./install-skills.sh dry-run
./install-skills.sh install
```

The installer does not require root access and should not be run with `sudo`.
Downloading and inspecting the script is preferred over piping a remote script
directly into a shell.

> A separate Go terminal installer, `cskill`, offers the same install, status,
> list, and restore workflow and reads and writes the same snapshot and
> state files. See [Go installer (`cskill`)](#go-installer-cskill) below.

Every `dry-run` and `install` performs a fresh shallow clone. By default, the
installer clones the `main` branch from `ChitoLabs/ai-skill-pack`, stages it
temporarily, and removes the staging directory afterward. `list` and `restore`
use local snapshots and do not contact the source repository.

## Check before updating

Use `status` before a later update. `check` is an alias:

```bash
./install-skills.sh status
# equivalent:
./install-skills.sh check
```

`status` first resolves the remote branch with `git ls-remote`, then compares
the remote commit with the installed commit. It never changes skill targets or
managed backups.

| Situation | What the installer reports or does |
|---|---|
| No previous install is recorded | Reports the remote version and recommends `install`. |
| Commits match | Reports that the installation is up to date. |
| Commits match, but a target was edited by hand | Warns about **manual drift** and suggests running `install` to restore the canonical tree. |
| The remote commit is newer | Reports `installed -> remote` and a skill summary: `+new / ~modified / -removed`. |

When an installed target is available, the summary compares the source skill
tree with the first existing target in this order: OpenCode, Claude, then AGY.
Names are listed alphabetically, with a maximum of 20 names in each group. A
fresh shallow clone is used when `status` needs source content for this
comparison.

`status` compares commits, not timestamps. If the configured repository URL or
branch differs from the last install, it warns that the comparison is by commit
only.

When a prior installation is recorded, `install` and `dry-run` also report the
installed-versus-source comparison before deciding whether to proceed.

### Idempotent install

`install` performs the same source validation and version check before changing
targets:

- A repeated install of the same recorded source commit is a no-op when the
  existing target already matches the canonical source. It creates no new
  snapshot.
- If the same commit is installed but local content has drifted, it warns and
  reinstalls the canonical tree.
- A newer commit creates an install snapshot and updates all three targets.
- `install --force` explicitly reinstalls the same version and creates a new
  install snapshot even when no update is available.

```bash
./install-skills.sh install
./install-skills.sh install --force
```

### Installed-version state

After a successful install, the installer records the repository URL, branch,
source commit, skill count, and UTC installation time in
`.installed-skills.state` beside the script. Override that location with
`SKILL_INSTALLER_STATE_FILE`.

If the state file is missing or invalid, the installer falls back to the newest
`reason=install` snapshot with a valid source commit in `BKOld/`. After a
successful restore, `restore` updates the state file to the selected snapshot's
source commit when that commit is recorded.

## Commands

| Command | Source access | Changes targets? | Changes backups? | Prompts? |
|---|---|---:|---:|---:|
| `./install-skills.sh dry-run` | Fresh shallow clone | No | No | No |
| `./install-skills.sh install` | Fresh shallow clone | Replaces all three unless already up to date | Creates an install snapshot unless it is a no-op | No |
| `./install-skills.sh install --force` | Fresh shallow clone | Replaces all three even when the version is unchanged | Creates an install snapshot | No |
| `./install-skills.sh status` or `check` | `git ls-remote`, plus a temporary clone when source content is needed | No | No | No |
| `./install-skills.sh list` | Local snapshots | No | No | No |
| `./install-skills.sh restore BACKUP_ID` | Local snapshot | Restores captured states | Creates a pre-restore snapshot | No |
| `./install-skills.sh menu` | Depends on the selection | Depends on the selection | Depends on the selection | Yes, for install and restore |
| `./install-skills.sh help` | None | No | No | No |

`--dry-run` is an alias for `dry-run`. With no arguments, the script opens the
menu only when a TTY is available. In a headless session, use an explicit
command. `restore` without `BACKUP_ID` requires a TTY for interactive selection;
headless sessions must provide the ID. Explicit `install` and `restore
BACKUP_ID` do not prompt.

## Dry run first

A dry run:

1. Resolves and validates the configured paths.
2. Creates a temporary staging directory.
3. Performs a fresh `git clone --depth 1 --single-branch`.
4. Validates the source commit and at least the configured number of top-level
   `SKILL.md` manifests.
5. Reports the source commit, skill count, resolved targets, path sources, and
   backup root.

It does **not** acquire the install lock, create target directories, prepare
replacement directories, create `BKOld/`, make a snapshot, or change existing
skills. Temporary staging is removed when the command exits.

## Install behavior and safety

After the same source checks as a dry run, `install` prepares identical copies
for all three targets, snapshots their current states, and replaces the trees
as one transaction.

| Safety property | Behavior |
|---|---|
| Source isolation | Uses temporary staging and never executes downloaded scripts. |
| Validation | Requires a valid 40-character source commit and at least the configured number of top-level manifests. |
| Complete backup | Preserves each existing target tree, not only `SKILL.md` files; a missing target is recorded as absent. |
| Transaction | Validates all prepared trees and rolls participating targets back if a failure occurs before commit. |
| Concurrency | `install` and `restore` hold one non-blocking `flock`; a second destructive operation exits before cloning or changing data. |
| Path protection | Rejects unsafe, overlapping, root, symlink, protected, and `.codegraph` paths, plus symlinks inside the downloaded tree. |
| Snapshot retention | Never silently deletes or prunes managed snapshots. |

By default, installation also creates `BKOld/`, `.install-skills.lock`, and
`.installed-skills.state` beside the script. Missing target parent directories
are created when replacements are prepared. The installer does not modify
OpenCode commands, Engram, CodeGraph indexes, `~/.agents/skills`, or
`~/.gemini/antigravity-cli/skills`.

## Targets and automatic path resolution

The installer selects the first non-empty value in each row. Before an install,
it reports the resolved canonical path and the source used.

| Runtime | Path precedence, highest first | Default |
|---|---|---|
| OpenCode | `OPENCODE_SKILLS_DIR` → `OPENCODE_CONFIG_DIR/skills` → `XDG_CONFIG_HOME/opencode/skills` | `~/.config/opencode/skills` |
| Claude | `CLAUDE_SKILLS_DIR` → `CLAUDE_CONFIG_DIR/skills` | `~/.claude/skills` |
| AGY | `AGY_SKILLS_DIR` | `~/.gemini/config/skills` |

> **AGY path:** The supported global target is `~/.gemini/config/skills`, based
> on the verified AGY CLI v1.1.19 customization layout. The installer never
> uses or modifies `~/.gemini/antigravity-cli/skills`.

Explicit target paths and configuration roots must resolve to safe absolute
paths, and each final target must end in `/skills`. Empty variables fall through
to the next candidate. Target directories do not need to exist before the run.

## Backups, restore, and recovery

Managed snapshots live in `BKOld/` beside the installer unless
`SKILL_BACKUP_DIR` overrides the location:

```text
BKOld/
└── 20260831T170000Z-a1b2c3d4/
    ├── metadata
    ├── opencode/   # only when the target existed
    ├── claude/     # only when the target existed
    └── agy/        # only when the target existed
```

List and restore snapshots with:

```bash
./install-skills.sh list
./install-skills.sh restore 20260831T170000Z-a1b2c3d4
```

`list` reports each snapshot's ID, UTC date, reason, represented targets, skill
counts, and source commit when known. `restore` validates the selected snapshot,
creates a new three-target `pre-restore` safety snapshot, and restores every
captured state transactionally. A target captured as absent becomes absent
again; a target not captured by the selected snapshot remains unchanged.

Legacy version 1 snapshots remain listable and restorable. They capture
OpenCode and Claude only, so AGY remains unchanged during their restoration.

There is no separate `uninstall` command. To remove an installation and return
to the exact earlier states, restore the snapshot created by that install. If
custom target or backup overrides were used during installation, reuse those
same overrides when listing or restoring; snapshot metadata does not relocate
targets automatically.

## Environment overrides

| Variable | Purpose | Default or rule |
|---|---|---|
| `OPENCODE_SKILLS_DIR` | Exact OpenCode target | Must be an absolute path ending in `/skills` |
| `CLAUDE_SKILLS_DIR` | Exact Claude target | Must be an absolute path ending in `/skills` |
| `AGY_SKILLS_DIR` | Exact AGY target | Must be an absolute path ending in `/skills` |
| `OPENCODE_CONFIG_DIR` | OpenCode configuration root | Appends `/skills` |
| `XDG_CONFIG_HOME` | XDG root for OpenCode | Appends `/opencode/skills` |
| `CLAUDE_CONFIG_DIR` | Claude configuration root | Appends `/skills` |
| `SKILL_BACKUP_DIR` | Managed snapshot root | `BKOld/` beside the script; override must be absolute |
| `SKILL_INSTALLER_LOCK_FILE` | Install/restore lock file | `.install-skills.lock` beside the script; override must be absolute |
| `SKILL_INSTALLER_STATE_FILE` | Installed-version state file | `.installed-skills.state` beside the script; override must be absolute |
| `SKILL_PACK_REPOSITORY_URL` | Source repository | `https://github.com/ChitoLabs/ai-skill-pack` |
| `SKILL_PACK_BRANCH` | Source branch | `main` |
| `SKILL_PACK_MIN_SKILL_COUNT` | Minimum accepted manifests | Positive integer; default `100` |
| `TMPDIR` | Temporary clone root | Existing safe absolute directory; default `/tmp` |

Example for an isolated test environment:

```bash
SKILL_PACK_REPOSITORY_URL="file:///path/to/repository" \
SKILL_PACK_MIN_SKILL_COUNT="2" \
OPENCODE_SKILLS_DIR="/tmp/fixture/opencode/skills" \
CLAUDE_SKILLS_DIR="/tmp/fixture/claude/skills" \
AGY_SKILLS_DIR="/tmp/fixture/agy/skills" \
SKILL_BACKUP_DIR="/tmp/fixture/BKOld" \
./install-skills.sh dry-run
```

## Dependencies

The installer requires Bash and these commands:

| Purpose | Commands |
|---|---|
| Source retrieval | `git` |
| Files and paths | `mktemp`, `cp`, `mv`, `rm`, `mkdir`, `realpath`, `find` |
| Validation and metadata | `diff`, `date`, `grep`, `cut`, `sort`, `head` |
| Install/restore locking | `flock` |

`flock` is required only by `install` and `restore`. On Debian or Ubuntu, the
usual packages are:

```bash
sudo apt-get update
sudo apt-get install -y bash git coreutils diffutils findutils util-linux
```

`status`, `dry-run`, and `install` require access to the configured source
repository. `list` and `restore` operate from local snapshots.

## SSH and headless use

Copy the installer to the server, then use either an allocated TTY for the menu
or an explicit noninteractive command:

```bash
scp install-skills.sh server:~/install-skills.sh
ssh -t server 'chmod u+x ~/install-skills.sh && ~/install-skills.sh menu'

ssh server '~/install-skills.sh dry-run'
ssh server '~/install-skills.sh status'
ssh server '~/install-skills.sh install'
ssh server '~/install-skills.sh list'
ssh server '~/install-skills.sh restore 20260831T170000Z-a1b2c3d4'
```

Explicit `install` and `restore BACKUP_ID` do not prompt. This is intentional
for automation, so run `dry-run` first and verify the reported paths.

## Verify

Successful installation reports the exact source commit and validated skill
count after checking all three installed trees. Then confirm that a recovery
snapshot is available:

```bash
./install-skills.sh list
```

For the default target paths, count installed manifests with:

```bash
find "$HOME/.config/opencode/skills" -mindepth 2 -maxdepth 2 -type f -name SKILL.md | wc -l
find "$HOME/.claude/skills" -mindepth 2 -maxdepth 2 -type f -name SKILL.md | wc -l
find "$HOME/.gemini/config/skills" -mindepth 2 -maxdepth 2 -type f -name SKILL.md | wc -l
```

If path overrides were used, substitute the resolved paths printed by `dry-run`
or `install`.

Repository checks use only temporary repositories, targets, and backup roots;
they do not touch real skill directories:

```bash
bash -n install-skills.sh
bash -n tests/test-installer.sh
bash tests/test-installer.sh
```

## Troubleshooting

| Message or symptom | What to check |
|---|---|
| `Required command not found` | Install the missing dependency listed above. `flock` is required only by install and restore. |
| Clone or branch failure | Confirm network access, repository URL, and branch. No target or backup is changed if source retrieval or validation fails. |
| Fewer than the minimum manifests | Verify that the selected repository and branch contain a complete `skills/` tree. |
| Unsafe, overlapping, or symlink path | Correct the reported override; the installer intentionally refuses ambiguous or protected destinations. |
| Another operation is running | Wait for the active install or restore to release the lock, then retry. |
| No arguments fail over SSH | Use an explicit command or allocate a TTY with `ssh -t`. |
| Post-commit quarantine cleanup warning | The new targets and `BKOld` snapshot are valid. Keep the reported transaction paths for inspection and use the managed snapshot if recovery is needed. |

After any install or restore, restart affected applications so they reload their
skills. After install, reinstall Gentle AI first if you use it.

## Go installer (`cskill`)

`cskill` (`cmd/cskills`) is a Linux-only Go command-line installer that offers
the same install, status, list, and restore workflow as `install-skills.sh`,
reading and writing the same snapshot metadata and installed-state files so
either installer can restore snapshots the other created.

### Build and run

Requires the Go toolchain declared in `go.mod` (`go 1.22` or newer). Build from
the repository root:

```bash
go build -o cskill ./cmd/cskills
./cskill --help
```

`cskill` builds and runs on Linux only: locking, hardlink preservation, and
snapshot publishing (`cmd/cskills/lock_linux.go`, `hardlink_linux.go`,
`snapshot_publish_linux.go`) use Linux-specific syscalls (`flock`, raw device
and inode numbers for hardlink detection).

### Commands

| Command | What it does | Changes targets? | Changes backups? |
|---|---|---:|---:|
| `cskill status` | Compares the installed commit (from the state file, or the newest valid install snapshot as fallback) against the remote branch head; on a match with local drift, or on a newer remote commit, clones the source and prints a skill diff | No | No |
| `cskill list` | Lists managed snapshots under the backup root: ID, UTC time, reason, per-target captured/existed state and skill counts, source commit | No | No |
| `cskill install [--force]` | Clones the configured branch, validates the skill tree, and replaces the OpenCode, Claude, and AGY targets as one transaction | Yes, unless the install is skipped as already up to date | Creates an install snapshot unless the install is skipped |
| `cskill restore BACKUP_ID` | Restores a validated snapshot by ID | Restores captured targets; leaves uncaptured targets unchanged | Creates a `pre-restore` safety snapshot |
| `cskill help`, `cskill --help`, `cskill -h` | Prints usage and the command list | No | No |
| `cskill` (no command) | Prints the `C-Skills` logo (animated on an interactive TTY, static otherwise) and exits `0` | No | No |

Every command exits `0` on success and `1` on any error; there are no distinct
exit codes per failure type. `install` accepts only an optional `--force`; any
other or additional argument is rejected before configuration is even
resolved. `restore` accepts exactly one `BACKUP_ID`, checked against the same
snapshot ID pattern as the Bash installer
(`^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$`); a missing or malformed ID is rejected
with no file access. There is no interactive menu or restore picker: `restore`
always requires an explicit `BACKUP_ID`.

#### Idempotent install

Like `install-skills.sh install`:

- A repeat install of the already-recorded commit, repository, and branch is a
  no-op when the live OpenCode target already matches the cloned source: no
  new snapshot, no target change.
- The same commit with drifted local targets is reinstalled with a warning,
  `--force` or not.
- A newer commit always installs and updates all three targets.
- `install --force` reinstalls the same version even when nothing changed.

### Shared configuration and environment variables

`cskill` resolves configuration with the same precedence and the same
environment variables as `install-skills.sh`: `OPENCODE_SKILLS_DIR`,
`CLAUDE_SKILLS_DIR`, `AGY_SKILLS_DIR`, `OPENCODE_CONFIG_DIR`,
`XDG_CONFIG_HOME`, `CLAUDE_CONFIG_DIR`, `SKILL_BACKUP_DIR`,
`SKILL_INSTALLER_LOCK_FILE`, `SKILL_INSTALLER_STATE_FILE`,
`SKILL_PACK_REPOSITORY_URL`, `SKILL_PACK_BRANCH`, and
`SKILL_PACK_MIN_SKILL_COUNT`. See [Environment overrides](#environment-overrides)
for defaults and rules. `cskill` applies the same absolute-path,
`/skills`-suffix, overlap, symlink, and `.codegraph`-rejection checks before
any mutation, plus one Go-only check: the lock path and state path must be
different files.

### Safety

| Property | Behavior |
|---|---|
| Pre-mutation validation | Configuration, target, backup, lock, and state paths are validated before any snapshot or destination mutation, then revalidated once the install lock is held. |
| Locking | `install` and `restore` take one non-blocking `flock` on the configured lock file; a second concurrent destructive operation fails immediately before any mutation. `install` holds the lock uninterrupted from cloning through the transaction and the installed-state write. `restore` releases the lock before writing installed state — a documented residual divergence (see Limitations). |
| Transactional replacement | Targets are staged, snapshotted, and committed as one unit; a failure before commit rolls back from quarantined originals, and a target that did not previously exist is restored to being absent. |
| Pre-restore safety snapshot | `restore` always creates a fresh `pre-restore` snapshot of the current state before applying the selected snapshot. |
| Uncaptured targets | A target the selected snapshot did not capture is left completely unchanged by `restore`. |
| Idempotent install | See above; a no-op install creates no snapshot and changes no target. |
| Sanitized clone | Cloning uses temporary staging and never executes fetched content; the cloned tree is rejected if it contains symlinks, a `.codegraph` directory, or fewer than the configured minimum number of `SKILL.md` manifests. |
| No implicit mutation | `restore` always requires an explicit `BACKUP_ID`; running `cskill` with no command only prints the logo. |
| Animation gating | The logo animates only on an interactive TTY, and only when `NO_COLOR` is unset and `--static` was not passed; any other context prints the static logo. |

### Interoperability with `install-skills.sh`

`cskill` reads and writes the same on-disk formats as the Bash installer,
without changing it:

- **Snapshot metadata** — `cskill` parses both version 1 (implicit
  OpenCode/Claude-only capture) and version 2 (explicit per-target
  `captured`/`existed`/`skill_count`) metadata, with the same required and
  forbidden keys, duplicate/unknown-key rejection, and captured/existed/count
  consistency rules as the Bash installer. It writes only version 2 snapshots,
  in the same key order as `create_snapshot`.
- **Installed state** — `cskill` reads and writes `.installed-skills.state` in
  the same `key=value` format as the Bash installer (`repository_url`,
  `branch`, `source_commit`, `skill_count`, `installed_utc`), and falls back to
  the newest valid `reason=install` snapshot when the state file is missing or
  invalid.
- **Cross-restore** — either installer can restore a snapshot the other
  created, including absent-target and uncaptured-target semantics, and either
  can read state the other wrote.

This is proven with isolated `go test` fixtures that create snapshots, state,
and skill trees with one implementation and restore or read them with the
other, never against real skill directories: see
`TestBashRestoresGoCreatedV2Snapshot`, `TestGoRestoresBashGeneratedV2Snapshot`,
`TestGoRestoresBashLegacyV1CaptureSemantics` (`cmd/cskills/restore_test.go`),
and `TestGoReadsBashWrittenInstalledState`,
`TestBashStatusAcceptsGoWrittenInstalledState` (`cmd/cskills/state_interop_test.go`).

### Known limitations and differences from `install-skills.sh`

| Area | Difference |
|---|---|
| Extended attributes | `copyTree` does not copy xattrs (`cp -a` does); see `TestCopyTreeDoesNotPreserveExtendedAttributesLikeCpArchive`. Modes, timestamps, symlinks, and hardlinks are preserved. |
| Ownership and ACLs | Not verified for a non-root user; `cskill` runs, and is expected to run, without root. |
| Installed-state fallback | Stricter than Bash: `cskill` requires the fallback snapshot to pass full `ValidateSnapshot` checks, not only a valid source-commit line (`TestGoInstalledStateFallbackIsStricterThanBash`). |
| Interactive restore | No menu or restore picker; `restore` always requires an explicit `BACKUP_ID`. |
| No-command behavior | `cskill` with no command prints the logo and exits `0`; the Bash installer prints usage and exits `2` in the same headless situation. |
| Install summary | `install` does not print the pre-upgrade skill change summary that `status` prints. |
| Restore lock scope | `restore` writes the installed-state update after releasing the install lock, unlike `install`, which holds the lock through the state write. |

### Verification

```bash
go build ./...
go test ./...
go vet ./...
bash tests/test-installer.sh
```

`bash tests/test-installer.sh` is the unchanged Bash regression and
interoperability gate; `cskill` does not modify `install-skills.sh` or its
tests.
