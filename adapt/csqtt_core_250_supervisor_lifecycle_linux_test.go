//go:build linux

package main

import (
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"

    libs "lalune-desktop/Libs"
)

// Real production LinuxRunner.StartCore logic, not just the isolated Go
// exec.Command helper. Native ARM64 client exits deliberately before opening
// TUN/network/VK because --obfs invalid-sentinel is rejected.
func TestCSQTT250SupervisorLaunchesRealELFAndCleansProcess(t *testing.T) {
    binary:=os.Getenv("CSQTT_250_ORIGINAL_CLIENT_BINARY")
    if binary=="" {t.Skip("requires verified original 2.5 ARM64 client")}
    t.Setenv("CSQTT_CORE_250_EXPERIMENT","isolated-only")
    t.Setenv("XDG_CONFIG_HOME",t.TempDir())
    t.Setenv("TMPDIR",t.TempDir())

    const secret="synthetic-supervisor-password"
    if err:=saveTunnelAuthPassword(secret);err!=nil {t.Fatal(err)}
    app:=tunTestApp()
    app.core.SetConnected(true)
    bridge:=libs.NewBridge(app.core,app.tun,nil)
    runner:=&LinuxRunner{app:app,startupTimeout:3*time.Second}
    legacyArgs:=[]string{
        "/nonexistent-legacy-wire4-client", "--peer","127.0.0.1:9",
        "--vk","synthetic-test-hash","--obfs","invalid-sentinel",
        "--password","legacy-config-secret","--device-id","legacy-identity",
        "--listen","127.0.0.1:52230","-n","9",
    }
    runner.StartCore(legacyArgs,52230,bridge)
    waitRunnerStopped(t,app)
    logs:=app.core.GetLogsJson()
    if !strings.Contains(logs,"Ядро запущено") {
        t.Fatalf("guarded Go supervisor never launched a real client: %s",logs)
    }
    if strings.Contains(logs,secret) || strings.Contains(logs,"legacy-config-secret") {
        t.Fatal("supervisor logs leaked a client credential")
    }
    privateLog:=filepath.Join(os.TempDir(),"csqtt-core250-lab-only.log")
    contents,err:=os.ReadFile(privateLog)
    if err!=nil {t.Fatal(err)}
    if !strings.Contains(string(contents),"invalid-sentinel") {
        t.Fatalf("real original 2.5 child did not reach validated parser: %s",contents)
    }
    if strings.Contains(string(contents),secret) || strings.Contains(string(contents),"legacy-config-secret") {
        t.Fatal("private process log leaked credentials")
    }
    if stat,err:=os.Stat(privateLog);err!=nil || stat.Mode().Perm()!=0600 {
        t.Fatalf("process log must be private 0600, err=%v",err)
    }
    t.Log("CSQTT250_REAL_GO_SUPERVISOR_LAUNCH_AND_CLEANUP_PASS")
}

func TestCSQTT250SupervisorFailsClosedBeforeSpawnForInvalidOptIn(t *testing.T) {
    t.Setenv("CSQTT_CORE_250_EXPERIMENT","production")
    t.Setenv("XDG_CONFIG_HOME",t.TempDir())
    if err:=saveTunnelAuthPassword("synthetic-only");err!=nil {t.Fatal(err)}
    app:=tunTestApp()
    app.core.SetConnected(true)
    bridge:=libs.NewBridge(app.core,app.tun,nil)
    runner:=&LinuxRunner{app:app,startupTimeout:time.Second}
    runner.StartCore([]string{"/tmp/unverified","--password","secret"},52230,bridge)
    if app.core.IsConnected(){t.Fatal("invalid candidate mode must fail closed")}
    if runner.activePID!=0 {t.Fatal("an unverified core process was started")}
}
