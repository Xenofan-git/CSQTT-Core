//go:build linux

package main

import (
  "strings"
  "testing"
)

// This checks the exact user-visible precondition used by the live
// /vpn/connect and /vpn/reconnect handlers. A 2.5 client no longer has
// the legacy per-device auth flag, but the private tunnel password stays required.
func TestCSQTT250CandidateAPIAuthDoesNotDemandRemovedDeviceID(t *testing.T) {
 t.Setenv("XDG_CONFIG_HOME",t.TempDir())
 t.Setenv("CSQTT_CORE_250_EXPERIMENT","isolated-only")
 app:=tunTestApp()
 if err:=validateTunnelAuth(app);err==nil || !strings.Contains(err.Error(),"пароль") {
  t.Fatalf("missing original 2.5 password not rejected: %v",err)
 }
 if err:=saveTunnelAuthPassword("synthetic-tunnel-secret");err!=nil {t.Fatal(err)}
 if err:=validateTunnelAuth(app);err!=nil {
  t.Fatalf("candidate 2.5 API auth must not demand removed device-id: %v",err)
 }
 t.Setenv("CSQTT_CORE_250_EXPERIMENT","invalid")
 if err:=validateTunnelAuth(app);err==nil {t.Fatal("invalid mode unexpectedly accepted")}
}
