//go:build linux

// Isolated CSQTT 2.5.0 process-launch compatibility helper.
// This file is not used by the production Go runner until its integration
// and independent WIRE-3 server acceptance have been demonstrated.
package main

import (
 "fmt"
 "os"
 "os/exec"
 "path/filepath"
 "strings"
 "syscall"
)

// csqtt250ClientCommand runs the original Rust client directly, without a
// shell or argv credentials. The caller is responsible for selecting the
// correct CSQTT 2.5.0 binary and providing its existing non-auth CLI options.
func csqtt250ClientCommand(binary string, args []string, password string, inheritedEnv []string) (*exec.Cmd, error) {
 if !filepath.IsAbs(binary) { return nil, fmt.Errorf("CSQTT 2.5 client path must be absolute") }
 info, err := os.Lstat(binary)
 if err != nil { return nil, fmt.Errorf("CSQTT 2.5 client not accessible: %w", err) }
 if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
  return nil, fmt.Errorf("CSQTT 2.5 client must be a regular executable file")
 }
 if password == "" { return nil, fmt.Errorf("CSQTT 2.5 tunnel password is empty") }
 for _, arg := range args {
  for _, legacy := range []string{"-password", "--password", "-device-id", "--device-id", "-client-id", "--client-id"} {
   if arg == legacy || strings.HasPrefix(arg, legacy+"=") {
    return nil, fmt.Errorf("CSQTT 2.5 rejects a legacy identity or password flag")
   }
  }
 }
 cmd := exec.Command(binary, args...)
 // Existing duplicate or inherited values must never override the requested
 // credentials. The password never appears in the process argv or a log.
 env := make([]string, 0, len(inheritedEnv)+1)
 for _, item := range inheritedEnv {
  if !strings.HasPrefix(item, "CSQTT_PASSWORD=") && !strings.HasPrefix(item, "CSQTT_250_HELPER=") { env = append(env, item) }
 }
 cmd.Env = append(env, "CSQTT_PASSWORD="+password)
 cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
 return cmd, nil
}
