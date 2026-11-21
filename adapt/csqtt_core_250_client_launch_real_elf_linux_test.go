//go:build linux

package main

import (
    "crypto/sha256"
    "fmt"
    "os"
    "os/exec"
    "strings"
    "syscall"
    "testing"
    "time"
    "sync/atomic"
)

// This is an opt-in native ARM64 acceptance of the pinned ORIGINAL CSQTT 2.5
// Rust client binary, not a fake subprocess. The deliberately invalid obfs
// value fails before the client attempts TUN, sockets, VK or system routing.
func TestCSQTT250ActualOriginalClientAcceptsGoPasswordEnv(t *testing.T) {
    binary := os.Getenv("CSQTT_250_ORIGINAL_CLIENT_BINARY")
    if binary == "" {
        t.Skip("needs independently verified original 2.5.0 ARM64 ELF")
    }
    data, err := os.ReadFile(binary)
    if err != nil { t.Fatal(err) }
    const verifiedSHA = "3c76a4323ef784568a38a82e206cecfa7657d93c01b6a69033a2f7a2f44760fa"
    if hash := fmt.Sprintf("%x", sha256.Sum256(data)); hash != verifiedSHA {
        t.Fatalf("original CSQTT 2.5 ELF hash mismatch: %s", hash)
    }
    // A local literal peer is parsed, not contacted; invalid obfs fails before
    // dispatcher and any VPN/network operations.
    args := []string{
        "--peer", "127.0.0.1:9",
        "--vk", "synthetic-vk-hash",
        "--obfs", "invalid-sentinel",
        "--listen", "127.0.0.1:0",
    }

    run := func(cmd *exec.Cmd) string {
        t.Helper()
        var timedOut atomic.Bool
        alarm := time.AfterFunc(5*time.Second, func() {
            timedOut.Store(true)
            if cmd.Process != nil {
                _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
            }
        })
        defer alarm.Stop()
        out, err := cmd.CombinedOutput()
        if timedOut.Load() { t.Fatal("the original Rust client failed to terminate before the timeout") }
        if err == nil { t.Fatalf("sentinel obfuscation should be invalid, output=%q", out) }
        return string(out)
    }

    // The authentic 2.5.0 client does NOT accept missing env credentials.
    noEnv := exec.Command(binary, args...)
    noEnv.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
    noEnv.Env = []string{"PATH=/usr/bin:/bin"}
    missing := run(noEnv)
    if !strings.Contains(missing, "Нужен пароль в переменной CSQTT_PASSWORD") {
        t.Fatalf("expected real client password-env guard, output=%q", missing)
    }

    const password = "synthetic-e2e-password-not-production"
    cmd, err := csqtt250ClientCommand(binary, args, password, []string{
        "PATH=/usr/bin:/bin", "CSQTT_PASSWORD=old-stale-value", "CSQTT_250_HELPER=1",
    })
    if err != nil { t.Fatal(err) }
    for _, arg := range cmd.Args {
        if strings.Contains(arg, password) { t.Fatal("credential was leaked through client argv") }
    }
    output := run(cmd)
    if strings.Contains(output, "Нужен пароль в переменной CSQTT_PASSWORD") {
        t.Fatalf("Go helper failed to inject password into real Rust client environment: %q", output)
    }
    if !strings.Contains(output, "invalid-sentinel") {
        t.Fatalf("original client did not reach post-password obfuscation parser: %q", output)
    }
    t.Log("REAL_ORIGINAL_250_ARM64_CLIENT_GO_ENV_NO_ARGV_PASS")
}
