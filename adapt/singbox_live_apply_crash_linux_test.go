//go:build linux

package main

import (
 "bytes"
 "context"
 "encoding/json"
 "encoding/binary"
 "errors"
 "fmt"
 "io"
 "net"
 "os"
 "os/exec"
 "path/filepath"
 "strconv"
 "strings"
 "testing"
 "time"
)

// Test worker MUST run in its own process. The parent SIGKILLs that disposable
// Go controller, never a production daemon or system service.
func TestSingBoxLiveApplyCrashWorker(t *testing.T) {
 if os.Getenv("CSQTT_CRASH_APPLY_WORKER")!="1" {t.Skip("only disposable subprocess")}
 dir:=os.Getenv("CSQTT_CRASH_APPLY_DIR")
 stage:=os.Getenv("CSQTT_CRASH_APPLY_STAGE")
 bin:=os.Getenv("CSQTT_SING_BOX_BINARY")
 if dir==""||bin==""||stage==""{t.Fatal("missing worker inputs")}
 m:=NewSingBoxManager(bin,filepath.Join(dir,"config.json"),filepath.Join(dir,"singbox.log"))
 m.checkTimeout=3*time.Second
 old:=singBoxConfigInput{ListenAddress:reserveSingBoxTestAddress(t)}
 if err:=m.SaveConfig(old);err!=nil{t.Fatal(err)}
 if err:=m.Start(context.Background());err!=nil{t.Fatal(err)}
 if err:=m.waitSOCKSReady(old.ListenAddress);err!=nil{t.Fatal(err)}
 next:=old
 next.Rules=[]singBoxRouteRule{{ID:singBoxDirectRuleID,Enabled:true,Domains:[]string{"live-crash.example"},Outbound:singBoxDirectTag}}
 checkpoint:=func(name string){
  if name!=stage{return}
  signal:=filepath.Join(dir,"checkpoint")
  f,err:=os.OpenFile(signal,os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600)
  if err!=nil{t.Fatal(err)}
  if _,err=f.WriteString(name);err!=nil{t.Fatal(err)}
  if err=f.Sync();err!=nil{t.Fatal(err)}
  if err=f.Close();err!=nil{t.Fatal(err)}
  select{} // stopped only by SIGKILL from the parent test
 }
 m.applyCheckpointHook=checkpoint
 if stage=="half-published" {
  m.renameOverride=func(from,to string) error {
   err:=os.Rename(from,to)
   if err==nil && to==m.configPath {
    if _,e:=os.Stat(m.liveApplyJournalPath());e==nil{checkpoint("half-published")}
   }
   return err
  }
 }
 if _,err:=m.ApplyConfig(next);err!=nil{t.Fatal(err)}
 t.Fatal("worker exited before SIGKILL checkpoint")
}

func testRecoveredSingBoxSOCKSEcho(t *testing.T,address string,echoAddr *net.TCPAddr) {
 t.Helper()
 conn,err:=net.DialTimeout("tcp",address,3*time.Second)
 if err!=nil{t.Fatal(err)}
 defer conn.Close()
 _=conn.SetDeadline(time.Now().Add(3*time.Second))
 if _,err=conn.Write([]byte{5,1,0});err!=nil{t.Fatal(err)}
 var hello [2]byte
 if _,err=io.ReadFull(conn,hello[:]);err!=nil{t.Fatal(err)}
 if hello!=[2]byte{5,0}{t.Fatalf("SOCKS greeting %+v",hello)}
 req:=[]byte{5,1,0,1,127,0,0,1,0,0}
 binary.BigEndian.PutUint16(req[8:],uint16(echoAddr.Port))
 if _,err=conn.Write(req);err!=nil{t.Fatal(err)}
 var reply [4]byte
 if _,err=io.ReadFull(conn,reply[:]);err!=nil{t.Fatal(err)}
 if reply[1]!=0{t.Fatalf("recovered SOCKS CONNECT failed %v",reply)}
 n:=0
 switch reply[3]{case 1:n=6;case 4:n=18;case 3:
  var b [1]byte
  if _,err=io.ReadFull(conn,b[:]);err!=nil{t.Fatal(err)}
  n=int(b[0])+2
 default:t.Fatalf("unrecognized SOCKS reply type %d",reply[3])}
 if _,err=io.CopyN(io.Discard,conn,int64(n));err!=nil{t.Fatal(err)}
 ping:=[]byte("old-route-restored-real-TCP")
 if _,err=conn.Write(ping);err!=nil{t.Fatal(err)}
 recv:=make([]byte,len(ping))
 if _,err=io.ReadFull(conn,recv);err!=nil{t.Fatal(err)}
 if !bytes.Equal(recv,ping){t.Fatalf("restored listener echoed %q",recv)}
}

func TestSingBoxLiveApplyCrashRecoveryNativeARM64(t *testing.T) {
 binaryPath:=strings.TrimSpace(os.Getenv("CSQTT_SING_BOX_BINARY"))
 if binaryPath==""{t.Skip("requires pinned native ARM64 sing-box executable")}
 echo,err:=net.Listen("tcp","127.0.0.1:0")
 if err!=nil{t.Fatal(err)}
 defer echo.Close()
 go func(){for{conn,e:=echo.Accept();if e!=nil{return};go func(c net.Conn){defer c.Close();_,_=io.Copy(c,c)}(conn)}}()
 echoAddr:=echo.Addr().(*net.TCPAddr)
 for _,stage:=range []string{"prepared","half-published","candidate-published","candidate-started","candidate-healthy"} {
  t.Run(stage,func(t *testing.T){
   dir:=t.TempDir()
   command:=exec.Command(os.Args[0],"-test.run=^TestSingBoxLiveApplyCrashWorker$","-test.timeout=65s")
   command.Env=append(os.Environ(),"CSQTT_CRASH_APPLY_WORKER=1","CSQTT_CRASH_APPLY_DIR="+dir,"CSQTT_CRASH_APPLY_STAGE="+stage)
   var childOut bytes.Buffer
   command.Stdout=&childOut
   command.Stderr=&childOut
   if err:=command.Start();err!=nil{t.Fatal(err)}
   defer func(){_ = command.Process.Kill();_,_=command.Process.Wait()}()
   waitUntil:=time.Now().Add(16*time.Second)
   var actualCheckpoint string
   for time.Now().Before(waitUntil){
    data,err:=os.ReadFile(filepath.Join(dir,"checkpoint"))
    if err==nil{actualCheckpoint=string(data);break}
    if command.ProcessState!=nil{break}
    time.Sleep(20*time.Millisecond)
   }
   if actualCheckpoint!=stage{
    t.Fatalf("worker did not reach %s, marker=%q, output=%s",stage,actualCheckpoint,childOut.String())
   }
   oldJournal:=filepath.Join(dir,"config.json.live-apply.json")
   info,err:=os.Lstat(oldJournal)
   if err!=nil{t.Fatalf("durable journal missing at %s: %v",stage,err)}
   if !info.Mode().IsRegular()||info.Mode().Perm()!=0600{t.Fatalf("checkpoint has unsafe mode %s",info.Mode())}
   old,err:=os.ReadFile(filepath.Join(dir,"config.json"))
   if err!=nil{t.Fatal(err)}
   // Reap and verify forced death, including the child sing-box Pdeathsig.
   if err:=command.Process.Kill();err!=nil{t.Fatal(err)}
   waitErr:=command.Wait()
   if waitErr==nil{t.Fatal("worker unexpectedly exited cleanly")}
   newManager:=NewSingBoxManager(binaryPath,filepath.Join(dir,"config.json"),filepath.Join(dir,"restarted.log"))
   journal,err:=newManager.readLiveApplyJournal();if err!=nil||journal==nil{t.Fatalf("missing verifiable recovery journal: %v",err)}
   if stage=="prepared" && singBoxHash(old)!=journal.OldConfigSHA256 {t.Fatal("pre-stop stage modified config")}
   if stage=="candidate-published" && singBoxHash(old)!=journal.NewConfigSHA256 {t.Fatal("new pair was not published")}
   if stage=="half-published" {
    if _,err:=os.Stat(newManager.transactionPath());err!=nil{t.Fatal("inner transactional journal absent before recovery")}
   }
   if stage=="candidate-published" {
    // Deliberate third-party mutation must fail closed and keep checkpoint.
    mutated:=[]byte("{\"external-change\":true}")
    if err:=os.WriteFile(newManager.configPath,mutated,0600);err!=nil{t.Fatal(err)}
    if _,err:=newManager.LoadConfig();err==nil||!strings.Contains(err.Error(),"externally modified"){
     t.Fatalf("unrecognized concurrent config mutation was not rejected: %v",err)
    }
    if _,err:=os.Stat(oldJournal);err!=nil{t.Fatal("checkpoint discarded after suspicious mutation")}
    if err:=os.WriteFile(newManager.configPath,old,0600);err!=nil{t.Fatal(err)}
   }
   // Kernel may need a moment to deliver Pdeathsig to orphaned sing-box.
   // Never try to reuse a busy port until the old child has stopped.
   listen,err:=func()(string,error){
    var config struct{Inbounds []struct{Listen string `json:"listen"`;Port int `json:"listen_port"`} `json:"inbounds"`}
    // Read previous input from trusted journal rather than untrusted config.
    if len(journal.OldConfig)==0{return "",errors.New("missing previous config")}
    if err:=jsonUnmarshalOldConfig(journal.OldConfig,&config);err!=nil{return "",err}
    if len(config.Inbounds)!=1{return "",errors.New("wrong inbound count")}
    return net.JoinHostPort(config.Inbounds[0].Listen,strconv.Itoa(config.Inbounds[0].Port)),nil
   }()
   if err!=nil{t.Fatal(err)}
   released:=false
   for until:=time.Now().Add(4*time.Second);time.Now().Before(until);{
    c,e:=net.DialTimeout("tcp",listen,80*time.Millisecond)
    if e!=nil{released=true;break}
    _=c.Close()
    time.Sleep(25*time.Millisecond)
   }
   if !released{t.Fatal("orphan sing-box did not stop after controller SIGKILL")}
   // Fresh controller recovers pending old files on initial LoadConfig.
   recovered,err:=newManager.LoadConfig()
   if err!=nil{t.Fatalf("startup recovery failed at %s: %v",stage,err)}
   if recovered.ListenAddress!=listen||len(recovered.Rules)!=0{t.Fatalf("wrong restored routes: %+v",recovered)}
   cfg,err:=os.ReadFile(newManager.configPath);if err!=nil{t.Fatal(err)}
   panel,err:=os.ReadFile(newManager.panelStatePath());if err!=nil{t.Fatal(err)}
   if singBoxHash(cfg)!=journal.OldConfigSHA256||singBoxHash(panel)!=journal.OldPanelSHA256{
    t.Fatal("crash recovery did not restore config and panel bit-for-bit")
   }
   if _,err:=os.Lstat(oldJournal);!errors.Is(err,os.ErrNotExist){t.Fatalf("completed recovery retained marker: %v",err)}
   if _,err:=os.Lstat(newManager.transactionPath());!errors.Is(err,os.ErrNotExist){t.Fatalf("nested transaction remains: %v",err)}
   if err:=newManager.Start(context.Background());err!=nil{t.Fatalf("restored sing-box start: %v",err)}
   defer newManager.Stop()
   if err:=newManager.waitSOCKSReady(listen);err!=nil{t.Fatal(err)}
   testRecoveredSingBoxSOCKSEcho(t,listen,echoAddr)
   if err:=newManager.Stop();err!=nil{t.Fatal(err)}
   // Restart and readback again: recovery must be idempotent.
   checked,err:=newManager.LoadConfig()
   if err!=nil||len(checked.Rules)!=0{t.Fatalf("second readback failed: %v %+v",err,checked)}
  })
 }
 fmt.Println("CSQTT_CORE_NATIVE_ARM64_SIGKILL_LIVE_APPLY_RECOVERY_PASS")
}

// Keep the worker's JSON payload decoding explicit and bounded.
func jsonUnmarshalOldConfig(data []byte,out any) error {
 if len(data)>singBoxTxnMaxFile {return fmt.Errorf("oversized journal old config")}
 return json.Unmarshal(data,out)
}
