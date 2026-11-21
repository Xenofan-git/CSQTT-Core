//go:build linux

package main

import (
    "os"
    "strings"
    "testing"
)

func TestCSQTT250SupervisorNormalizesOnlyLegacyIdentityAndPassword(t *testing.T) {
    input:=[]string{"/opt/legacy-client","--peer","127.0.0.1:9000",
        "--password","private-config-secret","--device-id","legacy-device",
        "-n","9","--listen","127.0.0.1:52230","--vk","test-vk"}
    got,err:=csqtt250NormalizeSupervisorArgv(input)
    if err != nil { t.Fatal(err) }
    joined:=strings.Join(got," ")
    for _, disallowed:=range []string{"private-config-secret","legacy-device","--password","--device-id"} {
        if strings.Contains(joined,disallowed){t.Fatalf("legacy secrets escaped into new argv: %s",disallowed)}
    }
    for _, allowed:=range []string{"--peer","--listen","--vk","-n"} {
        if !strings.Contains(joined,allowed){t.Errorf("supported flag dropped: %s",allowed)}
    }
}

func TestCSQTT250SupervisorRejectsUnknownAndAmbiguousFlags(t *testing.T) {
    for _, args:=range [][]string{
        {"/client","--password"},
        {"/client","--device-id"},
        {"/client","--unknown","x"},
        {"/client","--allow-hash-redistribution"},
        {"/client","--protocol","openflux"},
        {},
    } {
        if _,err:=csqtt250NormalizeSupervisorArgv(args);err==nil {t.Errorf("unsupported legacy argv accepted: %v",args)}
    }
}

func TestCSQTT250SupervisorOptInNeverSilentlyFallsBack(t *testing.T) {
    old:=os.Getenv("CSQTT_CORE_250_EXPERIMENT")
    t.Cleanup(func(){_ = os.Setenv("CSQTT_CORE_250_EXPERIMENT",old)})
    for _,tc:=range []struct{value string; selected, ok bool}{
        {"",false,true},{"isolated-only",true,true},{"production",false,false},{"true",false,false},
    } {
        _=os.Setenv("CSQTT_CORE_250_EXPERIMENT",tc.value)
        selected,err:=csqtt250CandidateMode()
        if selected!=tc.selected || (err==nil)!=tc.ok {
            t.Fatalf("mode %q selection=%v error=%v",tc.value,selected,err)
        }
    }
}

func TestCSQTT250SupervisorBinaryRequiresRealPinnedELF(t *testing.T) {
    old:=os.Getenv("CSQTT_250_ORIGINAL_CLIENT_BINARY")
    t.Cleanup(func(){_ = os.Setenv("CSQTT_250_ORIGINAL_CLIENT_BINARY",old)})
    _=os.Setenv("CSQTT_250_ORIGINAL_CLIENT_BINARY","/invalid/nonexistent")
    if _,err:=csqtt250VerifiedBinaryPath();err==nil {t.Fatal("accepted nonexistent binary")}
    exe,err:=os.Executable()
    if err!=nil{t.Fatal(err)}
    _=os.Setenv("CSQTT_250_ORIGINAL_CLIENT_BINARY",exe)
    if _,err:=csqtt250VerifiedBinaryPath();err==nil {t.Fatal("accepted wrong ELF checksum")}
}
