#!/usr/bin/env python3
"""Offline CSQTT Core API boundary checks; real Go tests run in ARM64 CI.

No servers, credentials or external source checkout are required.
"""
from pathlib import Path

HTTP = Path("adapt/headless_main.go")
BRIDGE = Path("adapt/csqtt_core_deploy_bridge_linux.go")
TEST = Path("adapt/csqtt_core_deploy_bridge_test.go")
WORKFLOW = Path(".github/workflows/build-original-linux-arm64.yml")


def verify(http: str, bridge: str, tests: str, workflow: str) -> None:
    if 'lalune-desktop/Libs' in http or 'libs.' in http:
        raise AssertionError("external Go library leaked into CSQTT Core HTTP panel")
    for token in (
        'type csqttSettingsReader interface',
        'func csqttDeviceID(core csqttSettingsReader) string',
        'csqttDeployService.DeployProtocol(string(raw))',
        'csqttDeployService.DeployLog()',
        'csqttDeployService.DeployBusy()',
        'value.Kind() == reflect.Pointer && value.IsNil()',
    ):
        if token not in http:
            raise AssertionError("CSQTT Core HTTP boundary missing: "+token)
    if bridge.count('import legacy "lalune-desktop/Libs"')!=1:
        raise AssertionError("deploy backend still depends on unexpected packages")
    for token in (
        'type csqttDeployBackend interface',
        'type csqttLegacyDeployBackend struct',
        'legacy.DeployProtocol(requestJSON)',
        'legacy.DeployLog()',
        'legacy.DeployBusy()',
        'var csqttDeployService csqttDeployBackend = csqttLegacyDeployBackend{}',
    ):
        if token not in bridge:
            raise AssertionError("original deploy implementation is not transparently wrapped: "+token)
    for token in (
        'func TestCSQTTCoreDeployBridgeIsolatesHTTPPanel',
        'func TestCSQTTCoreDeployBridgeBlocksPublicRequests',
        'func TestCSQTTCoreDeviceIDDoesNotNeedExternalGoPackage',
        'func TestCSQTTCoreDeployBridgeRefusesMalformedRequestBeforeBackend',
        'csqttDeployService = stub',
        'http.StatusForbidden',
    ):
        if token not in tests:
            raise AssertionError("isolated Go behavior test missing: "+token)
    for asset in (
        'adapt/csqtt_core_deploy_bridge_linux.go',
        'adapt/csqtt_core_deploy_bridge_test.go',
    ):
        if 'cp '+asset+' upstream-lalune/Desktop/Linux/'+asset.removeprefix('adapt/') not in workflow:
            raise AssertionError("ARM64 Go build does not link bridge or run its test: "+asset)
    if "go test -count=1 ." not in workflow:
        raise AssertionError("Go backend unit tests are not executed")
    if "go test -race -count=1 ." not in workflow:
        raise AssertionError("Go backend race tests are not executed")


def must_refuse(http: str, bridge: str, tests: str, workflow: str, label: str) -> None:
    try:
        verify(http,bridge,tests,workflow)
    except AssertionError:
        print("PASS source guard rejected:",label)
    else:
        raise AssertionError("source guard accepted regression: "+label)


def main() -> None:
    http=HTTP.read_text()
    bridge=BRIDGE.read_text()
    tests=TEST.read_text()
    workflow=WORKFLOW.read_text()
    verify(http,bridge,tests,workflow)
    must_refuse(http.replace('csqttDeployService.DeployLog()', 'libs.DeployLog()',1),
                bridge,tests,workflow,"direct legacy import into HTTP API")
    must_refuse(http,bridge.replace('legacy.DeployBusy()', 'false',1),
                tests,workflow,"adapter accidentally changes deployment liveness")
    must_refuse(http,bridge,tests,
                workflow.replace('cp adapt/csqtt_core_deploy_bridge_linux.go ',
                                 'cp adapt/missing_deploy_bridge.go ',1),
                "build omits CSQTT Core owned adapter")
    must_refuse(http,bridge,tests,
                workflow.replace('go test -race -count=1 .','echo no-race-testing',1),
                "Go race acceptance silently removed")
    print("PASS CSQTT Core HTTP panel has narrow Deploy and settings interfaces")
    print("PASS compatibility adapter preserves existing deploy implementation; full Go VM build required")


if __name__=="__main__":
    main()
