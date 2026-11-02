from pathlib import Path

root = Path("upstream")

app = root / "Desktop/Linux/app_linux.go"
s = app.read_text()
s = s.replace('\n\t"net"\n', '\n', 1)

old = """type LinuxTun struct {
\tapp          *App
\tbypassRoutes []string
\tmu           sync.Mutex
}

func (t *LinuxTun) Setup() error              { return nil }
func (t *LinuxTun) Start(_ net.Conn, _ *bool) {}
func (t *LinuxTun) Stop()                     {}
"""
new = """type LinuxTun struct {
\tapp          *App
\tbypassRoutes []string
\ttunFile      *os.File
\tmu           sync.Mutex
}
"""
assert old in s
s = s.replace(old, new, 1)

old = 'cmd := fmt.Sprintf("ip tuntap add dev csqtt0 mode tun && ip addr add %s/32 dev csqtt0 && ip link set csqtt0 up && ip link set csqtt0 mtu 1300", tunIP)'
new = 'cmd := fmt.Sprintf("ip addr add %s/32 dev csqtt0 && ip link set csqtt0 up && ip link set csqtt0 mtu 1300", tunIP)'
assert old in s
s = s.replace(old, new, 1)

# LaLune must remain an independent connection: never install a default route in main.
old = '''\tt.app.runSudo("ip route add default dev csqtt0")
'''
assert old in s
s = s.replace(old, "", 1)

old = '''\tt.app.runSudo("ip route del default dev csqtt0 2>/dev/null || true")
\tt.app.runSudo("ip tuntap del dev csqtt0 mode tun 2>/dev/null || true")
\tt.app.core.AddLog("[TUN] TUN удалён")
'''
new = '''\tt.app.runSudo("ip route del default dev csqtt0 2>/dev/null || true")
\tt.app.runSudo("ip link del csqtt0 2>/dev/null || true")
\tif t.tunFile != nil {
\t\t_ = t.tunFile.Close()
\t\tt.tunFile = nil
\t}
\tt.app.core.AddLog("[TUN] TUN удалён")
'''
assert old in s
s = s.replace(old, new, 1)

old = '''\tbridge.Core.AddLog("[INFO] Запуск ядра через sudo...")

\tcmd := exec.Command("sh", "-c", r.app.runSudoCommand(cmdLine))
'''
new = '''\tif err := bridge.SetupTun(); err != nil {
\t\tbridge.Core.AddLog(fmt.Sprintf("[ERROR] Не удалось подготовить TUN: %v", err))
\t\tbridge.Core.SetConnected(false)
\t\treturn
\t}
\ttunUDS := tunUDSPath()
\tcmdArgs = append(cmdArgs, "--tun-uds", tunUDS)
\tquotedArgs = make([]string, len(cmdArgs)-1)
\tfor i, arg := range cmdArgs[1:] {
\t\tescaped := strings.ReplaceAll(arg, "'", "'\\\\''")
\t\tquotedArgs[i] = "'" + escaped + "'"
\t}
\tcmdLine = fmt.Sprintf("'%s' %s > '%s' 2>&1",
\t\tstrings.ReplaceAll(corePath, "'", "'\\\\''"),
\t\tstrings.Join(quotedArgs, " "),
\t\tlogFile,
\t)

\tbridge.Core.AddLog("[INFO] Запуск ядра через sudo...")

\tcmd := exec.Command("sh", "-c", r.app.runSudoCommand(cmdLine))
'''
assert old in s
s = s.replace(old, new, 1)

old = '''\tbridge.Core.AddLog(fmt.Sprintf("[INFO] Ядро запущено (PID: %d)", cmd.Process.Pid))

\ttunconfChan := make(chan [2]string, 1)
'''
new = '''\tbridge.Core.AddLog(fmt.Sprintf("[INFO] Ядро запущено (PID: %d)", cmd.Process.Pid))
\tif tun, ok := bridge.Tun.(*LinuxTun); ok {
\t\tgo func() {
\t\t\tif err := tun.AttachToUDS(tunUDS); err != nil {
\t\t\t\tbridge.Core.AddLog(fmt.Sprintf("[ERROR] Не удалось передать TUN FD в core: %v", err))
\t\t\t} else {
\t\t\t\tbridge.Core.AddLog("[TUN] TUN FD передан в core через UDS/SCM_RIGHTS")
\t\t\t}
\t\t}()
\t}

\ttunconfChan := make(chan [2]string, 1)
'''
assert old in s
s = s.replace(old, new, 1)

app.write_text(s)

# Stage the helper into the upstream Linux package.
helper = root / "Desktop/Linux/tun_uds.go"
helper.write_text(Path("patches/linux-tun-uds.go").read_text())

print("Linux LaLune TUN UDS patch applied")
