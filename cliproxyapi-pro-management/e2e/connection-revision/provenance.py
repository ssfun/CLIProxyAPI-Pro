#!/usr/bin/env python3
"""Capture runner inputs before generation; record executed argv without shell parsing."""
import hashlib
import json
import pathlib
import shutil
import stat
import subprocess
import sys


def git(root, *args):
    return subprocess.check_output(["git", "-C", str(root), *args])


def digest(data):
    return hashlib.sha256(data).hexdigest()


def main():
    mode, artifact_arg, *args = sys.argv[1:]
    artifact = pathlib.Path(artifact_arg).resolve()
    inputs = artifact / "inputs.json"
    if mode == "record":
        data = json.loads(inputs.read_text())
        data["commands"].append({"cwd": str(pathlib.Path.cwd()), "argv": args})
        inputs.write_text(json.dumps(data, indent=2) + "\n")
        return
    # Reject occupied output locations before reading inputs or writing snapshots.
    # This prevents exclusion of real source trees and stale receipt reuse.
    if artifact.exists() and any(artifact.iterdir()):
        raise SystemExit("artifact directory must be empty before capture")
    repo_arg, target_arg, *invocation = args
    root = pathlib.Path(git(repo_arg, "rev-parse", "--show-toplevel").decode().strip())
    # Only executable customization/validation inputs are snapshotted. Ignore rules
    # are respected for untracked files; unrelated ignored files are never read.
    scopes = ["cliproxyapi-pro-management", "scripts/validation"]
    exclude = []
    if artifact.is_relative_to(root):
        relative_artifact = artifact.relative_to(root).as_posix()
        if git(root, "ls-files", "-z", "--", ":(literal)" + relative_artifact):
            raise SystemExit("artifact directory must not contain tracked inputs")
        if artifact == root or root / "cliproxyapi-pro-management" == artifact:
            raise SystemExit("artifact directory must not be a source root")
        exclude = [":(exclude)" + artifact.relative_to(root).as_posix() + "/**"]
    status = git(root, "status", "--porcelain=v1", "-z", "--", ".", *exclude)
    snapshot = artifact / "pro-inputs"
    snapshot.mkdir(parents=True, exist_ok=True)
    hashes = {}
    for name, options in [("worktree", []), ("staged", ["--cached"]), ("combined", ["HEAD"])]:
        diff = git(root, "diff", *options, "--binary", "--", *scopes, *exclude)
        (snapshot / (name + ".patch")).write_bytes(diff)
        hashes[name] = digest(diff)
    untracked = []
    paths = git(root, "ls-files", "--others", "--exclude-standard", "-z", "--", *scopes, *exclude)
    for raw in paths.split(b"\0"):
        if not raw:
            continue
        relative = pathlib.Path(raw.decode())
        source = root / relative
        destination = snapshot / "untracked" / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        if source.is_symlink():
            # Record the link itself, never read a potentially unrelated target.
            content = str(source.readlink()).encode()
            destination = destination.with_name(destination.name + ".symlink-target")
            destination.write_bytes(content)
            kind = "symlink"
        else:
            content = source.read_bytes()
            shutil.copyfile(source, destination)
            kind = "file"
        untracked.append({"path": relative.as_posix(), "sha256": digest(content), "kind": kind, "mode": oct(stat.S_IMODE(source.lstat().st_mode)),
                          "snapshot": str(destination.relative_to(artifact))})
    data = {
        "pro_commit_sha": git(root, "rev-parse", "HEAD").decode().strip(),
        "pro_worktree_dirty": bool(status),
        "dirty_diff_sha256": hashes["combined"],
        "pro_worktree_diff_hash": "sha256:" + hashes["combined"],
        "pro_worktree_diff_hash_scope": scopes,
        "diff_sha256": hashes,
        "snapshot_scope": scopes,
        "artifact_excluded_from_status": bool(exclude),
        "untracked_customization_files": untracked,
        "upstream_sha": git(target_arg, "rev-parse", "HEAD").decode().strip(),
        "python_version": sys.version,
        "bun_version": subprocess.check_output(["bun", "--version"], text=True).strip(),
        "invocation": invocation,
        "commands": [],
    }
    inputs.write_text(json.dumps(data, indent=2) + "\n")


if __name__ == "__main__":
    main()
