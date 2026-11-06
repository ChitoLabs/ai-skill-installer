#!/usr/bin/env python3
"""Public CLI/PTY smoke checks. Usage: python3 tests/test-tui.py /path/to/cskill."""

import errno
import fcntl
import os
import pathlib
import pty
import select
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time


class Terminal:
    def __init__(self, binary, environment, args=()):
        self.master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 48, 240, 0, 0))
        self.process = subprocess.Popen(
            [str(binary), *args], stdin=slave, stdout=slave, stderr=slave,
            env=environment, start_new_session=True,
        )
        os.close(slave)
        self.output = b""
        self.closed = False

    def send(self, keys):
        os.write(self.master, keys)

    def expect(self, text, timeout=15):
        expected = text.encode()
        start = len(self.output)
        deadline = time.monotonic() + timeout
        while expected not in self.output[start:]:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise AssertionError(f"Timed out waiting for {text!r}: {self.output[-4000:]!r}")
            ready, _, _ = select.select([self.master], [], [], remaining)
            if not ready:
                continue
            try:
                data = os.read(self.master, 65536)
            except OSError as error:
                if error.errno != errno.EIO:
                    raise
                data = b""
            if not data:
                raise AssertionError(f"Terminal exited before {text!r}: {self.output[-4000:]!r}")
            self.output += data
        return self.output[start:]

    def close(self):
        if self.closed:
            return
        self.closed = True
        if self.process.poll() is None:
            os.killpg(self.process.pid, signal.SIGKILL)
            self.process.wait(timeout=5)
        os.close(self.master)

    def quit(self):
        self.send(b"q")
        assert self.process.wait(timeout=10) == 0, self.output[-4000:]
        self.close()


def main():
    if len(sys.argv) != 2:
        raise SystemExit(__doc__)
    binary = pathlib.Path(sys.argv[1]).resolve(strict=True)
    temporary_root = os.environ.get("TMPDIR", str(pathlib.Path.cwd()))
    with tempfile.TemporaryDirectory(prefix="tui-smoke-", dir=temporary_root) as directory:
        root = pathlib.Path(directory)
        home = root / "home"
        home.mkdir()
        clone_temporary_root = root / "clone-tmp"
        clone_temporary_root.mkdir()
        environment = os.environ.copy()
        environment.update({
            "HOME": str(home), "TERM": "xterm-256color", "NO_COLOR": "1",
            "TMPDIR": str(clone_temporary_root), "SKILL_PACK_BRANCH": "main",
            "SKILL_PACK_MIN_SKILL_COUNT": "2",
            "SKILL_BACKUP_DIR": str(root / "backups"),
            "SKILL_INSTALLER_LOCK_FILE": str(root / "installer.lock"),
            "SKILL_INSTALLER_STATE_FILE": str(root / "installed.state"),
        })
        targets = [root / name / "skills" for name in ("opencode", "claude", "agy", "agents")]
        for variable, target in zip(
            ("OPENCODE_SKILLS_DIR", "CLAUDE_SKILLS_DIR", "AGY_SKILLS_DIR", "AGENTS_SKILLS_DIR"), targets
        ):
            environment[variable] = str(target)
        managed = [*targets, root / "backups", root / "installer.lock", root / "installed.state"]

        def assert_unmodified():
            for path in managed:
                assert not path.exists(), f"Unexpected mutation: {path}"

        plain = subprocess.run([str(binary)], env=environment, capture_output=True, timeout=10)
        assert (plain.returncode, plain.stdout, plain.stderr) == (0, b"C-Skills\n", b""), plain
        rejected = subprocess.run([str(binary), "menu"], env=environment, capture_output=True, timeout=10)
        assert (rejected.returncode, rejected.stdout, rejected.stderr) == (
            1, b"", b"menu requires an interactive terminal\n"
        ), rejected
        assert_unmodified()
        print("PASS: headless logo and explicit menu rejection, no mutation")

        interactive = environment.copy()
        interactive.pop("NO_COLOR", None)
        terminal = Terminal(binary, interactive)
        try:
            terminal.expect("Choose an action")
            terminal.quit()
            assert_unmodified()
        finally:
            terminal.close()
        print("PASS: automatic TTY menu and safe quit")

        terminal = Terminal(binary, environment, ("menu",))
        try:
            terminal.expect("Choose an action")
            terminal.send(b"\r")
            output = terminal.expect("Continue?")
            for target in targets:
                assert str(target).encode() in output, output
            terminal.send(b"\r")
            terminal.expect("Choose an action")
            terminal.quit()
            assert_unmodified()
        finally:
            terminal.close()
        print("PASS: confirmation defaults to cancel, all four paths visible")

        terminal = Terminal(binary, environment, ("menu",))
        try:
            terminal.expect("Choose an action")
            terminal.process.send_signal(signal.SIGINT)
            assert terminal.process.wait(timeout=10) == 0, terminal.output
            assert_unmodified()
        finally:
            terminal.close()
        print("PASS: external SIGINT exits idle menu without mutation")

        repository = root / "source"
        repository.mkdir()
        subprocess.run(["git", "init", "--quiet", "--initial-branch=main", str(repository)], check=True)
        for name in ("alpha", "beta"):
            skill = repository / "skills" / name
            skill.mkdir(parents=True)
            (skill / "SKILL.md").write_text(
                f"---\nname: {name}\ndescription: Fixture skill\n---\n\n# {name}\n", encoding="utf-8"
            )
        subprocess.run(["git", "-C", str(repository), "add", "skills"], check=True)
        subprocess.run([
            "git", "-C", str(repository), "-c", "user.name=TUI Test",
            "-c", "user.email=tui@example.invalid", "commit", "--quiet", "-m", "test: fixture skills"
        ], check=True)
        environment["SKILL_PACK_REPOSITORY_URL"] = repository.as_uri()
        terminal = Terminal(binary, environment, ("menu",))
        try:
            terminal.expect("Choose an action")
            terminal.send(b"\r")
            terminal.expect("Continue?")
            terminal.send(b"j\r")
            terminal.expect("Complete.")
            assert b"Installed 2 skills" in terminal.output, terminal.output[-4000:]
            assert b"removed existing skills" in terminal.output, terminal.output[-4000:]
            for target in targets:
                for name in ("alpha", "beta"):
                    assert (target / name / "SKILL.md").read_bytes() == (
                        repository / "skills" / name / "SKILL.md"
                    ).read_bytes()
            snapshots = list((root / "backups").iterdir())
            assert len(snapshots) == 1, snapshots
            assert (snapshots[0] / "metadata").is_file()
            assert (root / "installed.state").is_file()
            terminal.quit()
        finally:
            terminal.close()
        print("PASS: confirmed local install populates four targets, snapshot and state")

        terminal = Terminal(binary, environment, ("menu",))
        try:
            terminal.expect("Choose an action")
            terminal.send(b"jj\r")
            terminal.expect("Complete.")
            assert snapshots[0].name.encode() in terminal.output, terminal.output[-4000:]
            terminal.quit()
            assert len(list((root / "backups").iterdir())) == 1
        finally:
            terminal.close()
        print("PASS: backup listing uses existing engine without creating snapshots")

    print("PASS: isolated public CLI and PTY smoke checks")


if __name__ == "__main__":
    main()
