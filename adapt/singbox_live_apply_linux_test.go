//go:build linux

package main

import (
 "bytes"
 "context"
 "fmt"
 "io"
 "net"
 "os"
 "path/filepath"
 "reflect"
 "strings"
 "testing"
 "time"
)

// A pinned real sing-box and two independent local SOCKS5 servers exercise
// actual route changes and failed startup rollback. No TUN, netns, sysctl,
// firewall, system route, external VPN service or VK credentials are touched.
func TestSingBoxLiveApplyNativeARM64WithRollback(t *testing.T) {
 binary:=strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"))
 if binary=="" {t.Skip("requires pinned native ARM64 sing-box binary")}
 echo,err:=net.Listen("tcp","127.0.0.1:0")
 if err!=nil {t.Fatal(err)}
 defer echo.Close()
 go func(){for{c,e:=echo.Accept();if e!=nil{return};go func(c net.Conn){defer c.Close();_,_=io.Copy(c,c)}(c)}}()

 observed:=make(chan string,16)
 newProxy:=func(tag string)*Socks5Server{
  t.Helper()
  server:=NewSocks5Server("127.0.0.1:0")
  server.dialTCPOverride=func(target string)(net.Conn,error) {
   observed<-tag+"|"+target
   return net.DialTimeout("tcp",echo.Addr().String(),2*time.Second)
  }
  if err:=server.Start();err!=nil{t.Fatalf("start %s SOCKS5: %v",tag,err)}
  t.Cleanup(func(){server.Stop()})
  return server
 }
 csqtt:=newProxy("csqtt")
 ext:=newProxy("external")
 csqttAddress:=csqtt.Status().Address
 dir:=t.TempDir()
 m:=NewSingBoxManager(binary,filepath.Join(dir,"sing-box.json"),filepath.Join(dir,"sing-box.log"))
 m.checkTimeout=3*time.Second // bounded failure injection (normal production uses 10s)
 defer m.Stop()
 listen:=reserveSingBoxTestAddress(t)
 old:=singBoxConfigInput{
  ListenAddress:listen,CSQTTAddress:csqttAddress,
  FinalOutbound:"socks-external",
  SOCKS5:[]singBoxSOCKS5Outbound{{ID:"external",Server:"127.0.0.1",ServerPort:mustPort(t,ext.Status().Address),Enabled:true}},
  Rules:[]singBoxRouteRule{
   {ID:singBoxDirectRuleID,Domains:[]string{"live-apply.test"},Outbound:singBoxDirectTag,Enabled:false},
   {ID:singBoxCSQTTRuleID,Domains:[]string{"live-apply.test"},Outbound:singBoxCSQTTTag,Enabled:true},
   {ID:"external-route",Domains:[]string{"live-apply.test"},Outbound:"socks-external",Enabled:true},
  },
 }
 if err:=m.SaveConfig(old);err!=nil{t.Fatalf("save old: %v",err)}
 if err:=m.Start(context.Background());err!=nil{t.Fatalf("start old: %v",err)}
 if err:=m.waitSOCKSReady(listen);err!=nil{t.Fatalf("old listener: %v",err)}

 connect:=func(wantProxy string) {
  t.Helper()
  conn,err:=net.DialTimeout("tcp",listen,2*time.Second)
  if err!=nil{t.Fatal(err)}
  defer conn.Close()
  _=conn.SetDeadline(time.Now().Add(5*time.Second))
  if _,err=conn.Write([]byte{5,1,0});err!=nil{t.Fatal(err)}
  var hello [2]byte
  if _,err=io.ReadFull(conn,hello[:]);err!=nil{t.Fatal(err)}
  if hello!=[2]byte{5,0}{t.Fatalf("SOCKS hello %v",hello)}
  hostname:="live-apply.test"
  request:=append([]byte{5,1,0,3,byte(len(hostname))},[]byte(hostname)...)
  request=append(request,1,187)
  if _,err=conn.Write(request);err!=nil{t.Fatal(err)}
  var reply [4]byte
  if _,err=io.ReadFull(conn,reply[:]);err!=nil{t.Fatal(err)}
  if reply[1]!=0{t.Fatalf("SOCKS CONNECT failed with status %d",reply[1])}
  tail:=0
  switch reply[3] {case 1:tail=6;case 4:tail=18;case 3:
   var size [1]byte
   if _,err=io.ReadFull(conn,size[:]);err!=nil{t.Fatal(err)}
   tail=int(size[0])+2
  default:t.Fatalf("unsupported reply ATYP %d",reply[3])}
  if _,err=io.CopyN(io.Discard,conn,int64(tail));err!=nil{t.Fatal(err)}
  payload:=[]byte("CSQTT ARM64 live-apply proof")
  if _,err=conn.Write(payload);err!=nil{t.Fatal(err)}
  actual:=make([]byte,len(payload))
  if _,err=io.ReadFull(conn,actual);err!=nil{t.Fatal(err)}
  if !bytes.Equal(actual,payload){t.Fatalf("echo mismatch: %q",actual)}
  select{
  case seen:=<-observed:
   want:=wantProxy+"|live-apply.test:443"
   if seen!=want{t.Fatalf("selected SOCKS5=%s, want=%s",seen,want)}
  case <-time.After(2*time.Second):t.Fatal("no outbound SOCKS5 connection recorded")
  }
 }
 connect("csqtt")
 originalPID:=m.Status().PID
 if originalPID<=0{t.Fatal("initial sing-box not running")}

 // Invalid route must fail BEFORE any stop, preserving the same process.
 malformed:=old
 malformed.Rules=[]singBoxRouteRule{{ID:singBoxCSQTTRuleID,Domains:[]string{"illegal domain"},Outbound:singBoxCSQTTTag,Enabled:true}}
 if applied,err:=m.ApplyConfig(malformed);err==nil || applied {
  t.Fatalf("invalid candidate accepted: applied=%v err=%v",applied,err)
 }
 if m.Status().PID!=originalPID{t.Fatal("preflight error interrupted original sing-box")}
 connect("csqtt")

 // Route-card-only update while engine RUNNING -> bounded sing-box restart.
 updated:=old
 updated.Rules=append([]singBoxRouteRule(nil),old.Rules...)
 updated.Rules[1].Enabled=false
 applied,err:=m.ApplyConfig(updated)
 if err!=nil||!applied{t.Fatalf("apply candidate failed: applied=%v err=%v",applied,err)}
 if m.Status().PID<=0 || m.Status().PID==originalPID{t.Fatal("sing-box was not restarted after route change")}
 if _,err:=os.Lstat(m.liveApplyJournalPath());!os.IsNotExist(err){t.Fatalf("successful apply retained uncommitted journal: %v",err)}
 if csqtt.Status().Address!=csqttAddress{t.Fatal("CSQTT Core SOCKS5 was changed by Sing-box apply")}
 persisted,err:=m.LoadConfig()
 if err!=nil || !reflect.DeepEqual(persisted,updated){t.Fatalf("updated routing did not persist: %+v err %v",persisted,err)}
 connect("external")
 stablePID:=m.Status().PID
 if applied,err=m.ApplyConfig(updated);err!=nil||!applied{t.Fatalf("no-op apply: %v",err)}
 if m.Status().PID!=stablePID{t.Fatal("no-op Save caused avoidable sing-box restart")}
 connect("external")

 oldConfig,err:=os.ReadFile(m.configPath);if err!=nil{t.Fatal(err)}
 oldPanel,err:=os.ReadFile(m.panelStatePath());if err!=nil{t.Fatal(err)}

 // sing-box check succeeds on the valid JSON, but run cannot bind this port.
 // Runtime failure must restore exact config/panel bytes and restart old listener.
 occupied,err:=net.Listen("tcp","127.0.0.1:0")
 if err!=nil{t.Fatal(err)}
 defer occupied.Close()
 badRuntime:=updated
 badRuntime.ListenAddress=occupied.Addr().String()
 applied,err=m.ApplyConfig(badRuntime)
 if err==nil || applied || !strings.Contains(err.Error(),"restored") {
  t.Fatalf("runtime failure did not report verified rollback: applied=%v err=%v",applied,err)
 }
 if m.Status().PID<=0 || m.Status().PID==stablePID {
  t.Fatalf("old Sing-box listener was not restarted after rollback: %+v",m.Status())
 }
 if _,err:=os.Lstat(m.liveApplyJournalPath());!os.IsNotExist(err){t.Fatalf("successful runtime rollback retained uncommitted journal: %v",err)}
 gotConfig,err:=os.ReadFile(m.configPath);if err!=nil{t.Fatal(err)}
 gotPanel,err:=os.ReadFile(m.panelStatePath());if err!=nil{t.Fatal(err)}
 if !bytes.Equal(oldConfig,gotConfig)||!bytes.Equal(oldPanel,gotPanel) {
  t.Fatal("rollback changed last known-good config or panel bytes")
 }
 if got,err:=m.LoadConfig();err!=nil||!reflect.DeepEqual(got,updated) {
  t.Fatalf("rollback failed to restore previous route-card state: %+v err=%v",got,err)
 }
 connect("external")
 if csqtt.Status().Address!=csqttAddress {t.Fatal("CSQTT listener unexpectedly interrupted during restart/rollback")}
 if m.Status().Error=="" {t.Fatal("rollback failure must remain visible in status")}

 // Save while STOPPED must never auto-start Sing-box unexpectedly.
 if err:=m.Stop();err!=nil{t.Fatal(err)}
 applied,err=m.ApplyConfig(old)
 if err!=nil||applied {t.Fatalf("stopped Save should not start process: applied=%v err=%v",applied,err)}
 if m.Status().Running{t.Fatal("stopped Save unexpectedly started Sing-box")}
 if got,err:=m.LoadConfig();err!=nil||!reflect.DeepEqual(got,old){
  t.Fatalf("stopped save state mismatch: %+v err=%v",got,err)
 }
 if csqtt.Status().Address!=csqttAddress {t.Fatal("CSQTT listener was stopped")}
 fmt.Println("CSQTT_CORE_REAL_ARM64_LIVE_APPLY_ROLLBACK_PASS")
}
