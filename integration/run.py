#!/usr/bin/env python3
"""Exercise the adapter against real, pinned CodeGraph indexes; no private data."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent
CODEGRAPH = os.environ.get("CODEGRAPH_BIN", str(ROOT / "integration/node_modules/.bin/codegraph"))
ENV = dict(os.environ, DO_NOT_TRACK="1", CODEGRAPH_TELEMETRY="0")

def command(args, expected=0):
    result = subprocess.run([str(a) for a in args], env=ENV, capture_output=True, text=True, timeout=180)
    if result.returncode != expected:
        raise AssertionError(f"exit {result.returncode}, expected {expected}\n{result.stdout}\n{result.stderr}")
    return result.stdout

with tempfile.TemporaryDirectory(prefix="archcheck integration ") as temp:
    work = Path(temp)
    binary = work / "archcheck"
    command(["go", "build", "-o", binary, ROOT / "cmd/archcheck"])
    for language in ("js", "ruby"):
        project = work / language
        shutil.copytree(ROOT / "integration/fixtures" / language, project, ignore=shutil.ignore_patterns(".codegraph"))
        command([CODEGRAPH, "init", "--yes", project])
        scan = [binary, "scan", "--project", project, "--format", "json", "--codegraph", CODEGRAPH]
        report = json.loads(command(scan, 1))
        assert report["status"] == "violations", report
        assert any(f["edge"] == "calls" and f["line"] > 1 for f in report["findings"]), report
        assert report["coverage"]["synced"]
        if language == "js":
            assert any(f["edge"] == "imports" for f in report["findings"]), report
            command(scan + ["--strict"], 1)
            source = project / "src/ui/orders.js"
            source.write_text("export function submitOrder(order) { return order; }\n")
        else:
            assert report["coverage"]["unresolved_references"] > 0, report
            strict = json.loads(command(scan + ["--strict"], 2))
            assert strict["status"] == "blocked", strict
            source = project / "app/controllers/orders_controller.rb"
            source.write_text("class OrdersController\n  def create\n    100\n  end\nend\n")
        stale = json.loads(command(scan + ["--no-sync"], 2))
        assert stale["status"] == "blocked" and "устарел" in stale["errors"][0], stale
        clean = json.loads(command(scan, 0))
        assert clean["status"] == "no_observed_violations" and not clean["findings"], clean
        if language == "js":
            # A newly added, previously unindexed source must be picked up by default sync.
            (project / "src/ui/new.js").write_text("import { storeOrder } from '../domain/store.js';\nexport function newOrder(o) { return storeOrder(o); }\n")
            new = json.loads(command(scan, 1))
            assert any(f["file"] == "src/ui/new.js" for f in new["findings"]), new
        config = project / "archcheck.yaml"
        config.write_text(config.read_text().replace("app/controllers/**", "absent/**").replace("src/ui/**", "absent/**"))
        empty = json.loads(command(scan, 2))
        assert empty["status"] == "blocked" and any("селектор" in e for e in empty["errors"]), empty
        print(f"{language}: real graph, violations, repair, stale index, coverage, and exit codes OK")

    project = work / "typescript"
    shutil.copytree(ROOT / "integration/fixtures/typescript", project)
    command([CODEGRAPH, "init", "--yes", project])
    scan = [binary, "scan", "--project", project, "--format", "json", "--codegraph", CODEGRAPH]
    report = json.loads(command(scan, 0))
    assert report["status"] == "no_observed_violations" and not report["findings"], report
    assert report["coverage"]["excluded_dispatch_edges"] > 0, report
    config = project / "archcheck.yaml"
    config.write_text(config.read_text().replace('src/port.ts', 'TEMP').replace('src/adapter.ts', 'src/port.ts').replace('TEMP', 'src/adapter.ts').replace('[calls]', '[implements]'))
    actual_dependency = json.loads(command(scan, 1))
    assert any(f["edge"] == "implements" for f in actual_dependency["findings"]), actual_dependency
    print("typescript: real interface dispatch bridges excluded, source implements dependency retained OK")
