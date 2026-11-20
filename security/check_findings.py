import json
import sys
from pathlib import Path

report = json.loads(Path(sys.argv[1]).read_text())
findings = {(v["VulnerabilityID"], v["PkgName"], v.get("FixedVersion") or "")
            for result in report.get("Results", [])
            for v in result.get("Vulnerabilities") or []}
for advisory, package, fix in sorted(findings):
    print(f"{advisory} {package}: {fix or 'no fix available'}")
raise SystemExit(any(fix for _, _, fix in findings))
