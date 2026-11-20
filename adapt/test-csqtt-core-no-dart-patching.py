#!/usr/bin/env python3
"""Offline source boundary regression: no Dart mutations in Linux Go patcher."""
from pathlib import Path

POLICY=Path("adapt/apply-pinned-original-linux-arm64-policy.py")
WORKFLOW=Path(".github/workflows/build-original-linux-arm64.yml")
OWNED_PACKAGE=Path("adapt/package-csqtt-core-web.py")


def verify(policy: str, workflow: str, packaging: str) -> None:
    for marker in (
        "Frontend/Core/", ".dart", "API_DART", "SETTINGS_PAGE",
        "flutter_bootstrap_js", "NANOPI_JS", "api.dart",
        "deploy_page.dart", "info_page.dart", "main.dart",
    ):
        if marker in policy:
            raise AssertionError(f"Flutter/Dart mutation found in Go patcher: {marker}")
    for marker in (
        "adapt/patch-csqtt-deploy-ui.py", "flutter build web",
        "flutter pub get", "subosito/flutter-action",
        "Frontend/Core/lib/pages/deploy_page.dart",
    ):
        if marker in workflow:
            raise AssertionError(f"Obsolete Flutter build or assertion: {marker}")
    for marker in (
        "DirectDomains           string",
        "DirectIPs               string",
        "func (t *LinuxTun) ApplyDirectRouting() error",
        "func (t *LinuxTun) CleanupDirectRouting()",
        "func prepareDirectPlan(domainsRaw string)",
    ):
        if marker not in policy:
            raise AssertionError(f"Real Go Direct routing damaged: {marker}")
    if "adapt/package-csqtt-core-web.py --output csqtt-core-web-dist" not in workflow:
        raise AssertionError("CSQTT native web packaging missing from integrated build")
    if "cp -a csqtt-core-web-dist/. upstream-lalune/Desktop/Linux/frontend/" not in workflow:
        raise AssertionError("CSQTT web not copied into backend embedded frontend")
    if "adapt/csqtt-panel-preview.html" not in packaging:
        raise AssertionError("native panel source not in web packaging")
    if "adapt/nanopi.js" not in packaging:
        raise AssertionError("native API bridge source not in web packaging")


def must_refuse(policy: str, workflow: str, packaging: str, label: str) -> None:
    try:
        verify(policy, workflow, packaging)
    except AssertionError:
        print("PASS refuse obsolete dependency:", label)
    else:
        raise AssertionError("source-boundary guard failed to detect: "+label)


def main() -> None:
    policy=POLICY.read_text()
    workflow=WORKFLOW.read_text()
    packaging=OWNED_PACKAGE.read_text()
    verify(policy, workflow, packaging)
    must_refuse(policy+"\n# Frontend/Core/lib/api.dart\n", workflow, packaging,
                "Go patcher starts mutating Dart")
    must_refuse(policy, workflow+"\nflutter build web --release\n", packaging,
                "Flutter compiler returns")
    must_refuse(policy.replace("func prepareDirectPlan(domainsRaw string)",
                              "func lostDirectPlan(domainsRaw string)",1),
                workflow,packaging,"Go Direct routing removed")
    must_refuse(policy,workflow.replace("cp -a csqtt-core-web-dist/.",
                                       "cp -a external-static-web/.",1),
                packaging,"Go embed built from external frontend")
    print("PASS owned CSQTT Core web/Go patch boundary and Go Direct compatibility")


if __name__=="__main__":
    main()
