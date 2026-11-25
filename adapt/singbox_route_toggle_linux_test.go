//go:build linux

package main

import (
 "context"
 "fmt"
 "io"
 "net"
 "os"
 "path/filepath"
 "strings"
 "testing"
 "time"
)

// Actual native ARM64 acceptance: the CSQTT SOCKS5 fixture stays online
// while only the sing-box route-card state and priority change. No TUN,
// global routing, firewall, credentials, or external destinations are used.
func TestSingBoxSystemCardToggleActualTraffic(t *testing.T) {
 binary := strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"))
 if binary == "" { t.Skip("needs pinned real sing-box executable") }
 echo,err:=net.Listen("tcp","127.0.0.1:0"); if err!=nil {t.Fatal(err)}
 defer echo.Close()
 go func(){for {c,e:=echo.Accept(); if e!=nil{return};go func(c net.Conn){defer c.Close();_,_=io.Copy(c,c)}(c)}}()
 seen:=make(chan string,16)
 makeSOCKS:=func(label string)*Socks5Server{
  t.Helper()
  // The test fixture needs a fixed endpoint across the monitor's 5s poll.
  // Port zero would rebind to a new ephemeral port on every poll.
  srv:=NewSocks5Server(reserveSingBoxTestAddress(t))
  srv.dialTCPOverride=func(target string)(net.Conn,error){
   seen <- label+"|"+target
   return net.DialTimeout("tcp",echo.Addr().String(),2*time.Second)
  }
  if err:=srv.Start();err!=nil{t.Fatalf("%s SOCKS5 start: %v",label,err)}
  t.Cleanup(func(){srv.Stop()})
  return srv
 }
 core:=makeSOCKS("csqtt")
 external:=makeSOCKS("external")
 coreAddress:=core.Status().Address
 configDir:=t.TempDir()
 manager:=NewSingBoxManager(binary,filepath.Join(configDir,"config.json"),filepath.Join(configDir,"singbox.log"))
 defer manager.Stop()
 expectRequest:=echo.Addr().String()

 connect:=func(inbound string)(bool,error){
  var conn net.Conn
  var err error
  deadline:=time.Now().Add(8*time.Second)
  for time.Now().Before(deadline) {
   conn,err=net.DialTimeout("tcp",inbound,250*time.Millisecond)
   if err==nil {break}
   time.Sleep(50*time.Millisecond)
  }
  if conn==nil{return false,fmt.Errorf("inbound not listening: %w",err)}
  defer conn.Close()
  _=conn.SetDeadline(time.Now().Add(5*time.Second))
  if _,err=conn.Write([]byte{5,1,0});err!=nil{return false,err}
  var greeting [2]byte
  if _,err=io.ReadFull(conn,greeting[:]);err!=nil{return false,err}
  if greeting!=[2]byte{5,0}{return false,fmt.Errorf("bad SOCKS5 greeting %v",greeting)}
  addr:=echo.Addr().(*net.TCPAddr)
  req:=[]byte{5,1,0,1,127,0,0,1,byte(addr.Port>>8),byte(addr.Port)}
  if _,err=conn.Write(req);err!=nil{return false,err}
  var header [4]byte
  if _,err=io.ReadFull(conn,header[:]);err!=nil{return false,err}
  if header[1]!=0{return false,nil}
  tail:=0
  switch header[3]{case 1:tail=6;case 4:tail=18;case 3:
   var size [1]byte;if _,err=io.ReadFull(conn,size[:]);err!=nil{return false,err}
   tail=int(size[0])+2
  default:return false,fmt.Errorf("invalid reply atyp %d",header[3])}
  if _,err=io.CopyN(io.Discard,conn,int64(tail));err!=nil{return false,err}
  payload:=[]byte("csqtt-core-route-toggle-real-tcp")
  if _,err=conn.Write(payload);err!=nil{return false,err}
  result:=make([]byte,len(payload))
  if _,err=io.ReadFull(conn,result);err!=nil{return false,err}
  if string(result)!=string(payload){return false,fmt.Errorf("echo mismatch %q",result)}
  return true,nil
 }

 cases:=[]struct{
  name string
  direct,csqtt,reorder,externalRule,coreDown bool
  expected string
 }{
  {"direct_overrides_csqtt",true,true,false,true,false,"direct"},
  {"csqtt_enabled_routes_to_core",false,true,false,true,false,"csqtt"},
  {"csqtt_disabled_uses_next_external",false,false,false,true,false,"external"},
  {"external_above_csqtt_wins",false,true,true,true,false,"external"},
  {"disabled_rules_use_explicit_final",false,false,false,false,false,"external"},
  {"core_listener_down_fails_closed",false,true,false,true,true,"failure"},
 }
 for _,tc:=range cases {
  t.Run(tc.name,func(t *testing.T){
   t.Cleanup(func(){if err:=manager.Stop();err!=nil{t.Errorf("cleanup sing-box: %v",err)}})
   if tc.coreDown { core.Stop() }
   rules:=[]singBoxRouteRule{
    {ID:singBoxDirectRuleID,IPCIDRs:[]string{"127.0.0.1/32"},Outbound:singBoxDirectTag,Enabled:tc.direct},
    {ID:singBoxCSQTTRuleID,IPCIDRs:[]string{"127.0.0.1/32"},Outbound:singBoxCSQTTTag,Enabled:tc.csqtt},
    {ID:"external-route",IPCIDRs:[]string{"127.0.0.1/32"},Outbound:"socks-external",Enabled:tc.externalRule},
   }
   if tc.reorder {rules[1],rules[2]=rules[2],rules[1]}
   input:=singBoxConfigInput{
    ListenAddress:reserveSingBoxTestAddress(t),CSQTTAddress:coreAddress,
    FinalOutbound:"socks-external",
    DirectIPCIDRs:[]string{"127.0.0.1/32"}, // legacy lists must not override the explicit toggle
    SOCKS5:[]singBoxSOCKS5Outbound{{ID:"external",Server:"127.0.0.1",ServerPort:mustPort(t,external.Status().Address),Enabled:true}},
    Rules:rules,
   }
   if err:=manager.SaveConfig(input);err!=nil{t.Fatalf("save: %v",err)}
   loaded,err:=manager.LoadConfig();if err!=nil{t.Fatalf("load: %v",err)}
   if len(loaded.Rules)!=3 || loaded.Rules[0].ID!=singBoxDirectRuleID || loaded.Rules[0].Enabled!=tc.direct || loaded.Rules[1].ID!=rules[1].ID || loaded.Rules[1].Enabled!=rules[1].Enabled {
    t.Fatalf("rule priority or state changed after save/load: %+v",loaded.Rules)
   }
   if err:=manager.Start(context.Background());err!=nil{t.Fatalf("start sing-box: %v",err)}
   if !manager.Status().Running {t.Fatal("sing-box not running")}
   if !tc.coreDown && core.Status().Address!=coreAddress {t.Fatal("CSQTT SOCKS5 fixture restarted unexpectedly")}
   // Confirm that saving while running fails explicitly rather than silently
   // accepting a config that will only take effect on the next restart.
   if err:=manager.SaveConfig(input);err==nil || !strings.Contains(err.Error(),"while running") {
    t.Fatalf("live save must report the current restart limitation: %v",err)
   }
   ok,err:=connect(input.ListenAddress)
   if tc.expected=="failure" {
    if err==nil && ok {t.Fatal("dead Core route incorrectly connected; must fail closed")}
   }else if err!=nil || !ok {t.Fatalf("wanted %s success, ok=%v err=%v",tc.expected,ok,err)}
   want:=""
   if tc.expected=="csqtt" || tc.expected=="external" {want=tc.expected+"|"+expectRequest}
   if want!="" {
    select{case got:=<-seen:if got!=want{t.Fatalf("outbound %s selected, wanted %s",got,want)}
    case <-time.After(2*time.Second):t.Fatalf("no SOCKS5 dial from expected %s",tc.expected)}
   }
   select{case other:=<-seen:t.Fatalf("unexpected extra SOCKS5 outbound: %s",other)
   case <-time.After(120*time.Millisecond):}
   if err:=manager.Stop();err!=nil{t.Fatalf("stop sing-box: %v",err)}
   if manager.Status().Running {t.Fatal("sing-box still running after stop")}
  })
 }
}
