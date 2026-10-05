#!/usr/bin/env python3
"""Local verification on selected working-tree sources. Never uploads project data.

Creates an ignored, owner-only evidence directory in the target repository.
The application is parsed, not executed. Witness rules verify observed graph
edges; they are test probes, not architecture policies.
"""
import argparse
import datetime
import hashlib
import json
import os
import re
from pathlib import Path
import shutil
import sqlite3
import subprocess
import time

EXTENSIONS = {".rb", ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx"}

def run(args, cwd, env, log=None, timeout=600):
    start = time.monotonic()
    result = subprocess.run([str(x) for x in args], cwd=cwd, env=env, capture_output=True, text=True, timeout=timeout)
    if log:
        log.write_text(result.stdout + "\n--- stderr ---\n" + result.stderr)
        log.chmod(0o600)
    return result, round(time.monotonic() - start, 3)

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("project", type=Path)
    parser.add_argument("--include", action="append", required=True, help="source file/directory relative to project; repeatable")
    parser.add_argument("--exclude", action="append", default=[], help="excluded relative directory/file prefix")
    parser.add_argument("--metadata", action="append", default=[], help="package.json or tsconfig/jsconfig metadata; never .env")
    parser.add_argument("--unignore-dir", action="append", default=[], help="explicitly include selected first-party directories ignored by CodeGraph defaults")
    parser.add_argument("--rules", type=Path, help="project-specific YAML rules; retained locally")
    parser.add_argument("--codegraph", type=Path, required=True)
    parser.add_argument("--archcheck", type=Path, required=True)
    parser.add_argument("--label", default="verification")
    args = parser.parse_args()
    root = args.project.resolve()
    for prefix in args.include + args.exclude + args.metadata:
        if Path(prefix).is_absolute() or ".." in Path(prefix).parts:
            parser.error("include/exclude must stay within project")
    env = dict(os.environ, DO_NOT_TRACK="1", CODEGRAPH_TELEMETRY="0", CODEGRAPH_NO_DAEMON="1")
    codegraph, archcheck = args.codegraph.resolve(), args.archcheck.resolve()
    gitdir = subprocess.check_output(["git", "rev-parse", "--git-path", "info/exclude"], cwd=root, text=True).strip()
    exclude_file = Path(gitdir)
    if not exclude_file.is_absolute(): exclude_file = root / exclude_file
    exclude_file.parent.mkdir(parents=True, exist_ok=True)
    existing = exclude_file.read_text() if exclude_file.exists() else ""
    if "/.archcheck-verification/" not in existing.splitlines():
        exclude_file.write_text(existing.rstrip() + "\n/.archcheck-verification/\n")
    base = root / ".archcheck-verification"
    base.mkdir(mode=0o700, exist_ok=True)
    base.chmod(0o700)
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H%M%SZ")
    evidence = base / stamp
    evidence.mkdir(mode=0o700)
    snapshot = evidence / "snapshot"
    snapshot.mkdir(mode=0o700)
    files = subprocess.check_output(["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"], cwd=root).decode().split("\0")
    manifest = []
    def in_prefix(path, prefix):
        return path == prefix.rstrip("/") or path.startswith(prefix.rstrip("/") + "/")
    for rel in sorted(set(files)):
        if not rel or not any(in_prefix(rel, p) for p in args.include): continue
        if any(in_prefix(rel, p) for p in args.exclude): continue
        source = root / rel
        if source.suffix not in EXTENSIONS or source.name.endswith(".min.js"): continue
        if source.is_symlink() or not source.is_file(): continue
        # No environment, archives, dependency trees, or configuration documents.
        if any(p in {"node_modules", ".git", ".codegraph", ".archcheck-verification", "prompts"} for p in source.relative_to(root).parts): continue
        dest = snapshot / rel
        dest.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, dest)
        dest.chmod(0o600)
        manifest.append({"path": rel, "sha256": hashlib.sha256(dest.read_bytes()).hexdigest()})
    if not manifest: raise RuntimeError("no selected source files")
    for rel in args.metadata:
        source = root / rel
        if not re.fullmatch(r"(?:package|jsconfig|tsconfig(?:\.[\w-]+)?)\.json", source.name):
            parser.error("metadata must be package.json, jsconfig.json, or tsconfig*.json")
        if source.is_symlink() or not source.is_file(): raise RuntimeError(f"metadata unavailable: {rel}")
        dest = snapshot / rel
        dest.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, dest)
        dest.chmod(0o600)
    unignore = []
    for name in args.unignore_dir:
        if not re.fullmatch(r"[\w-]+", name): parser.error("unignore-dir must be a directory name")
        unignore.extend([f"!**/{name}/", f"!**/{name}/**"])
    (snapshot / ".gitignore").write_text("\n".join(unignore) + "\n.codegraph/\n")
    write_json(evidence / "manifest.json", manifest)
    head = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()
    summary = {"label": args.label, "head": head, "scope": args.include, "excluded": args.exclude,
               "source_files": len(manifest), "codegraph_version": "1.6.2", "checks": {}, "timings_seconds": {}}
    summary["working_tree_snapshot"] = True
    summary["metadata_files"] = args.metadata
    summary["unignored_directories"] = args.unignore_dir
    result, duration = run([codegraph, "init", "--yes", snapshot], root, env, evidence / "index.log")
    summary["timings_seconds"]["index"] = duration
    if result.returncode:
        summary["checks"]["index"] = "failed"
        write_json(evidence / "summary.json", summary)
        raise RuntimeError(f"index failed; inspect {evidence / 'index.log'}")
    database = snapshot / ".codegraph/codegraph.db"
    db = sqlite3.connect(f"file:{database}?mode=ro", uri=True)
    db.row_factory = sqlite3.Row
    summary["coverage"] = {
        "indexed_files": db.execute("select count(*) from files").fetchone()[0],
        "nodes": db.execute("select count(*) from nodes").fetchone()[0],
        "edges": db.execute("select count(*) from edges").fetchone()[0],
        "unresolved_references": db.execute("select count(*) from unresolved_refs").fetchone()[0],
        "parse_error_files": db.execute("select count(*) from files where errors is not null and errors not in ('','[]','null')").fetchone()[0],
    }
    indexed_paths = {r[0] for r in db.execute("select path from files")}
    missing = [r["path"] for r in manifest if r["path"] not in indexed_paths]
    summary["coverage"]["unindexed_source_files"] = len(missing)
    write_json(evidence / "unindexed-source-files.json", missing)
    summary["checks"]["source_coverage"] = "passed" if not missing else "incomplete"
    parse_errors = [dict(x) for x in db.execute("select path,errors from files where errors is not null and errors not in ('','[]','null')")]
    write_json(evidence / "parse-errors.json", parse_errors)
    # A cross-file resolved call/import is an independent witness in the graph.
    edge = db.execute("""select s.file_path as source_file,t.file_path as target_file,
       s.name as source_name,t.name as target_name,e.kind,e.line
       from edges e join nodes s on s.id=e.source join nodes t on t.id=e.target
       where s.file_path != t.file_path and e.kind in ('calls','imports')
       and coalesce(json_extract(e.metadata,'$.synthesizedBy'),'') != 'interface-impl'
       order by case e.kind when 'calls' then 0 else 1 end,s.file_path,t.file_path,e.line limit 1""").fetchone()
    if edge is None: raise RuntimeError("no cross-file witness found")
    edge = dict(edge)
    write_json(evidence / "witness-edge.json", edge)
    def policy(identifier, source, target, kind):
        # JSON is valid YAML, so no third-party Python YAML dependency needed.
        return {"version": 1, "rules": [{"id": identifier, "description": "Контрольный тест существующей связи; не архитектурная политика.",
                "from": {"paths": [source]}, "to": {"paths": [target]}, "edges": [kind]}]}
    probe = evidence / "witness-rule.yaml"
    write_json(probe, policy("verification-witness", edge["source_file"], edge["target_file"], edge["kind"]))
    scan = [archcheck, "scan", "--project", snapshot, "--codegraph", codegraph, "--format", "json", "--timeout", "10m"]
    def check(name, rules, flags=()):
        result, duration = run(scan + ["--config", rules] + list(flags), root, env, evidence / f"{name}.log")
        report = json.loads(result.stdout)
        write_json(evidence / f"{name}.json", report)
        summary["timings_seconds"][name] = duration
        return result.returncode, report
    exitcode, report = check("witness", probe)
    if summary["coverage"]["parse_error_files"]:
        summary["checks"]["witness"] = "blocked_by_parse_errors"
        summary["checks"]["strict"] = "blocked_by_parse_errors"
        if exitcode != 2: raise AssertionError("parse errors did not block scan")
    else:
        expected = db.execute("""select distinct s.id as source,t.id as target,e.kind,coalesce(nullif(e.line,0),s.start_line) as line
            from edges e join nodes s on s.id=e.source join nodes t on t.id=e.target
            where s.file_path=? and t.file_path=? and e.kind=?
            and coalesce(json_extract(e.metadata,'$.synthesizedBy'),'') != 'interface-impl'""",
            (edge["source_file"], edge["target_file"], edge["kind"])).fetchall()
        expected_keys = {(r["source"], r["target"], r["kind"], r["line"]) for r in expected}
        actual_keys = {(f["from"]["id"], f["to"]["id"], f["edge"], f["line"]) for f in report["findings"]}
        assert exitcode == 1 and actual_keys == expected_keys, (exitcode, len(actual_keys), len(expected_keys))
        summary["checks"]["witness"] = "passed"
        summary["witness_findings"] = len(actual_keys)
        summary["coverage"]["excluded_dispatch_edges"] = report["coverage"].get("excluded_dispatch_edges", 0)
        # Same populated file selectors, with a relation absent between them.
        absent_kind = next(kind for kind in ("extends", "implements", "instantiates", "references", "imports", "calls")
            if not db.execute("""select 1 from edges e join nodes s on s.id=e.source join nodes t on t.id=e.target
                where s.file_path=? and t.file_path=? and e.kind=? limit 1""",
                (edge["source_file"], edge["target_file"], kind)).fetchone())
        clean = evidence / "clean-rule.yaml"
        write_json(clean, policy("verification-clean", edge["source_file"], edge["target_file"], absent_kind))
        clean_exit, clean_report = check("clean", clean)
        assert clean_exit == 0 and clean_report["status"] == "no_observed_violations" and not clean_report["findings"]
        summary["checks"]["clean"] = "passed"
        strict_exit, _ = check("strict", probe, ["--strict"])
        assert strict_exit == (2 if summary["coverage"]["unresolved_references"] else 1)
        summary["checks"]["strict"] = "passed"
        empty = evidence / "empty-rule.yaml"
        write_json(empty, policy("empty-probe", "__archcheck_absent__/**", edge["target_file"], edge["kind"]))
        code, _ = check("empty-selector", empty)
        assert code == 2
        summary["checks"]["empty_selector"] = "passed"
        # Alter only the private snapshot; the original working tree is never edited.
        source = snapshot / edge["source_file"]
        original = source.read_bytes()
        source.write_bytes(original + b"\n")
        code, stale = check("stale-index", probe, ["--no-sync"])
        assert code == 2 and any("устарел" in e for e in stale["errors"])
        source.write_bytes(original)
        summary["checks"]["stale_index"] = "passed"
    if args.rules:
        rules = evidence / "architecture-rules.yaml"
        shutil.copyfile(args.rules.resolve(), rules)
        rules.chmod(0o600)
        code, report = check("architecture", rules)
        summary["architecture"] = {"exit_code": code, "status": report["status"], "findings": len(report["findings"]), "rules": report["rules"], "errors": report["errors"]}
    db.close()
    write_json(evidence / "summary.json", summary)
    print(json.dumps({"label": args.label, "evidence": str(evidence).replace(str(Path.home()), "~", 1),
                      "source_files": summary["source_files"], "coverage": summary["coverage"],
                      "checks": summary["checks"], "timings_seconds": summary["timings_seconds"],
                      "architecture": {k:v for k,v in summary.get("architecture",{}).items() if k not in ("rules","errors")}}, ensure_ascii=False))

def write_json(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n")
    path.chmod(0o600)

if __name__ == "__main__":
    main()
