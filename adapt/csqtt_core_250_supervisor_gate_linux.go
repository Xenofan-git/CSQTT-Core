//go:build linux

package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const csqtt250VerifiedClientSHA256 = "3c76a4323ef784568a38a82e206cecfa7657d93c01b6a69033a2f7a2f44760fa"

// This stage is opt-in and intentionally cannot affect the existing WIRE-4
// bundle. Invalid nonempty values fail closed instead of silently reverting
// to the legacy client and leaking its incompatible CLI flags.
func csqtt250CandidateMode() (bool, error) {
	mode := os.Getenv("CSQTT_CORE_250_EXPERIMENT")
	if mode == "" { return false, nil }
	if mode != "isolated-only" { return false, fmt.Errorf("CSQTT 2.5 candidate mode must be isolated-only") }
	return true, nil
}

func csqtt250VerifiedBinaryPath() (string, error) {
	path := os.Getenv("CSQTT_250_ORIGINAL_CLIENT_BINARY")
	if !filepath.IsAbs(path) { return "", fmt.Errorf("original 2.5 client ELF path must be absolute") }
	file, err := os.Lstat(path)
	if err != nil { return "", fmt.Errorf("read original 2.5 client ELF: %w", err) }
	if !file.Mode().IsRegular() || file.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("original 2.5 client must be an executable regular file")
	}
	payload, err := os.ReadFile(path)
	if err != nil { return "", fmt.Errorf("hash original 2.5 client: %w", err) }
	if got := fmt.Sprintf("%x", sha256.Sum256(payload)); got != csqtt250VerifiedClientSHA256 {
		return "", fmt.Errorf("original 2.5 client ELF has unexpected SHA256")
	}
	return path, nil
}

// Bridge emits legacy flags for backwards compatibility. Strip ONLY the two
// obsolete identity/password options; keep the canonical tunnel password in
// the supervisor's private file, then pass it to the new client as an env var.
// Reject all unknown flags and the removed redistribution option.
func csqtt250NormalizeSupervisorArgv(legacy []string) ([]string, error) {
	if len(legacy) < 1 { return nil, fmt.Errorf("CSQTT 2.5 requires a client command") }
	valued := map[string]bool{
		"--peer":true,"--vk":true,"--vk-hash-mode":true,
		"--vk-auth-mode":true,"--listen":true,"--turn":true,
		"--port":true,"-n":true,"--obfs":true,
		"--fingerprint":true,"--client-ids":true,
		"--captcha-mode":true,"--turn-transport":true,
	}
	flags := map[string]bool{"--validate-vk-hashes":true}
	removed := map[string]bool{"--password":true,"--device-id":true}
	result := make([]string,0,len(legacy))
	for i:=1;i<len(legacy);i++ {
		flag:=legacy[i]
		switch {
		case removed[flag]:
			if i+1>=len(legacy) || strings.HasPrefix(legacy[i+1],"--") {
				return nil,fmt.Errorf("CSQTT 2.5 invalid retired option value for %s",flag)
			}
			i++
		case valued[flag]:
			if i+1>=len(legacy) { return nil,fmt.Errorf("CSQTT 2.5 missing value for %s",flag) }
			result=append(result,flag,legacy[i+1])
			i++
		case flags[flag]:
			result=append(result,flag)
		default:
			return nil,fmt.Errorf("CSQTT 2.5 unsupported client option %q",flag)
		}
	}
	return result,nil
}
