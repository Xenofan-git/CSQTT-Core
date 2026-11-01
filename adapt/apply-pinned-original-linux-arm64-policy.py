from pathlib import Path

UPSTREAM = Path("upstream-lalune")
PROTO = UPSTREAM / "Desktop/Libs/protocols.go"
LINUX = UPSTREAM / "Desktop/Linux/app_linux.go"

def replace_once(path, old, new):
    s = path.read_text()
    if old not in s:
        raise SystemExit(f"marker not found in {path}: {old!r}")
    path.write_text(s.replace(old, new, 1))

replace_once(
    PROTO,
    '''\t\tdefault:\n\t\t\treturn "client-linux-x86_64"''',
    '''\t\tdefault:\n\t\t\tif goarch == "arm64" {\n\t\t\t\treturn "client-linux-arm64"\n\t\t\t}\n\t\t\treturn "client-linux-x86_64"'''
)

# The pinned Go wrapper invokes DeployManager with --port, but the original
# DeployManager CLI calls this option --ssh-port. Keep the original UI/API
# contract while fixing the actual CLI invocation.
BRIDGE = UPSTREAM / "Desktop/Libs/bridge.go"
# Propagate Linux TUN setup failures through the shared interface so the
# runner can fail closed before starting the packet bridge.
bridge_source = BRIDGE.read_text()
old_tun_interface = '\tSetupRoutes(tunIP string, tunDNS string)'
new_tun_interface = '\tSetupRoutes(tunIP string, tunDNS string) error'
if bridge_source.count(old_tun_interface) != 1:
    raise SystemExit("TunInterface SetupRoutes marker missing or ambiguous")
bridge_source = bridge_source.replace(old_tun_interface, new_tun_interface, 1)
BRIDGE.write_text(bridge_source)


# Make Bridge.StartTunnel fail closed when UDP or Linux TUN startup fails.
bridge_source = BRIDGE.read_text()
old_start_tunnel = """func (b *Bridge) StartTunnel(corePort int) {
\tif b.TunRunning {
\t\treturn
\t}
\tudpConn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", corePort))
\tif err != nil {
\t\tb.Core.AddLog(fmt.Sprintf("[TUN] UDP ошибка: %v", err))
\t\treturn
\t}
\tb.UdpConn = udpConn
\tb.TunRunning = true
\tb.Tun.Start(udpConn, &b.TunRunning)
\tb.Core.AddLog("[TUN] Пакетный мост запущен")
}"""
new_start_tunnel = """type tunStarterWithError interface {
\tStartWithError(udpConn net.Conn, running *bool) error
}

func (b *Bridge) StartTunnel(corePort int) bool {
\tif b.TunRunning {
\t\treturn true
\t}
\tudpConn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", corePort))
\tif err != nil {
\t\tb.Core.AddLog(fmt.Sprintf("[TUN] UDP ошибка: %v", err))
\t\treturn false
\t}
\tb.UdpConn = udpConn
\tb.TunRunning = true
\tif starter, ok := b.Tun.(tunStarterWithError); ok {
\t\terr = starter.StartWithError(udpConn, &b.TunRunning)
\t} else {
\t\tb.Tun.Start(udpConn, &b.TunRunning)
\t}
\tif err != nil {
\t\tb.TunRunning = false
\t\t_ = udpConn.Close()
\t\tb.UdpConn = nil
\t\tb.Tun.Stop()
\t\tb.Core.SetConnected(false)
\t\tb.Core.AddLog(fmt.Sprintf("[TUN] Запуск пакетного моста отменён: %v", err))
\t\treturn false
\t}
\tb.Core.AddLog("[TUN] Пакетный мост запущен")
\treturn true
}"""
if old_start_tunnel not in bridge_source:
    raise SystemExit("Bridge.StartTunnel source marker missing or ambiguous")
bridge_source = bridge_source.replace(old_start_tunnel, new_start_tunnel, 1)
BRIDGE.write_text(bridge_source)

# New CSQTT core Auto VK protocol: stdin bootstrap replaces obsolete --token.
bs = BRIDGE.read_text()
bs = bs.replace('import (\n\t"fmt"', 'import (\n\t"encoding/base64"\n\t"encoding/json"\n\t"fmt"', 1)
old = '\tif isAutoVk {\n\t\ttoken := b.Core.ReadTokenFromFile()\n\t\tif token == "" {\n\t\t\tb.Core.AddLog("[АВТО ВК] ПРЕДУПРЕЖДЕНИЕ: token.json не найден или пуст — ядро упадёт")\n\t\t} else {\n\t\t\tcmd = append(cmd, "--token", token)\n\t\t\tb.Core.AddLog("[АВТО ВК] Токен передан из token.json")\n\t\t}\n\t}\n'
new = '\tif isAutoVk {\n\t\ttoken := b.Core.ReadTokenFromFile()\n\t\tif token == "" {\n\t\t\tb.Core.AddLog("[АВТО ВК] ПРЕДУПРЕЖДЕНИЕ: token.json не найден или пуст — ядро не запустит Auto JS")\n\t\t} else {\n\t\t\tb.Core.AddLog("[АВТО ВК] VK bootstrap подготовлен для stdin")\n\t\t}\n\t}\n'
if old not in bs: raise SystemExit("bridge auto-vk marker not found")
bs = bs.replace(old, new, 1)
anchor = 'func (b *Bridge) ParseTunconf(line string) (string, string) {'
method = 'func (b *Bridge) AutoVkBootstrap() string {\n\tsettings := b.Core.GetSettings()\n\tif settings.AuthMode != "autoVk" { return "" }\n\ttoken := b.Core.ReadTokenFromFile()\n\tif token == "" { return "" }\n\tpayload, err := json.Marshal(map[string]string{"token": token})\n\tif err != nil { return "" }\n\treturn "VK_JS_BOOTSTRAP:" + base64.StdEncoding.EncodeToString(payload) + "\\n"\n}\n\n'
if anchor not in bs: raise SystemExit("bridge anchor not found")
bs = bs.replace(anchor, method + anchor, 1)
BRIDGE.write_text(bs)
DEPLOY_GO = UPSTREAM / "Desktop/Libs/deploy.go"
s = DEPLOY_GO.read_text()
old = '"--port", itoa(req.SSHPort)'
new = '"--ssh-port", itoa(req.SSHPort)'
if old not in s:
    raise SystemExit(f"marker not found in {DEPLOY_GO}: {old!r}")
DEPLOY_GO.write_text(s.replace(old, new, 1))

# Linux runner must feed the Auto JS bootstrap through a dedicated stdin pipe.
# Authenticate sudo separately so its password pipeline can never consume the
# bootstrap or CAPTCHA_RESULT messages.
s = LINUX.read_text()
old = '\tcmd := exec.Command("sh", "-c", r.app.runSudoCommand(cmdLine))\n\tcmd.Stdin = os.Stdin\n\tcmd.Stdout = os.Stdout\n\tcmd.Stderr = os.Stderr\n'
new = '\tvar cmd *exec.Cmd\n\tif os.Geteuid() == 0 {\n\t\tcmd = exec.Command("sh", "-c", cmdLine)\n\t} else {\n\t\tr.app.mu.Lock()\n\t\tpassword := r.app.sudoPass\n\t\tr.app.mu.Unlock()\n\t\tvar auth *exec.Cmd\n\t\tif password != "" {\n\t\t\tauth = exec.Command("sudo", "-S", "-v")\n\t\t\tauth.Stdin = strings.NewReader(password + "\\n")\n\t\t} else {\n\t\t\tauth = exec.Command("sudo", "-v")\n\t\t\tauth.Stdin = os.Stdin\n\t\t}\n\t\tauth.Stdout = os.Stdout\n\t\tauth.Stderr = os.Stderr\n\t\tif err := auth.Run(); err != nil {\n\t\t\tbridge.Core.AddLog(fmt.Sprintf("[АВТО ВК] Не удалось авторизовать sudo: %v", err))\n\t\t\tbridge.Core.SetConnected(false)\n\t\t\treturn\n\t\t}\n\t\tcmd = exec.Command("sudo", "-n", "sh", "-c", cmdLine)\n\t}\n\tstdin, err := cmd.StdinPipe()\n\tif err != nil {\n\t\tbridge.Core.AddLog(fmt.Sprintf("[ERROR] Не удалось подготовить stdin ядра: %v", err))\n\t\tbridge.Core.SetConnected(false)\n\t\treturn\n\t}\n\tcmd.Stdout = os.Stdout\n\tcmd.Stderr = os.Stderr\n'
if old not in s: raise SystemExit("stdin launch marker not found")
s = s.replace(old, new, 1)
old = '\tif err := cmd.Start(); err != nil {\n\t\tbridge.Core.AddLog(fmt.Sprintf("[ERROR] Не удалось запустить: %v", err))\n\t\tbridge.Core.SetConnected(false)\n\t\treturn\n\t}\n'
new = '\tif err := cmd.Start(); err != nil {\n\t\t_ = stdin.Close()\n\t\tbridge.Core.AddLog(fmt.Sprintf("[ERROR] Не удалось запустить: %v", err))\n\t\tbridge.Core.SetConnected(false)\n\t\treturn\n\t}\n\tlinuxRunnerInputs.Store(r, stdin)\n\tif bootstrap := bridge.AutoVkBootstrap(); bootstrap != "" {\n\t\tif _, err := io.WriteString(stdin, bootstrap); err != nil {\n\t\t\tbridge.Core.AddLog(fmt.Sprintf("[АВТО ВК] Ошибка передачи bootstrap: %v", err))\n\t\t} else {\n\t\t\tbridge.Core.AddLog("[АВТО ВК] VK_JS_BOOTSTRAP передан в core")\n\t\t}\n\t}\n'
if old not in s: raise SystemExit("stdin start marker not found")
s = s.replace(old, new, 1)
if '\t"io"\n' not in s:
    s = s.replace('\t"time"\n)', '\t"time"\n\t"io"\n)', 1)
LINUX.write_text(s)

# Keep the same stdin pipe open for later CAPTCHA_RESULT responses. Close it
# deterministically when the core process exits.
s = LINUX.read_text()
s = s.replace("type LinuxRunner struct {", "var linuxRunnerInputs sync.Map\n\ntype LinuxRunner struct {", 1)
wait_old = '\t\tcmd.Wait()'
wait_new = '\t\tcmd.Wait()\n\t\tif v, ok := linuxRunnerInputs.LoadAndDelete(r); ok { _ = v.(io.WriteCloser).Close() }'
if wait_old not in s:
    raise SystemExit("core wait marker not found for stdin cleanup")
s = s.replace(wait_old, wait_new, 1)
method = '''func (r *LinuxRunner) SubmitCaptchaResult(result string) bool {
\tif len(result) == 0 || len(result) > 16384 || strings.ContainsAny(result, "\\r\\n") { return false }
\tv, ok := linuxRunnerInputs.Load(r)
\tif !ok { return false }
\t_, err := io.WriteString(v.(io.WriteCloser), "CAPTCHA_RESULT|"+result+"\\n")
\treturn err == nil
}
'''
s = s.replace("func NewApp() *App {", method + "\nfunc NewApp() *App {", 1)
LINUX.write_text(s)

# NanoPi dataplane adaptation: the pinned original leaves LinuxTun.Start empty.
# Keep the original UDP core protocol, but bridge Linux TUN packets to that UDP
# socket using /dev/net/tun. This is intentionally the only dataplane deviation.
s = LINUX.read_text()
# Upstream field alignment can vary; replace the Go struct independent of whitespace.
import re
struct_pattern = re.compile(r"type LinuxTun struct \{[^}]*\}", re.S)
s, struct_count = struct_pattern.subn("""type LinuxTun struct {
	app          *App
	bypassRoutes []string
	mu           sync.Mutex
	lifecycleMu  sync.Mutex
	tunFile      *os.File
	udpConn      net.Conn
	workers      sync.WaitGroup
	tunOpen      func() (*os.File, error)
	tunSetIFF    func(*os.File, *unix.Ifreq) error
	socksRuleCreated bool
}""", s, count=1)
if struct_count != 1:
    raise SystemExit(f"LinuxTun struct marker missing or ambiguous: replaced {struct_count}")

s=s.replace(
'''import "lalune-desktop/Libs"''',
'''import "lalune-desktop/Libs"

import "golang.org/x/sys/unix"''', 1)

old='''func (t *LinuxTun) Setup() error              { return nil }
func (t *LinuxTun) Start(_ net.Conn, _ *bool) {}
func (t *LinuxTun) Stop()                     {}'''
new='''func (t *LinuxTun) Setup() error { return nil }

func (t *LinuxTun) Start(udpConn net.Conn, running *bool) {
\tif err := t.StartWithError(udpConn, running); err != nil {
\t\tt.app.core.AddLog(fmt.Sprintf("[TUN] Запуск TUN не выполнен: %v", err))
\t}
}

func (t *LinuxTun) StartWithError(udpConn net.Conn, running *bool) error {
\tt.lifecycleMu.Lock()
\tdefer t.lifecycleMu.Unlock()
\t_ = running // Worker shutdown is driven by closing resources, not an unsynchronized bool.
\tt.mu.Lock()
\tif t.tunFile != nil {
\t\tt.mu.Unlock()
\t\treturn nil
\t}
\topenTun, setIFF := t.tunOpen, t.tunSetIFF
\tt.mu.Unlock()
\tif openTun == nil {
\t\topenTun = func() (*os.File, error) { return os.OpenFile("/dev/net/tun", os.O_RDWR, 0) }
\t}
\tf, err := openTun()
\tif err != nil {
\t\tt.app.core.AddLog(fmt.Sprintf("[TUN] Не удалось открыть /dev/net/tun: %v", err))
\t\treturn fmt.Errorf("open /dev/net/tun: %w", err)
\t}
\tifr, err := unix.NewIfreq("csqtt0")
\tif err != nil {
\t\t_ = f.Close()
\t\tt.app.core.AddLog(fmt.Sprintf("[TUN] Не удалось создать ifreq: %v", err))
\t\treturn fmt.Errorf("create TUN ifreq: %w", err)
\t}
\tifr.SetUint16(unix.IFF_TUN | unix.IFF_NO_PI)
\tif setIFF == nil {
\t\tsetIFF = func(file *os.File, request *unix.Ifreq) error {
\t\t\treturn unix.IoctlIfreq(int(file.Fd()), unix.TUNSETIFF, request)
\t\t}
\t}
\tif err := setIFF(f, ifr); err != nil {
\t\t_ = f.Close()
\t\tt.app.core.AddLog(fmt.Sprintf("[TUN] TUNSETIFF csqtt0: %v", err))
\t\treturn fmt.Errorf("TUNSETIFF csqtt0: %w", err)
\t}
\tt.mu.Lock()
\tt.tunFile, t.udpConn = f, udpConn
\tt.workers.Add(2)
\tt.mu.Unlock()
\tt.app.core.AddLog("[TUN] Linux TUN↔UDP bridge запущен")
\tgo func() {
\t\tdefer t.workers.Done()
\t\tbuf := make([]byte, 65535)
\t\tfor {
\t\t\tn, readErr := f.Read(buf)
\t\t\tif readErr != nil { return }
\t\t\tif n > 0 {
\t\t\t\tif _, writeErr := udpConn.Write(buf[:n]); writeErr != nil { return }
\t\t\t}
\t\t}
\t}()
\tgo func() {
\t\tdefer t.workers.Done()
\t\tbuf := make([]byte, 65535)
\t\tfor {
\t\t\tn, readErr := udpConn.Read(buf)
\t\t\tif readErr != nil { return }
\t\t\tif n > 0 {
\t\t\t\tif _, writeErr := f.Write(buf[:n]); writeErr != nil { return }
\t\t\t}
\t\t}
\t}()
\treturn nil
}

func (t *LinuxTun) Stop() {
\tt.lifecycleMu.Lock()
\tdefer t.lifecycleMu.Unlock()
\tt.mu.Lock()
\tf, conn := t.tunFile, t.udpConn
\tt.tunFile, t.udpConn = nil, nil
\tt.mu.Unlock()
\t// Close endpoints first to unblock reads, then join both workers.
\tif f != nil { _ = f.Close() }
\tif conn != nil { _ = conn.Close() }
\tt.workers.Wait()
}'''
if old not in s:
    raise SystemExit("LinuxTun Start/Stop marker not found")
s=s.replace(old,new,1)

# Persist the TUN lifecycle edits before the next block reloads app_linux.go.
LINUX.write_text(s)

# Start the original Bridge TUN↔UDP packet bridge after Linux route setup.
s = LINUX.read_text()
# Prevent concurrent core workers sharing one Bridge/TUN state on NanoPi.
old_runner_guard = """func (r *LinuxRunner) StartCore(cmdArgs []string, listenPort int, bridge *libs.Bridge) {
	r.startCoreWithSudo(cmdArgs, listenPort, bridge)
}"""
new_runner_guard = old_runner_guard
if old_runner_guard not in s:
    raise SystemExit("LinuxRunner.StartCore marker not found")
s = s.replace(old_runner_guard, new_runner_guard, 1)

old_runner = """		if tun, ok := bridge.Tun.(*LinuxTun); ok {
			tun.SetupRoutes(tunIP, tunDNS)
		}"""
new_runner = """        if tun, ok := bridge.Tun.(*LinuxTun); ok {
            if err := tun.SetupRoutes(tunIP, tunDNS); err != nil {
                bridge.Core.AddLog(fmt.Sprintf("[TUN] Подключение отменено: %v", err))
                bridge.Core.SetConnected(false)
                return
            }
            if !bridge.StartTunnel(coreListenPort) {
                bridge.Core.AddLog("[TUN] Подключение отменено: пакетный мост не запущен")
                bridge.CleanupRoutes()
                bridge.Core.SetConnected(false)
                if cmd.Process != nil { _ = cmd.Process.Kill() }
                return
            }
        }"""
if old_runner not in s:
    raise SystemExit("LinuxRunner TUN setup marker not found")
s = s.replace(old_runner, new_runner, 1)

start = s.index("func (t *LinuxTun) SetupRoutes")
end = s.index("\n}\n\nfunc (t *LinuxTun) CleanupRoutes", start) + 2
setup = '''func (t *LinuxTun) SetupRoutes(tunIP, tunDNS string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.app.core.AddLog(fmt.Sprintf("[TUN] Настройка TUN (IP: %s, DNS: %s)...", tunIP, tunDNS))

	ip := net.ParseIP(strings.TrimSpace(tunIP))
	if ip == nil || ip.To4() == nil {
		t.app.core.AddLog("[TUN] ОШИБКА: некорректный IPv4 TUN; настройка отменена")
		return fmt.Errorf("invalid TUN IPv4 address %q", tunIP)
	}
	var dnsServers []string
	for _, raw := range strings.Split(tunDNS, ",") {
		d := strings.TrimSpace(raw)
		if d == "" { continue }
		parsed := net.ParseIP(d)
		if parsed == nil || parsed.To4() == nil {
			t.app.core.AddLog(fmt.Sprintf("[TUN] ОШИБКА: некорректный DNS IPv4 %q; настройка отменена", d))
			return fmt.Errorf("invalid TUN DNS IPv4 %q", d)
		}
		dnsServers = append(dnsServers, parsed.To4().String())
	}
	// Refuse to touch a non-empty table or occupied rule priority.
	if err := t.app.runSudo("test -z \\"$(ip -o route show table 202 2>/dev/null)\\" && test -z \\"$(ip -o rule show pref 22020 2>/dev/null)\\" && test -z \\"$(ip -o rule show pref 22015 2>/dev/null)\\""); err != nil {
		t.app.core.AddLog(fmt.Sprintf("[TUN] ОШИБКА: table 202 или priority 22020/22015 уже заняты; чужие записи сохранены: %v", err))
		return fmt.Errorf("TUN routing table 202 or rule priority 22020/22015 is already occupied: %w", err)
	}
	cmd := fmt.Sprintf("ip tuntap add dev csqtt0 mode tun && ip addr add %s/32 dev csqtt0 && ip link set csqtt0 up && ip link set csqtt0 mtu 1300", ip.To4().String())
	if err := t.app.runSudo(cmd); err != nil {
		t.app.core.AddLog(fmt.Sprintf("[TUN] ОШИБКА создания интерфейса: %v", err))
		return fmt.Errorf("create csqtt0: %w", err)
	}
	createdRoutes := []string{}
	createdRule := false
	rollback := func() {
		if createdRule { _ = t.app.runSudo("ip rule del pref 22020 from 192.168.5.0/24 lookup 202") }
		for i := len(createdRoutes)-1; i >= 0; i-- { _ = t.app.runSudo(createdRoutes[i]) }
		if err := t.app.runSudo("resolvectl revert csqtt0"); err != nil { t.app.core.AddLog(fmt.Sprintf("[TUN] rollback DNS: %v", err)) }
		if err := t.app.runSudo("ip tuntap del dev csqtt0 mode tun"); err != nil { t.app.core.AddLog(fmt.Sprintf("[TUN] rollback interface: %v", err)) }
	}
	for _, pair := range []struct{ add, del string }{
		{"ip route add 192.168.4.0/24 dev eth0 table 202", "ip route del 192.168.4.0/24 dev eth0 table 202"},
		{"ip route add 192.168.5.0/24 dev eth1 table 202", "ip route del 192.168.5.0/24 dev eth1 table 202"},
		{"ip route add default dev csqtt0 table 202", "ip route del default dev csqtt0 table 202"},
	} {
		if err := t.app.runSudo(pair.add); err != nil {
			t.app.core.AddLog(fmt.Sprintf("[TUN] ОШИБКА %q: %v; откат", pair.add, err))
			rollback()
			return fmt.Errorf("add TUN route %q: %w", pair.add, err)
		}
		createdRoutes = append(createdRoutes, pair.del)
	}
	if err := t.app.runSudo("ip rule add pref 22020 from 192.168.5.0/24 lookup 202"); err != nil {
		t.app.core.AddLog(fmt.Sprintf("[TUN] ОШИБКА policy rule: %v; откат", err))
		rollback()
		return fmt.Errorf("add TUN policy rule: %w", err)
	}
	createdRule = true
	if len(dnsServers) > 0 {
		if err := t.app.runSudo("resolvectl dns csqtt0 " + strings.Join(dnsServers, " ")); err != nil {
			t.app.core.AddLog(fmt.Sprintf("[TUN] DNS для csqtt0 недоступен через systemd-resolved; /etc/resolv.conf не изменялся: %v; откат", err))
            rollback()
            return fmt.Errorf("configure csqtt0 DNS: %w", err)
		}
	}
	if err := t.applyDirectRoutingLocked(); err != nil {
		t.app.core.AddLog(fmt.Sprintf("[DIRECT] Ошибка применения: %v; откат TUN", err))
        rollback()
        return fmt.Errorf("apply Direct routing: %w", err)
	}
	t.app.core.AddLog("[TUN] TUN настроен успешно (table 202; чужие маршруты и DNS сохранены)")
    return nil
}'''
s = s[:start] + setup + s[end:]
start = s.index("func (t *LinuxTun) CleanupRoutes")
end = s.index("\n}\n\nfunc (t *LinuxTun) AddBypassRoute", start) + 2
cleanup = '''func (t *LinuxTun) CleanupRoutes() {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Do not remove matching-looking entries unless the complete CSQTT route
	// signature is present. Setup refuses occupied tables/priorities.
	if err := t.app.runSudo("ip rule show pref 22020 | grep -Fq 'from 192.168.5.0/24 lookup 202' && ip route show table 202 | grep -Fq 'default dev csqtt0'"); err != nil {
		t.app.core.AddLog("[TUN] Очистка пропущена: правило/маршрут не подтверждают владение")
		return
	}
	t.cleanupDirectRoutingLocked()
	t.app.core.AddLog("[TUN] Удаление TUN...")
	for _, command := range []string{
		"ip rule del pref 22020 from 192.168.5.0/24 lookup 202",
		"ip route del default dev csqtt0 table 202",
		"ip route del 192.168.5.0/24 dev eth1 table 202",
		"ip route del 192.168.4.0/24 dev eth0 table 202",
	} {
		if err := t.app.runSudo(command); err != nil {
			t.app.core.AddLog(fmt.Sprintf("[TUN] Не удалось удалить запись %q: %v", command, err))
		}
	}
	if err := t.app.runSudo("resolvectl revert csqtt0"); err != nil {
		t.app.core.AddLog(fmt.Sprintf("[TUN] Не удалось сбросить DNS csqtt0: %v", err))
	}
	if err := t.app.runSudo("ip tuntap del dev csqtt0 mode tun"); err != nil {
		t.app.core.AddLog(fmt.Sprintf("[TUN] Не удалось удалить csqtt0: %v", err))
	}
	t.app.core.AddLog("[TUN] TUN удалён; чужие маршруты и /etc/resolv.conf не изменялись")
}'''
s = s[:start] + cleanup + s[end:]
LINUX.write_text(s)
print("Pinned original LaLune NanoPi adaptation applied")

# NanoPi headless panel: use installed DeployManager and bundled CSQTT assets.
s = DEPLOY_GO.read_text()
old = 'if wd, err := os.Getwd(); err == nil {'
new = 'if st, err := os.Stat("/usr/local/lib/lalune/deploy-manager"); err == nil && !st.IsDir() {\n\t\treturn "/usr/local/lib/lalune/deploy-manager"\n\t}\n\tif wd, err := os.Getwd(); err == nil {'
if old not in s: raise SystemExit("deploy manager path marker missing")
s = s.replace(old, new, 1)
old = '\tif req.ManualPorts {'
new = '\tif strings.EqualFold(strings.TrimSpace(req.Protocol), "CSQTT") {\n\t\tif st, err := os.Stat("/usr/local/lib/lalune/server-assets"); err == nil && st.IsDir() {\n\t\t\targs = append(args, "--local-binary-dir", "/usr/local/lib/lalune/server-assets")\n\t\t}\n\t}\n\tif req.ManualPorts {'
if old not in s: raise SystemExit("manual ports marker missing")
DEPLOY_GO.write_text(s.replace(old, new, 1))

# NanoPi build version: keep the original InfoPage wording but report this adapted build.
INFO = UPSTREAM / "Frontend/Core/lib/pages/info_page.dart"
replace_once(INFO, "static const String _laluneVersion = '0.5.0';", "static const String _laluneVersion = '0.6.0';")

# Restore the original Deploy tab and connect the Flutter Web UI to NanoPi HTTP API.
MAIN = UPSTREAM / "Frontend/Core/lib/main.dart"
NAV = UPSTREAM / "Frontend/Core/lib/widgets/navbar.dart"
INDEX = UPSTREAM / "Frontend/Core/web/index.html"
API_JS = UPSTREAM / "Frontend/Core/web/api.js"
NANOPI_JS = Path(__file__).with_name("nanopi.js")
replace_once(MAIN, "import 'pages/logs_page.dart';", "import 'pages/logs_page.dart';\nimport 'pages/deploy_page.dart';")
replace_once(MAIN, """      case 3:\n        return const LogsPage();\n      // case 4 (Деплой) убран из UI — вкладка отключена, страница сохранена.\n      default:""", """      case 3:\n        return const LogsPage();\n      case 4:\n        return const DeployPage();\n      default:""")
replace_once(NAV, """    _NavItem(icon: 'assets/logs.png', label: 'Логи'),\n    // Вкладка \"Деплой\" временно убрана из UI (логика в deploy_page.dart сохранена).""", """    _NavItem(icon: 'assets/logs.png', label: 'Логи'),\n    _NavItem(icon: 'assets/info.png', label: 'Деплой'),""")
replace_once(INDEX, "    {{flutter_bootstrap_js}}", "    <script src=\"api.js\"></script>\n    {{flutter_bootstrap_js}}")
API_JS.write_text(NANOPI_JS.read_text())

# ============================================================
# Direct routing: destination domains/IPs bypass CSQTT.
# Only traffic sourced from the LaLune client subnet is affected.
# ============================================================

COMMON = UPSTREAM / "Desktop/Libs/common.go"
LINUX = UPSTREAM / "Desktop/Linux/app_linux.go"
API_DART = UPSTREAM / "Frontend/Core/lib/api.dart"
SETTINGS_PAGE = UPSTREAM / "Frontend/Core/lib/pages/settings_page.dart"

replace_once(
    COMMON,
    '''\tValidateVkHashes        bool   `json:"validateVkHashes"`\n\n\t// Экспериментальные функции.''',
    '''\tValidateVkHashes        bool   `json:"validateVkHashes"`\n\tDirectDomains           string `json:"directDomains"`\n\tDirectIPs               string `json:"directIPs"`\n\n\t// Экспериментальные функции.''',
)

linux_direct = r'''
const directMark = "0x4c4c"
const directRulePref = "22010"

// Paths are variables so tests can isolate Direct's filesystem effects in a tempdir.
var directNftPath = "/run/lalune-direct.nft"
var directDnsmasqPath = "/etc/dnsmasq.d/lalune-direct.conf"
var directDnsmasqMain = "/etc/dnsmasq.conf"
const directDnsmasqInclude = "conf-file=/etc/dnsmasq.d/lalune-direct.conf"

func writeDirectFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".lalune-tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil { return err }
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func ensureDirectDnsmasqInclude() error {
	data, err := os.ReadFile(directDnsmasqMain)
	if err != nil { return fmt.Errorf("чтение %s: %w", directDnsmasqMain, err) }
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == directDnsmasqInclude { return nil }
	}
	content := strings.TrimRight(string(data), "\n") + "\n" + directDnsmasqInclude + "\n"
	if err := writeDirectFileAtomic(directDnsmasqMain, []byte(content), 0644); err != nil {
		return fmt.Errorf("обновление %s: %w", directDnsmasqMain, err)
	}
	return nil
}

func removeDirectDnsmasqInclude() (bool, error) {
	data, err := os.ReadFile(directDnsmasqMain)
	if err != nil { return false, fmt.Errorf("чтение %s: %w", directDnsmasqMain, err) }
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines))
	changed := false
	for _, line := range lines {
		if strings.TrimSpace(line) == directDnsmasqInclude { changed = true; continue }
		out = append(out, line)
	}
	if !changed { return false, nil }
	content := strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
	if err := writeDirectFileAtomic(directDnsmasqMain, []byte(content), 0644); err != nil {
		return false, fmt.Errorf("обновление %s: %w", directDnsmasqMain, err)
	}
	return true, nil
}

func splitDirectList(raw string) []string {
\treturn strings.FieldsFunc(raw, func(r rune) bool {
\t\treturn r == ',' || r == ';' || r == '\\n' || r == '\\r' || r == '\\t' || r == ' '
\t})
}

func validDirectDomain(s string) bool {
\tif s == "" {
\t\treturn false
\t}
\t// Parse IP literals before rejecting domain delimiters: IPv6 uses colons.
\t// CIDR and URL forms remain rejected by the delimiter filter below.
\tif net.ParseIP(s) != nil {
\t\treturn true
\t}
\tif strings.ContainsAny(s, "/#:=<>\\"'\\\\") {
\t\treturn false
\t}
\treturn regexp.MustCompile("(?i)^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?[.])+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$").MatchString(s)
}
func prepareDirectPlan(domainsRaw string) ([]string, bool, error) {
\tdomains := splitDirectList(domainsRaw)
\tfor _, domain := range domains {
\t\tif !validDirectDomain(domain) {
\t\t\treturn nil, false, fmt.Errorf("некорректный домен Direct: %s", domain)
\t\t}
\t}
\tneedsDNS := false
\tfor _, entry := range domains {
\t\tif net.ParseIP(entry) == nil { needsDNS = true; break }
\t}
\treturn domains, needsDNS, nil
}

func (t *LinuxTun) cleanupDirectRoutingLocked() {
\t// Remove only rules carrying our exact mark/table signature; never delete
\t// an unrelated rule merely because it shares the preference number.
\tt.app.runSudo("ip rule del pref " + directRulePref + " fwmark " + directMark + "/0xffff lookup main 2>/dev/null || true")
\tt.app.runSudo("ip -6 rule del pref " + directRulePref + " fwmark " + directMark + "/0xffff lookup main 2>/dev/null || true")
\tt.app.runSudo("nft delete table inet lalune_direct 2>/dev/null || true")
\tincludeRemoved, includeErr := removeDirectDnsmasqInclude()
\tconfigRemoved := false
\tif _, err := os.Stat(directDnsmasqPath); err == nil {
\t\t_ = os.Remove(directDnsmasqPath)
\t\tconfigRemoved = true
\t}
\tif includeErr != nil {
\t\tt.app.core.AddLog(fmt.Sprintf("[DIRECT] Не удалось убрать dnsmasq include: %v", includeErr))
\t} else if includeRemoved || configRemoved {
\t\tif err := t.app.runSudo("systemctl restart dnsmasq"); err != nil {
\t\t\tt.app.core.AddLog(fmt.Sprintf("[DIRECT] Не удалось перезапустить dnsmasq после очистки: %v", err))
\t\t}
\t}
\t_ = os.Remove(directNftPath)
}

func (t *LinuxTun) applyDirectRoutingLocked() (retErr error) {
\t// Once live state starts changing, any subsequent error must leave Direct
\t// fully disabled rather than partially applied. TUN/CSQTT itself is untouched.
\tmutating := false
\tdefer func() {
\t\tif retErr != nil && mutating {
\t\t\tt.cleanupDirectRoutingLocked()
\t\t\tt.app.core.AddLog(fmt.Sprintf("[DIRECT] Частичное применение отменено; Direct очищен: %v", retErr))
\t\t}
\t}()
\tsettings := t.app.core.GetSettings()
\tdomains, _, err := prepareDirectPlan(settings.DirectDomains + "," + settings.DirectIPs)
\tif err != nil { return err }

\tmutating = true
\t// Reconcile only CSQTT-owned Direct rules; never delete by priority alone.
\tt.app.runSudo("ip rule del pref " + directRulePref + " fwmark " + directMark + "/0xffff lookup main 2>/dev/null || true")
\tt.app.runSudo("ip -6 rule del pref " + directRulePref + " fwmark " + directMark + "/0xffff lookup main 2>/dev/null || true")
\tt.app.runSudo("nft delete table inet lalune_direct 2>/dev/null || true")
\tincludeRemoved, includeErr := removeDirectDnsmasqInclude()
\tif includeErr != nil { return includeErr }
\thadDnsmasq := false
\tif _, err := os.Stat(directDnsmasqPath); err == nil {
\t\thadDnsmasq = true
\t\tif err := os.Remove(directDnsmasqPath); err != nil {
\t\t\treturn fmt.Errorf("удаление Direct dnsmasq-конфига: %w", err)
\t\t}
\t}
\tdnsmasqChanged := includeRemoved || hadDnsmasq
\t_ = os.Remove(directNftPath)

\tif len(domains) == 0 {
\t\tif dnsmasqChanged {
\t\t\tif err := t.app.runSudo("systemctl restart dnsmasq"); err != nil { return fmt.Errorf("перезапуск dnsmasq после очистки Direct: %w", err) }
\t\t}
\t\treturn nil
\t}

\tvar domainsOnly, ipv4s, ipv6s []string
\tfor _, entry := range domains {
\t\tif ip := net.ParseIP(entry); ip != nil {
\t\t\tif v4 := ip.To4(); v4 != nil { ipv4s = append(ipv4s, v4.String()) } else { ipv6s = append(ipv6s, ip.String()) }
\t\t} else { domainsOnly = append(domainsOnly, entry) }
\t}
\tvar nft strings.Builder
\tnft.WriteString("table inet lalune_direct {\\n")
\tnft.WriteString("  set direct4 { type ipv4_addr; flags interval;")
\tif len(ipv4s) > 0 { nft.WriteString(" elements = { " + strings.Join(ipv4s, ", ") + " };") }
\tnft.WriteString(" }\\n")
\tnft.WriteString("  set direct6 { type ipv6_addr; flags interval;")
\tif len(ipv6s) > 0 { nft.WriteString(" elements = { " + strings.Join(ipv6s, ", ") + " };") }
\tnft.WriteString(" }\\n")
\tnft.WriteString("  chain prerouting { type filter hook prerouting priority -150; policy accept;\\n")
\tnft.WriteString("    ip saddr 192.168.5.0/24 ip daddr @direct4 meta mark set " + directMark + "\\n")
\tnft.WriteString("    iifname \\"eth1\\" ip6 daddr @direct6 meta mark set " + directMark + "\\n")
\tnft.WriteString("  }\\n")
\tnft.WriteString("}\\n")

\tif err := writeDirectFileAtomic(directNftPath, []byte(nft.String()), 0600); err != nil {
\t\treturn fmt.Errorf("атомарная запись nft-конфига: %w", err)
\t}
\tif err := t.app.runSudo("nft -c -f " + directNftPath); err != nil {
\t\treturn fmt.Errorf("проверка nft direct policy: %w", err)
\t}
\tif err := t.app.runSudo("nft -f " + directNftPath); err != nil {
\t\treturn fmt.Errorf("nft direct policy: %w", err)
\t}

\tif len(domainsOnly) > 0 {
\t\t// Keep A and AAAA nftset mappings in one directive. dnsmasq processes
\t\t// both address families for the same domain using this combined form.
\t\tvar dnsmasq strings.Builder
\t\tdnsmasq.WriteString("nftset=/")
\t\tdnsmasq.WriteString(strings.Join(domainsOnly, "/"))
\t\tdnsmasq.WriteString("/4#inet#lalune_direct#direct4,6#inet#lalune_direct#direct6\\n")
\t\tif err := writeDirectFileAtomic(directDnsmasqPath, []byte(dnsmasq.String()), 0644); err != nil {
\t\t\treturn fmt.Errorf("атомарная запись dnsmasq direct-конфига: %w", err)
\t\t}
\t\tif err := ensureDirectDnsmasqInclude(); err != nil {
\t\t\treturn fmt.Errorf("подключение Direct dnsmasq-конфига: %w", err)
\t\t}
\t\tdnsmasqChanged = true
\t}
\tif dnsmasqChanged {
\t\tif err := t.app.runSudo("systemctl restart dnsmasq"); err != nil {
\t\t\treturn fmt.Errorf("перезапуск dnsmasq после изменения Direct: %w", err)
\t\t}
\t}

\tif err := t.app.runSudo("ip rule add pref " + directRulePref + " fwmark " + directMark + "/0xffff lookup main"); err != nil {
\t\treturn fmt.Errorf("IPv4 ip rule Direct: %w", err)
\t}
\tif err := t.app.runSudo("ip -6 rule add pref " + directRulePref + " fwmark " + directMark + "/0xffff lookup main"); err != nil {
\t\treturn fmt.Errorf("IPv6 ip rule Direct: %w", err)
\t}

\tt.app.core.AddLog(fmt.Sprintf("[DIRECT] Активно: доменов=%d; mark=%s; LTE=main", len(domains), directMark))
\treturn nil
}

func (t *LinuxTun) ApplyDirectRouting() error {
\tt.mu.Lock()
\tdefer t.mu.Unlock()
\treturn t.applyDirectRoutingLocked()
}

func (t *LinuxTun) CleanupDirectRouting() {
\tt.mu.Lock()
\tdefer t.mu.Unlock()
\tt.cleanupDirectRoutingLocked()
}

'''
# This Go fragment originated in a JavaScript patch and contains escaped tabs and
# doubled Go escapes. Normalize it before inserting it into the Go source file.
linux_direct = linux_direct.replace("\\\\", "\\")
_normalized_lines = []
for _line in linux_direct.splitlines():
    _indent = ""
    while _line.startswith("\\t"):
        _indent += "\t"
        _line = _line[2:]
    _normalized_lines.append(_indent + _line)
linux_direct = "\n".join(_normalized_lines) + "\n"

# Integrate Direct-domain-only bypass into the Linux adapter and settings UI.
s = LINUX.read_text()
marker = 'func (t *LinuxTun) SetupRoutes(tunIP, tunDNS string) error {'
if s.count(marker) != 1:
    raise SystemExit('SetupRoutes marker missing or ambiguous')
s = s.replace(marker, linux_direct + marker, 1)

# SetupRoutes and CleanupRoutes above are complete ownership-safe implementations.
# Legacy text replacements removed: their old markers intentionally no longer exist.
LINUX.write_text(s)

# Add direct routing preferences to the persisted settings model.
s = API_DART.read_text()
replacements = [
    ('''  final bool validateVkHashes;\n\n  /// Экспериментальные функции.''', '''  final bool validateVkHashes;\n  final String directDomains;\n  final String directIPs;\n\n  /// Экспериментальные функции.'''),
    ('''    this.validateVkHashes = false,\n    this.enableSmartTunnel = false,''', '''    this.validateVkHashes = false,\n    this.directDomains = '',\n    this.directIPs = '',\n    this.enableSmartTunnel = false,'''),
    ('''      validateVkHashes: (j['validateVkHashes'] ?? false) as bool,\n      enableSmartTunnel:''', '''      validateVkHashes: (j['validateVkHashes'] ?? false) as bool,\n      directDomains: (j['directDomains'] ?? '') as String,\n      directIPs: (j['directIPs'] ?? '') as String,\n      enableSmartTunnel:'''),
    ('''    'validateVkHashes': validateVkHashes,\n    'enableSmartTunnel': enableSmartTunnel,''', '''    'validateVkHashes': validateVkHashes,\n    'directDomains': directDomains,\n    'directIPs': directIPs,\n    'enableSmartTunnel': enableSmartTunnel,'''),
    ('''    String? vkAuthMode, bool? allowHashRedistribution, bool? validateVkHashes,\n    bool? enableSmartTunnel,''', '''    String? vkAuthMode, bool? allowHashRedistribution, bool? validateVkHashes,\n    String? directDomains, String? directIPs,\n    bool? enableSmartTunnel,'''),
    ('''    validateVkHashes: validateVkHashes ?? this.validateVkHashes,\n    enableSmartTunnel:''', '''    validateVkHashes: validateVkHashes ?? this.validateVkHashes,\n    directDomains: directDomains ?? this.directDomains,\n    directIPs: directIPs ?? this.directIPs,\n    enableSmartTunnel:'''),
]
for old, new in replacements:
    if s.count(old) != 1:
        raise SystemExit(f'API settings marker missing or ambiguous: {old[:80]!r}')
    s = s.replace(old, new, 1)
API_DART.write_text(s)

# Add the Direct-domain-only list to Settings.
s = SETTINGS_PAGE.read_text()
replacements = [
    ('''  final _deviceIdCtl = TextEditingController();''', '''  final _deviceIdCtl = TextEditingController();\n  final _directDomainsCtl = TextEditingController();'''),
    ('''    _deviceIdCtl.dispose();''', '''    _deviceIdCtl.dispose();\n    _directDomainsCtl.dispose();'''),
    ('''    _deviceIdCtl.text = s.deviceId;\n    _authMode''', '''    _deviceIdCtl.text = s.deviceId;\n    _directDomainsCtl.text = s.directDomains;\n    _authMode'''),
    ('''      authMode: _authMode,\n      enableSmartTunnel: _enableSmartTunnel,''', '''      authMode: _authMode,\n      directDomains: _directDomainsCtl.text.trim(),\n      enableSmartTunnel: _enableSmartTunnel,'''),
]
for old, new in replacements:
    if s.count(old) != 1:
        raise SystemExit(f'Settings marker missing or ambiguous: {old[:80]!r}')
    s = s.replace(old, new, 1)
ui_marker = '''              const SizedBox(height: 18),\n\n              _sectionTitle('Device ID'),'''
ui_insert = '''              const SizedBox(height: 18),\n\n              _sectionTitle('Direct — обход CSQTT'),\n              GlassCard(\n                child: Column(\n                  crossAxisAlignment: CrossAxisAlignment.start,\n                  children: [\n                    Text('Только указанные назначения идут напрямую через LTE. Всё остальное остаётся через CSQTT.',\n                      style: TextStyle(fontSize: 11.5, height: 1.45, color: Colors.white.withOpacity(0.55))),\n                    const SizedBox(height: 12),\n                    Text('Домены', style: TextStyle(fontSize: 12, color: Colors.white.withOpacity(0.7))),\n                    const SizedBox(height: 5),\n                    TextField(\n                      controller: _directDomainsCtl,\n                      minLines: 3,\n                      maxLines: 8,\n                      style: const TextStyle(fontSize: 13),\n                      decoration: const InputDecoration(hintText: 'example.com\\ncdn.example.com'),\n                      onChanged: (_) => _markDirty(),\n                    ),\n                    const SizedBox(height: 12),\n                  ],\n                ),\n              ),\n              const SizedBox(height: 18),\n\n              _sectionTitle('Device ID'),'''
if s.count(ui_marker) != 1:
    raise SystemExit('Settings UI insertion marker missing or ambiguous')
SETTINGS_PAGE.write_text(s.replace(ui_marker, ui_insert, 1))

# Make the no-selection state explicit. Without this, deleting the active last
# profile leaves a stale pointer in AppCore and the UI keeps trying to connect it.
COMMON = UPSTREAM / "Desktop/Libs/common.go"
replace_once(
    COMMON,
    '''func (a *AppCore) SetSelectedConfigJson(jsonStr string) bool {
	var c Config
	if err := json.Unmarshal([]byte(jsonStr), &c); err != nil {
		return false
	}
	a.SetSelectedConfig(&c)
	return true
}''',
    '''func (a *AppCore) SetSelectedConfigJson(jsonStr string) bool {
	trimmed := strings.TrimSpace(jsonStr)
	if trimmed == "null" || trimmed == "{}" || trimmed == "" {
		a.SetSelectedConfig(nil)
		return true
	}
	var c Config
	if err := json.Unmarshal([]byte(jsonStr), &c); err != nil {
		return false
	}
	if c.ID <= 0 {
		return false
	}
	a.SetSelectedConfig(&c)
	return true
}'''
)

# The upstream token-fetcher release is Linux x64 only. Bundle a native
# linux-arm64 build and let Playwright use Debian's system Chromium on NanoPi.
VTOKEN = UPSTREAM / "Desktop/Libs/vktoken.go"
replace_once(
    VTOKEN,
    'VKTokenFetcherArchive = "LaLuneTokenFetcher_%s.zip"',
    'VKTokenFetcherArchive = "LaLuneTokenFetcher_%s.zip"\n\tVKTokenFetcherARM64Archive = "LaLuneTokenFetcher_Linux_ARM64.zip"',
)
replace_once(
    VTOKEN,
    '// EnsureVKTokenFetcher — скачивает и устанавливает LaLuneTokenFetcher.',
    '''func (a *AppCore) bundledVKTokenFetcherArchive() string {
    candidates := []string{}
    if exe, err := os.Executable(); err == nil {
        candidates = append(candidates, filepath.Join(filepath.Dir(exe), "server-assets", VKTokenFetcherARM64Archive))
    }
    candidates = append(candidates, filepath.Join("/usr/local/lib/lalune/server-assets", VKTokenFetcherARM64Archive))
    for _, candidate := range candidates {
        if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
            return candidate
        }
    }
    return ""
}

// EnsureVKTokenFetcher — скачивает и устанавливает LaLuneTokenFetcher.'''
)
replace_once(
    VTOKEN,
    'if !DownloadFile(url, zipPath) {',
    '''if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
        bundled := a.bundledVKTokenFetcherArchive()
        if bundled == "" {
            return false, fmt.Errorf("для NanoPi ARM64 не найден bundled LaLuneTokenFetcher_Linux_ARM64.zip")
        }
        data, err := os.ReadFile(bundled)
        if err != nil {
            return false, fmt.Errorf("не удалось прочитать ARM64 token fetcher: %w", err)
        }
        if err := os.WriteFile(zipPath, data, 0600); err != nil {
            return false, fmt.Errorf("не удалось подготовить ARM64 token fetcher: %w", err)
        }
        archiveName = VKTokenFetcherARM64Archive
        a.AddLog(fmt.Sprintf("[VK] Использую локальный ARM64 token fetcher: %s", bundled))
    } else if !DownloadFile(url, zipPath) {'''
)


replace_once(
    VTOKEN,
    '"strings"',
    '"strings"\n\t"net/url"'
)
replace_once(
    VTOKEN,
    'func (a *AppCore) HasVKToken() bool {',
    """func (a *AppCore) SaveVKTokenInput(value string) error {
    value = strings.TrimSpace(value)
    token := value
    if strings.Contains(value, "://") {
        parsed, err := url.Parse(value)
        if err != nil { return fmt.Errorf("некорректная ссылка OAuth: %w", err) }
        if parsed.Scheme != "https" || parsed.User != nil {
            return fmt.Errorf("OAuth-ссылка должна использовать HTTPS")
        }
        host := strings.ToLower(parsed.Hostname())
        if host != "oauth.vk.ru" && host != "oauth.vk.com" {
            return fmt.Errorf("OAuth-ссылка должна принадлежать oauth.vk.ru или oauth.vk.com")
        }
        if parsed.Port() != "" && parsed.Port() != "443" {
            return fmt.Errorf("недопустимый порт OAuth-ссылки")
        }
        if parsed.EscapedPath() != "/blank.html" {
            return fmt.Errorf("ожидался OAuth redirect /blank.html")
        }
        token = parsed.Query().Get("access_token")
        if token == "" && parsed.Fragment != "" {
            fragment, err := url.ParseQuery(parsed.Fragment)
            if err != nil { return fmt.Errorf("не удалось разобрать фрагмент OAuth: %w", err) }
            token = fragment.Get("access_token")
        }
    } else if strings.Contains(value, "=") || strings.HasPrefix(value, "#") {
        fragment := strings.TrimPrefix(value, "#")
        parsed, err := url.ParseQuery(fragment)
        if err != nil { return fmt.Errorf("не удалось разобрать параметры OAuth: %w", err) }
        token = parsed.Get("access_token")
    }
    token = strings.TrimSpace(token)
    if len(token) < 20 || strings.ContainsAny(token, " "+string([]byte{9, 10, 13})) {
        return fmt.Errorf("не найден корректный access_token; silent_token не является access_token")
    }
    if err := os.MkdirAll(a.appDir, 0700); err != nil {
        return fmt.Errorf("не удалось создать каталог токена: %w", err)
    }
    data, err := json.MarshalIndent(VKTokenJSON{Token: token, SavedAt: time.Now()}, "", "  ")
    if err != nil { return fmt.Errorf("не удалось сериализовать VK токен: %w", err) }
    if err := os.WriteFile(a.vkTokenFile(), data, 0600); err != nil {
        return fmt.Errorf("не удалось сохранить VK токен: %w", err)
    }
    if err := os.Chmod(a.vkTokenFile(), 0600); err != nil {
        return fmt.Errorf("не удалось ограничить права VK токена: %w", err)
    }
    return nil
}

func (a *AppCore) HasVKToken() bool {"""
)

FETCHER = UPSTREAM / "Core/LaLuneTokenFetcher/Playwright/PlaywrightTokenFetcher.cs"
replace_once(
    FETCHER,
    '                    Headless = false,',
    '                    Headless = OperatingSystem.IsLinux(),\n                    ExecutablePath = ResolveChromiumExecutable(),'
)
replace_once(
    FETCHER,
    '    private static bool IsBrowserMissing(PlaywrightException ex)',
    '''    private static string? ResolveChromiumExecutable()
    {
        if (!OperatingSystem.IsLinux() ||
            System.Runtime.InteropServices.RuntimeInformation.ProcessArchitecture != System.Runtime.InteropServices.Architecture.Arm64)
        {
            return null;
        }

        var configured = Environment.GetEnvironmentVariable("PLAYWRIGHT_CHROMIUM_EXECUTABLE");
        if (!string.IsNullOrWhiteSpace(configured) && File.Exists(configured))
        {
            return configured;
        }

        foreach (var candidate in new[] { "/usr/bin/chromium", "/usr/bin/chromium-browser" })
        {
            if (File.Exists(candidate))
            {
                return candidate;
            }
        }

        throw new ChromiumMissingException(
            "Для VK-авторизации на NanoPi ARM64 нужен системный Chromium. Установите пакет chromium.");
    }

    private static bool IsBrowserMissing(PlaywrightException ex)'''
)

print('Direct domain/IP bypass and explicit selection clearing adaptations applied')

# Guard the final generated Linux source, after every later adaptation has run.
# Earlier checks only proved the patch script executed; they did not prove the
# exact file copied into the Go module retained the lifecycle implementation.
final_linux = LINUX.read_text()
for required in (
    "tunOpen      func() (*os.File, error)",
    "tunSetIFF    func(*os.File, *unix.Ifreq) error",
    "func (t *LinuxTun) StartWithError(udpConn net.Conn, running *bool) error",
    "func (t *LinuxTun) Stop()",
):
    if required not in final_linux:
        raise SystemExit(f"final generated app_linux.go missing TUN lifecycle marker: {required}")
print("Verified final app_linux.go contains TUN lifecycle fields and methods")

# Extract the exact production argv quoting path for an executable regression test.
core_argv_old = """\tcorePath := cmdArgs[0]
\tquotedArgs := make([]string, len(cmdArgs)-1)
\tfor i, arg := range cmdArgs[1:] {
\t\tescaped := strings.ReplaceAll(arg, "'", "'\\\\''")
\t\tquotedArgs[i] = "'" + escaped + "'"
\t}

\tcmdLine := fmt.Sprintf("'%s' %s > '%s' 2>&1",
\t\tstrings.ReplaceAll(corePath, "'", "'\\\\''"),
\t\tstrings.Join(quotedArgs, " "),
\t\tlogFile,
\t)
"""
core_argv_new = """\tcmdLine := buildCoreCommandLine(cmdArgs, logFile)
"""
if final_linux.count(core_argv_old) != 1:
    raise SystemExit("production core argv construction marker missing or ambiguous")
final_linux = final_linux.replace(core_argv_old, core_argv_new, 1)
core_argv_helper = """func appendCSQTTAuthArgs(cmdArgs []string, password, deviceID string) []string {\n\treturn append(cmdArgs, "-device-id", deviceID, "-password", password)\n}\n\nfunc buildCoreCommandLine(cmdArgs []string, logFile string) string {
\tif len(cmdArgs) == 0 { return "" }
\tquote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\\\''") + "'" }
\tquotedArgs := make([]string, 0, len(cmdArgs)-1)
\tfor _, arg := range cmdArgs[1:] { quotedArgs = append(quotedArgs, quote(arg)) }
\treturn fmt.Sprintf("%s %s > %s 2>&1", quote(cmdArgs[0]), strings.Join(quotedArgs, " "), quote(logFile))
}

"""
core_argv_anchor = "func (r *LinuxRunner) startCoreWithSudo(cmdArgs []string, listenPort int, bridge *libs.Bridge) {"
if final_linux.count(core_argv_anchor) != 1:
    raise SystemExit("startCoreWithSudo marker missing or ambiguous")
final_linux = final_linux.replace(core_argv_anchor, core_argv_helper + core_argv_anchor, 1)

# CSQTT 2.1.9 tunnel authentication: bind the saved tunnel password to the
# stable LaLune device ID and pass both to the pinned native client.
linux_auth_marker = """func (r *LinuxRunner) StartCore(cmdArgs []string, listenPort int, bridge *libs.Bridge) {
	r.startCoreWithSudo(cmdArgs, listenPort, bridge)
}"""
linux_auth_replacement = """func (r *LinuxRunner) StartCore(cmdArgs []string, listenPort int, bridge *libs.Bridge) {
	password, err := loadTunnelAuthPassword()
	if err != nil {
		bridge.Core.AddLog("[AUTH] Пароль туннеля не задан; запуск отменён")
		bridge.Core.SetConnected(false)
		return
	}
	deviceID := csqttDeviceID(bridge.Core)
	if deviceID == "" {
		bridge.Core.AddLog("[AUTH] ID устройства LaLune не задан; запуск отменён")
		bridge.Core.SetConnected(false)
		return
	}
	cmdArgs = appendCSQTTAuthArgs(cmdArgs, password, deviceID)
	r.startCoreWithSudo(cmdArgs, listenPort, bridge)
}"""
if final_linux.count(linux_auth_marker) != 1:
    raise SystemExit("LinuxRunner.StartCore auth insertion marker missing or ambiguous")
final_linux = final_linux.replace(linux_auth_marker, linux_auth_replacement, 1)
LINUX.write_text(final_linux)

