//go:build linux

package main

import (
 "bytes"
 "os"
 "path/filepath"
 "strings"
 "testing"
)

func TestCSQTT250LaunchRejectsLegacyFlagsAndUnsafeBinary(t *testing.T) {
 binary, err := os.Executable()
 if err != nil { t.Fatal(err) }
 cases := [][]string{
  {"-device-id","legacy-uuid"}, {"--device-id=legacy-uuid"},
  {"-client-id","legacy"}, {"--client-id=legacy"},
  {"-password","legacy"}, {"--password=legacy"},
 }
 for _, args := range cases {
  if _, err := csqtt250ClientCommand(binary,args,"fake-lab-password",os.Environ()); err == nil {
   t.Errorf("unexpected acceptance of original-2.5 unsupported CLI: %q",args)
  }
 }
 if _,err:=csqtt250ClientCommand("client",nil,"x",nil); err == nil { t.Error("relative binary accepted") }
 if _,err:=csqtt250ClientCommand(binary,nil,"",nil); err == nil { t.Error("missing password accepted") }
 dir:=t.TempDir()
 symlink:=filepath.Join(dir,"client")
 if err:=os.Symlink(binary,symlink);err != nil { t.Fatal(err) }
 if _,err:=csqtt250ClientCommand(symlink,nil,"x",nil);err == nil { t.Error("symlink binary accepted") }
}

func TestCSQTT250ChildProcessFixture(t *testing.T) {
 if os.Getenv("CSQTT_250_HELPER")!="1" { return }
 if os.Getenv("CSQTT_PASSWORD")!="fake-lab-password" { os.Exit(41) }
 for _,arg:=range os.Args { if strings.Contains(arg,"fake-lab-password") { os.Exit(42) } }
 os.Stdout.WriteString("CSQTT_250_ENV_ONLY_OK")
 os.Exit(0)
}

func TestCSQTT250LaunchPassesPasswordOnlyInEnv(t *testing.T) {
 binary,err:=os.Executable()
 if err!=nil { t.Fatal(err) }
 args:=[]string{"-test.run=^TestCSQTT250ChildProcessFixture$"}
 cmd,err:=csqtt250ClientCommand(binary,args,"fake-lab-password",append(os.Environ(),"CSQTT_PASSWORD=wrong-value"))
 if err!=nil { t.Fatal(err) }
 cmd.Env=append(cmd.Env,"CSQTT_250_HELPER=1")
 if !cmd.SysProcAttr.Setpgid { t.Fatal("process group isolation missing") }
 if strings.Contains(strings.Join(cmd.Args," "),"fake-lab-password"){ t.Fatal("password appeared in process argv") }
 var out bytes.Buffer
 cmd.Stdout=&out
 if err:=cmd.Run();err!=nil { t.Fatalf("child process failed: %v",err) }
 if got:=out.String();got!="CSQTT_250_ENV_ONLY_OK" { t.Fatalf("unexpected output: %q",got) }
}
