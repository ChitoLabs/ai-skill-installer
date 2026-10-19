# Safely install `ai-skill-pack` across AI Coding Agents

Install the complete [`ChitoLabs/ai-skill-pack`](https://github.com/ChitoLabs/ai-skill-pack) catalog into **OpenCode**, **Claude**, **AGY**, **Pi**, and **Gentle Shell** with atomic transactions, rollback protection, and managed backups.

> **Important:** `install` replaces all four skill directories in full. If you use Gentle AI skills, reinstall Gentle AI after the update, then restart your agent applications.

---

## Quick path

Get up and running on Linux in three commands without root privileges:

```bash
# 1. Clone the installer
git clone --depth 1 https://github.com/ChitoLabs/ai-skill-installer.git
cd ai-skill-installer

# 2. Inspect and simulate the installation
./install-skills.sh dry-run

# 3. Apply the transactional installation
./install-skills.sh install
```

> **Prefer Go?** A native Linux terminal executable, `cskill`, is included. Compile it with `go build -o cskill ./cmd/cskills` and run `./cskill install`. Both implementations share identical snapshot and state formats.

---

## Targets & Platforms

The installer automatically targets the standard skills directories for all supported environments. An update populates all four targets simultaneously:

| Runtime / Harness | Default Destination | Override Variable | Platform Notes |
|---|---|---|---|
| **OpenCode** | `~/.config/opencode/skills` | `OPENCODE_SKILLS_DIR` | Falls back to `OPENCODE_CONFIG_DIR/skills` or `XDG_CONFIG_HOME/opencode/skills`. |
| **Claude** | `~/.claude/skills` | `CLAUDE_SKILLS_DIR` | Falls back to `CLAUDE_CONFIG_DIR/skills`. |
| **AGY** | `~/.gemini/config/skills` | `AGY_SKILLS_DIR` | Verified layout for AGY CLI v1.1.19+. Never touches `~/.gemini/antigravity-cli/skills`. |
| **Pi & Gentle Shell** | `~/.agents/skills` | `AGENTS_SKILLS_DIR` | Standard [Agent Skills specification](https://agentskills.io/specification) directory, discovered natively by Pi and Gentle Shell without duplicate copies on disk. |

*All target overrides must resolve to absolute paths ending in `/skills`. Missing parent directories (such as `~/.agents`) are created automatically.*

---

## Commands & Usage

Both `install-skills.sh` and the `cskill` Go binary provide the same core commands:

| Action | Bash (`./install-skills.sh`) | Go (`./cskill`) | Mutates Targets? | Creates Snapshot? | Description |
|---|---|---|:---:|:---:|---|
| **Preview** | `dry-run` | `dry-run` | No | No | Shallow-clones and validates source skills without touching targets or backups. |
| **Install / Update** | `install` | `install` | Yes* | Yes* | Updates all four targets transactionally. Skips if already up to date (*no-op). |
| **Force Reinstall** | `install --force` | `install --force` | Yes | Yes | Forces complete re-installation and creates a new snapshot even if unchanged. |
| **Check Version** | `status` (or `check`) | `status` | No | No | Compares installed commit against remote HEAD and summarizes `+new / ~modified / -removed` skills. Warns on manual drift. |
| **List Backups** | `list` | `list` | No | No | Lists managed snapshots in chronological order with target and skill counts. |
| **Restore** | `restore <ID>` | `restore <ID>` | Yes | Yes | Restores a selected snapshot atomically, creating a pre-restore safety snapshot first. |
| **Interactive Menu** | `menu` (or no args on TTY) | *(Terminal logo)* | Depends | Depends | TTY-only interactive prompt. Headless/SSH runs require explicit commands. |

---

## Safety & Atomic Rollback

Every installation and restoration runs inside an isolated, failure-safe transaction:

```text
[1. Verify Paths] ──> [2. Non-blocking Lock] ──> [3. Stage Source] ──> [4. Snapshot Targets]
                                                                                │
[7. Clean Quarantine] <── [6. Post-Commit Verify] <── [5. Swap Targets] <───────┘
                                                             │ (on error)
                                                             └──> [Atomic Rollback from Quarantine]
```

- **Exclusive Non-blocking Lock (`flock`):** Prevents concurrent installer runs from corrupting targets or backup states.
- **Pre-quarantine Swap:** Existing target directories are renamed to `.transaction.<id>` before replacement.
- **Automatic Rollback:** If any target copy or validation fails before commit, all original directories are restored immediately.
- **Sanitized Source Clone:** Clones into temporary staging (`TMPDIR`), verifying top-level `SKILL.md` manifests and rejecting symlinks or `.codegraph` directories inside the source.

---

## Managed Snapshots & Recovery

Managed backups are saved to `BKOld/<SNAPSHOT_ID>/` beside the installer (overridable via `SKILL_BACKUP_DIR`):

```text
BKOld/
└── 20261019T120000Z-a1b2c3d4/
    ├── metadata        # Format version, source commit, UTC date, per-target status
    ├── opencode/       # Populated only if target existed
    ├── claude/         # Populated only if target existed
    ├── agy/            # Populated only if target existed
    └── agents/         # Populated only if target existed (v3+)
```

### Snapshot format versions & backward compatibility

| Format Version | Introduced | Targets Captured | Restore Compatibility |
|---|---|---|---|
| **Version 1** | Initial release | OpenCode, Claude | Restoring v1 preserves current `AGY` and `Agents` targets unchanged. |
| **Version 2** | AGY integration | OpenCode, Claude, AGY | Restoring v2 preserves current `Agents` target unchanged. |
| **Version 3** | Pi & Gentle Shell | OpenCode, Claude, AGY, Agents | Restores all four targets transactionally. |

*Both Bash and Go installers seamlessly read and restore v1, v2, and v3 snapshots.*

### How to restore

```bash
# 1. Find the desired snapshot ID
./install-skills.sh list

# 2. Restore by ID (creates an automatic pre-restore safety snapshot first)
./install-skills.sh restore 20261019T120000Z-a1b2c3d4
```

---

## Configuration & Environment Overrides

Tune installer behavior via environment variables:

| Variable | Description | Default |
|---|---|---|
| `SKILL_PACK_REPOSITORY_URL` | Source repository URL | `https://github.com/ChitoLabs/ai-skill-pack` |
| `SKILL_PACK_BRANCH` | Source branch | `main` |
| `SKILL_PACK_MIN_SKILL_COUNT` | Minimum valid `SKILL.md` manifests required | `100` |
| `SKILL_BACKUP_DIR` | Managed snapshot storage directory | `./BKOld` |
| `SKILL_INSTALLER_LOCK_FILE` | Non-blocking lock file path | `./.install-skills.lock` |
| `SKILL_INSTALLER_STATE_FILE` | Installed-version metadata file | `./.installed-skills.state` |
| `OPENCODE_SKILLS_DIR` | Explicit OpenCode target path | `~/.config/opencode/skills` |
| `CLAUDE_SKILLS_DIR` | Explicit Claude target path | `~/.claude/skills` |
| `AGY_SKILLS_DIR` | Explicit AGY target path | `~/.gemini/config/skills` |
| `AGENTS_SKILLS_DIR` | Explicit Pi & Gentle Shell target path | `~/.agents/skills` |
| `TMPDIR` | Temporary clone root | `/tmp` |

---

## Verification & Checks

### Confirm local installation

Count installed `SKILL.md` manifests across all active runtimes:

```bash
find "$HOME/.config/opencode/skills" -mindepth 2 -maxdepth 2 -type f -name SKILL.md | wc -l
find "$HOME/.claude/skills" -mindepth 2 -maxdepth 2 -type f -name SKILL.md | wc -l
find "$HOME/.gemini/config/skills" -mindepth 2 -maxdepth 2 -type f -name SKILL.md | wc -l
find "$HOME/.agents/skills" -mindepth 2 -maxdepth 2 -type f -name SKILL.md | wc -l
```

### Run hermetic test suites

All regression tests use isolated temporary fixtures and do not touch real skill directories:

```bash
# Run the Go suite (includes cross-restore interop tests)
go test -count=1 ./...

# Run the Bash suite (58 isolated end-to-end cases)
bash tests/test-installer.sh
```

---

## Troubleshooting

| Issue | Cause | Solution |
|---|---|---|
| `Another install or restore operation is already running` | Stale or active `flock` lock file. | Wait for the concurrent process to finish, or remove `.install-skills.lock` if the process terminated abnormally. |
| `Target overlaps protected path` | Destination path collides with another target, home, or backup root. | Check your `*_SKILLS_DIR` overrides; all targets must be distinct non-overlapping directories ending in `/skills`. |
| `Skill validation found N manifests; minimum is 100` | Incomplete source clone or branch mismatch. | Verify network connectivity or adjust `SKILL_PACK_MIN_SKILL_COUNT` for small fixture repositories. |
| `restore requires BACKUP_ID without a TTY` | Running `restore` headlessly or via automation without specifying the ID. | Pass the snapshot ID directly: `./install-skills.sh restore <ID>` (or use `./cskill restore <ID>`). |
