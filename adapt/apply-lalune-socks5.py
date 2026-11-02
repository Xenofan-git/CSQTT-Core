from pathlib import Path

ROOT = Path("upstream-lalune")
src = ROOT / "Desktop/Linux/app_linux.go"
s = src.read_text()

# Add SOCKS5 server ownership to App.
old = "\trunner    *LinuxRunner\n"
new = "\trunner    *LinuxRunner\n\tsocks      *Socks5Server\n"
if old not in s:
    raise SystemExit("missing App runner field")
s = s.replace(old, new, 1)

# Start SOCKS5 only after the TUN fd is attached and the TUN<->UDP bridge is live.
start_method = '''func (t *LinuxTun) StartSocks() error {
\tt.lifecycleMu.Lock()
\tdefer t.lifecycleMu.Unlock()
\tt.mu.Lock()
\tdefer t.mu.Unlock()
\tif t.app.socks == nil { return nil }
\tif t.tunFile == nil || t.udpConn == nil {
\t\treturn fmt.Errorf("cannot start SOCKS5 before the TUN packet bridge is active")
\t}
\tif !t.socksRuleCreated {
\t\tif err := t.app.runSudo("ip rule add pref 22015 fwmark 0x4d53/0xffff lookup 202"); err != nil {
\t\t\treturn fmt.Errorf("add SOCKS5 fwmark rule: %w", err)
\t\t}
\t\tt.socksRuleCreated = true
\t}
\tif err := t.app.socks.Start(); err != nil {
\t\tif cleanupErr := t.app.runSudo("ip rule del pref 22015 fwmark 0x4d53/0xffff lookup 202"); cleanupErr != nil {
\t\t\tt.app.core.AddLog(fmt.Sprintf("[SOCKS5] Failed to rollback fwmark rule: %v", cleanupErr))
\t\t} else {
\t\t\tt.socksRuleCreated = false
\t\t}
\t\treturn fmt.Errorf("start SOCKS5 listener: %w", err)
\t}
\tt.app.core.AddLog("[SOCKS5] Address monitor started; waiting for eth1 private IPv4 or rebinding dynamically")
\treturn nil
}

'''
cleanup_start = '''func (t *LinuxTun) CleanupRoutes() {
\tt.mu.Lock()
\tdefer t.mu.Unlock()
'''
cleanup_replacement = '''func (t *LinuxTun) CleanupRoutes() {
\t// Serialize listener shutdown with StartSocks and the TUN fd lifecycle.
\tt.lifecycleMu.Lock()
\tdefer t.lifecycleMu.Unlock()
\tt.mu.Lock()
\tdefer t.mu.Unlock()
\tif t.app.socks != nil { t.app.socks.Stop() }
\tif t.socksRuleCreated {
\t\tif err := t.app.runSudo("ip rule del pref 22015 fwmark 0x4d53/0xffff lookup 202"); err != nil {
\t\t\tt.app.core.AddLog(fmt.Sprintf("[SOCKS5] Не удалось удалить fwmark rule: %v", err))
\t\t} else {
\t\t\tt.socksRuleCreated = false
\t\t}
\t}
'''
if s.count(cleanup_start) != 1:
    raise SystemExit("CleanupRoutes lock marker missing or ambiguous")
s = s.replace(cleanup_start, cleanup_replacement, 1)

cleanup_marker = "func (t *LinuxTun) CleanupRoutes() {"
if s.count(cleanup_marker) != 1:
    raise SystemExit("CleanupRoutes function marker missing or ambiguous")
if "func (t *LinuxTun) StartSocks() error" in s:
    raise SystemExit("StartSocks already exists in generated source")
s = s.replace(cleanup_marker, start_method + cleanup_marker, 1)

# Keep the listener and mark rule out of SetupRoutes. SetupRoutes owns only TUN
# and Direct setup; the runner starts SOCKS after Bridge.StartTunnel succeeds.

# The runner starts the packet bridge first, then opens the Keenetic-facing listener.
old_runner = '''            if !bridge.StartTunnel(coreListenPort) {
                bridge.Core.AddLog("[TUN] Подключение отменено: пакетный мост не запущен")
                bridge.CleanupRoutes()
                bridge.Core.SetConnected(false)
                if cmd.Process != nil { _ = cmd.Process.Kill() }
                return
            }'''
new_runner = '''            if err := startTunnelAndSocks(bridge, coreListenPort); err != nil {
                bridge.Core.AddLog(fmt.Sprintf("[TUN] Подключение отменено: %v", err))
                bridge.Core.SetConnected(false)
                if cmd.Process != nil { _ = terminateCoreProcessGroup(cmd.Process.Pid) }
                return
            }'''
if s.count(old_runner) != 1:
    raise SystemExit("LinuxRunner packet bridge marker missing or ambiguous")
s = s.replace(old_runner, new_runner, 1)

# Centralize startup ordering and rollback so it can be tested independently.
helper = '''func startTunnelAndSocks(bridge *libs.Bridge, coreListenPort int) error {
\tif !bridge.StartTunnel(coreListenPort) {
\t\tbridge.CleanupRoutes()
\t\treturn fmt.Errorf("start TUN packet bridge failed")
\t}
\tif tun, ok := bridge.Tun.(*LinuxTun); ok {
\t\tif err := tun.StartSocks(); err != nil {
\t\t\tbridge.StopTunnel()
\t\t\tbridge.CleanupRoutes()
\t\t\treturn fmt.Errorf("start SOCKS5 after TUN packet bridge: %w", err)
\t\t}
\t}
\treturn nil
}

'''
new_app_marker = "func NewApp() *App {"
if s.count(new_app_marker) != 1:
    raise SystemExit("NewApp marker missing or ambiguous")
s = s.replace(new_app_marker, helper + new_app_marker, 1)

# Initialize the dynamic eth1 listener in the App owner.
old = '\tapp := &App{core: core, tun: tun}\n'
new = """\tapp := &App{core: core, tun: tun}\n\t// Resolve eth1 dynamically when TUN starts and rebind after DHCP/address changes.\n\tapp.socks = NewSocks5Server("eth1:12500")\n"""
if old not in s:
    raise SystemExit("missing NewApp marker")
s = s.replace(old, new, 1)

src.write_text(s)

helper = ROOT / "Desktop/Linux/lalune_socks5_linux.go"
helper.write_text(Path("adapt/lalune_socks5_linux.go").read_text())
print("LaLune SOCKS5 adaptation applied: TUN bridge now starts before SOCKS5 listener")
