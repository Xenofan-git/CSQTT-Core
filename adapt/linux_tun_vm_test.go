//go:build linux

package main

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	libs "lalune-desktop/Libs"
)

// These tests execute the generated LinuxTun.SetupRoutes implementation with
// command shims. They run inside the disposable ARM64 system VM and never touch
// host routes, interfaces, resolver state, or Direct policy.
func tunTestApp() *App {
	core := libs.NewAppCore()
	tun := &LinuxTun{}
	app := &App{core: core, tun: tun}
	tun.app = app
	return app
}

func installTunCommandShims(t *testing.T, failContains string) string {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "commands.log")
	sudo := "#!/bin/sh\nprintf 'SUDO %s\\n' \"$*\" >> \"" + logPath + "\"\n" +
		"if [ -n \"${FAKE_SUDO_FAIL_CONTAINS:-}\" ]; then case \"$*\" in *\"$FAKE_SUDO_FAIL_CONTAINS\"*) exit 17;; esac; fi\nif [ \"$1\" = test ]; then shift; /usr/bin/test \"$@\"; exit $?; fi\nexit 0\n"
	ip := "#!/bin/sh\nprintf 'IP %s\\n' \"$*\" >> \"" + logPath + "\"\n" +
		"case \"$*\" in\n" +
		"  '-o route show table 202') [ -n \"${FAKE_IP_OCCUPIED_ROUTE:-}\" ] && echo \"$FAKE_IP_OCCUPIED_ROUTE\";;\n" +
		"  '-o rule show pref 22020') [ -n \"${FAKE_IP_OCCUPIED_RULE:-}\" ] && echo \"$FAKE_IP_OCCUPIED_RULE\";;\n" +
		"  '-o rule show pref 22015') [ -n \"${FAKE_IP_OCCUPIED_SOCKS_RULE:-}\" ] && echo \"$FAKE_IP_OCCUPIED_SOCKS_RULE\";;\n" +
		"esac\nexit 0\n"
	for name, content := range map[string]string{"sudo": sudo, "ip": ip} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_SUDO_LOG", logPath)
	t.Setenv("FAKE_SUDO_FAIL_CONTAINS", failContains)
	return logPath
}

func readTunCommandLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestLinuxTunRejectsInvalidConfigBeforeMutation(t *testing.T) {
	logPath := installTunCommandShims(t, "")
	app := tunTestApp()
	if err := app.tun.SetupRoutes("not-an-ip", "8.8.8.8"); err == nil {
		t.Fatal("invalid TUN address unexpectedly accepted")
	}
	if got := readTunCommandLog(t, logPath); strings.Contains(got, "SUDO ip tuntap add") {
		t.Fatalf("invalid config mutated system state: %s", got)
	}
	if err := app.tun.SetupRoutes("192.0.2.10", "8.8.8.8,not-a-dns"); err == nil {
		t.Fatal("invalid DNS unexpectedly accepted")
	}
	if got := readTunCommandLog(t, logPath); strings.Contains(got, "SUDO ip tuntap add") {
		t.Fatalf("invalid DNS mutated system state: %s", got)
	}
}

func isolateDirectFilesystem(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	oldNft, oldDnsmasq, oldMain := directNftPath, directDnsmasqPath, directDnsmasqMain
	directNftPath = filepath.Join(dir, "lalune-direct.nft")
	directDnsmasqPath = filepath.Join(dir, "dnsmasq.d", "lalune-direct.conf")
	directDnsmasqMain = filepath.Join(dir, "dnsmasq.conf")
	if err := os.MkdirAll(filepath.Dir(directDnsmasqPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(directDnsmasqMain, []byte("# isolated test config\\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		directNftPath, directDnsmasqPath, directDnsmasqMain = oldNft, oldDnsmasq, oldMain
	})
}

func TestLinuxTunSuccessfulSetupBuildsIsolatedPolicy(t *testing.T) {
	logPath := installTunCommandShims(t, "")
	isolateDirectFilesystem(t)
	app := tunTestApp()
	if err := app.tun.SetupRoutes("192.0.2.10", "8.8.8.8,1.1.1.1"); err != nil {
		t.Fatalf("valid TUN setup failed: %v", err)
	}
	log := readTunCommandLog(t, logPath)
	for _, required := range []string{
		"SUDO ip tuntap add dev csqtt0 mode tun",
		"SUDO ip route add 192.168.4.0/24 dev eth0 table 202",
		"SUDO ip route add 192.168.5.0/24 dev eth1 table 202",
		"SUDO ip route add default dev csqtt0 table 202",
		"SUDO ip rule add pref 22020 from 192.168.5.0/24 lookup 202",
		"SUDO resolvectl dns csqtt0 8.8.8.8 1.1.1.1",
	} {
		if !strings.Contains(log, required) {
			t.Fatalf("successful setup trace missing %q:\\n%s", required, log)
		}
	}
	if strings.Contains(log, "SUDO ip route add default dev csqtt0\n") {
		t.Fatalf("setup installed a global default route instead of table 202:\\n%s", log)
	}
	// Applying an empty Direct plan deliberately reconciles that separate policy;
	// the assertions above are limited to the CSQTT route table and DNS scope.
}

func TestLinuxTunCleanupStopsSocksEvenWhenTUNOwnershipIsMissing(t *testing.T) {
	logPath := installTunCommandShims(t, "")
	app := tunTestApp()
	app.socks = NewSocks5Server("127.0.0.1:0")
	if err := app.socks.Start(); err != nil { t.Fatalf("start SOCKS listener: %v", err) }
	app.tun.socksRuleCreated = true
	app.tun.CleanupRoutes()
	app.socks.mu.Lock()
	stopped := app.socks.stopped
	app.socks.mu.Unlock()
	if !stopped { t.Fatal("SOCKS5 listener remained active after CleanupRoutes") }
	log := readTunCommandLog(t, logPath)
	if !strings.Contains(log, "SUDO ip rule del pref 22015 fwmark 0x4d53/0xffff lookup 202") {
		t.Fatalf("owned SOCKS5 fwmark rule was not removed before TUN ownership check:\\n%s", log)
	}
	if strings.Contains(log, "SUDO ip tuntap del dev csqtt0 mode tun") {
		t.Fatalf("cleanup removed TUN despite failed ownership check:\\n%s", log)
	}
}

func TestLinuxTunCleanupRefusesUnownedState(t *testing.T) {
	logPath := installTunCommandShims(t, "")
	app := tunTestApp()
	// The shims report no CSQTT-owned rule/default route. Cleanup must be a no-op.
	app.tun.CleanupRoutes()
	log := readTunCommandLog(t, logPath)
	if strings.Contains(log, "SUDO ip rule del pref 22020") ||
		strings.Contains(log, "SUDO ip route del default dev csqtt0 table 202") ||
		strings.Contains(log, "SUDO ip tuntap del dev csqtt0 mode tun") {
		t.Fatalf("cleanup touched unowned TUN state:\\n%s", log)
	}
	if !strings.Contains(log, "SUDO ip rule show pref 22020") {
		t.Fatalf("cleanup did not verify ownership before mutation:\\n%s", log)
	}
	assertNoDirectCleanup(t, log)
}

func TestLinuxTunRefusesForeignTableOrRule(t *testing.T) {
	t.Run("foreign route table", func(t *testing.T) {
		logPath := installTunCommandShims(t, "")
		t.Setenv("FAKE_IP_OCCUPIED_ROUTE", "blackhole 198.18.77.0/24")
		app := tunTestApp()
		if err := app.tun.SetupRoutes("192.0.2.10", "8.8.8.8"); err == nil {
			t.Fatal("occupied route table unexpectedly accepted")
		}
		if got := readTunCommandLog(t, logPath); strings.Contains(got, "SUDO ip tuntap add") {
			t.Fatalf("foreign table caused mutation: %s", got)
		}
	})
	t.Run("foreign rule priority", func(t *testing.T) {
		logPath := installTunCommandShims(t, "")
		t.Setenv("FAKE_IP_OCCUPIED_RULE", "22020: from all lookup 42424")
		app := tunTestApp()
		if err := app.tun.SetupRoutes("192.0.2.10", "8.8.8.8"); err == nil {
			t.Fatal("occupied rule priority unexpectedly accepted")
		}
		if got := readTunCommandLog(t, logPath); strings.Contains(got, "SUDO ip tuntap add") {
			t.Fatalf("foreign rule caused mutation: %s", got)
		}
	})
	t.Run("foreign SOCKS rule priority", func(t *testing.T) {
		logPath := installTunCommandShims(t, "")
		t.Setenv("FAKE_IP_OCCUPIED_SOCKS_RULE", "22015: from all lookup 42424")
		app := tunTestApp()
		if err := app.tun.SetupRoutes("192.0.2.10", "8.8.8.8"); err == nil {
			t.Fatal("occupied SOCKS rule priority unexpectedly accepted")
		}
		if got := readTunCommandLog(t, logPath); strings.Contains(got, "SUDO ip tuntap add") {
			t.Fatalf("foreign SOCKS rule caused mutation: %s", got)
		}
	})
}

func assertNoDirectCleanup(t *testing.T, log string) {
	t.Helper()
	for _, forbidden := range []string{"22010", "lalune_direct", "lalune-direct.nft", "dnsmasq"} {
		if strings.Contains(log, forbidden) {
			t.Fatalf("TUN rollback touched independent Direct policy (%q):\n%s", forbidden, log)
		}
	}
}

func TestLinuxTunRouteFailureRollsBackOnlyOwnedResources(t *testing.T) {
	logPath := installTunCommandShims(t, "ip route add 192.168.5.0/24 dev eth1 table 202")
	app := tunTestApp()
	if err := app.tun.SetupRoutes("192.0.2.10", "8.8.8.8"); err == nil {
		t.Fatal("injected route failure unexpectedly succeeded")
	}
	log := readTunCommandLog(t, logPath)
	for _, required := range []string{
		"SUDO ip tuntap add dev csqtt0 mode tun",
		"SUDO ip route add 192.168.4.0/24 dev eth0 table 202",
		"SUDO ip route del 192.168.4.0/24 dev eth0 table 202",
		"SUDO resolvectl revert csqtt0",
		"SUDO ip tuntap del dev csqtt0 mode tun",
	} {
		if !strings.Contains(log, required) {
			t.Fatalf("rollback trace missing %q:\n%s", required, log)
		}
	}
	if strings.Contains(log, "SUDO ip rule add pref 22020") {
		t.Fatalf("policy rule added after route failure:\n%s", log)
	}
	assertNoDirectCleanup(t, log)
}

func TestLinuxTunDNSFailureRollsBackAndPreservesDirect(t *testing.T) {
	logPath := installTunCommandShims(t, "resolvectl dns csqtt0")
	app := tunTestApp()
	if err := app.tun.SetupRoutes("192.0.2.10", "8.8.8.8,1.1.1.1"); err == nil {
		t.Fatal("injected DNS failure unexpectedly succeeded")
	}
	log := readTunCommandLog(t, logPath)
	for _, required := range []string{
		"SUDO ip route add default dev csqtt0 table 202",
		"SUDO ip rule add pref 22020 from 192.168.5.0/24 lookup 202",
		"SUDO resolvectl dns csqtt0 8.8.8.8 1.1.1.1",
		"SUDO ip rule del pref 22020 from 192.168.5.0/24 lookup 202",
		"SUDO ip route del default dev csqtt0 table 202",
		"SUDO ip route del 192.168.5.0/24 dev eth1 table 202",
		"SUDO ip route del 192.168.4.0/24 dev eth0 table 202",
		"SUDO resolvectl revert csqtt0",
		"SUDO ip tuntap del dev csqtt0 mode tun",
	} {
		if !strings.Contains(log, required) {
			t.Fatalf("DNS rollback trace missing %q:\n%s", required, log)
		}
	}
	assertNoDirectCleanup(t, log)
}


type failingStartTun struct {
	startCalled bool
	stopCalled  bool
}

func (t *failingStartTun) Setup() error { return nil }
func (t *failingStartTun) Start(net.Conn, *bool) { t.startCalled = true }
func (t *failingStartTun) StartWithError(net.Conn, *bool) error {
	return errors.New("injected TUNSETIFF failure")
}
func (t *failingStartTun) Stop() { t.stopCalled = true }
func (t *failingStartTun) SetupRoutes(string, string) error { return nil }
func (t *failingStartTun) CleanupRoutes() {}

func TestBridgeStartTunnelFailsClosedOnTUNStartError(t *testing.T) {
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil { t.Fatalf("listen UDP: %v", err) }
	defer listener.Close()

	tun := &failingStartTun{}
	core := libs.NewAppCore()
	core.SetConnected(true)
	bridge := libs.NewBridge(core, tun, nil)
	if bridge.StartTunnel(listener.LocalAddr().(*net.UDPAddr).Port) {
		t.Fatal("StartTunnel unexpectedly reported success after injected TUN startup failure")
	}
	if bridge.TunRunning { t.Fatal("TunRunning remained true after failed startup") }
	if bridge.UdpConn != nil { t.Fatal("UDP connection was retained after failed startup") }
	if core.IsConnected() { t.Fatal("core remained connected after failed TUN startup") }
	if !tun.stopCalled { t.Fatal("TUN Stop was not called during rollback") }
	if tun.startCalled { t.Fatal("legacy non-error Start method used despite error-aware starter") }
}

func TestBridgeStartTunnelRejectsInvalidUDPPort(t *testing.T) {
	tun := &failingStartTun{}
	core := libs.NewAppCore()
	core.SetConnected(true)
	bridge := libs.NewBridge(core, tun, nil)
	if bridge.StartTunnel(70000) {
		t.Fatal("StartTunnel unexpectedly accepted invalid UDP port")
	}
	if bridge.TunRunning { t.Fatal("TunRunning set after UDP dial failure") }
	if bridge.UdpConn != nil { t.Fatal("UDP connection retained after UDP dial failure") }
	if tun.startCalled || tun.stopCalled { t.Fatal("TUN was touched after UDP dial failed") }
}

type successfulStartTun struct {
	starts int
	stops  int
}

func (t *successfulStartTun) Setup() error { return nil }
func (t *successfulStartTun) Start(net.Conn, *bool) {}
func (t *successfulStartTun) StartWithError(net.Conn, *bool) error {
	t.starts++
	return nil
}
func (t *successfulStartTun) Stop() { t.stops++ }
func (t *successfulStartTun) SetupRoutes(string, string) error { return nil }
func (t *successfulStartTun) CleanupRoutes() {}

func TestBridgeStartTunnelReconnectLifecycle(t *testing.T) {
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen UDP: %v", err)
	}
	defer listener.Close()
	port := listener.LocalAddr().(*net.UDPAddr).Port

	tun := &successfulStartTun{}
	core := libs.NewAppCore()
	bridge := libs.NewBridge(core, tun, nil)

	if !bridge.StartTunnel(port) {
		t.Fatal("first StartTunnel unexpectedly failed")
	}
	firstConn := bridge.UdpConn
	if firstConn == nil || !bridge.TunRunning {
		t.Fatal("first start did not retain active UDP connection/running state")
	}
	if !bridge.StartTunnel(port) {
		t.Fatal("repeated StartTunnel while running should be idempotent")
	}
	if tun.starts != 1 {
		t.Fatalf("repeated start initialized TUN %d times; want 1", tun.starts)
	}
	if bridge.UdpConn != firstConn {
		t.Fatal("repeated start replaced the active UDP connection")
	}

	bridge.StopTunnel()
	if bridge.TunRunning || bridge.UdpConn != nil || bridge.TunSetupDone {
		t.Fatal("StopTunnel did not clear running/connection/setup state")
	}
	if tun.stops != 1 {
		t.Fatalf("StopTunnel called TUN Stop %d times; want 1", tun.stops)
	}
	if _, err := firstConn.Write([]byte("after-close")); err == nil {
		t.Fatal("UDP connection remained writable after StopTunnel")
	}

	if !bridge.StartTunnel(port) {
		t.Fatal("StartTunnel failed after disconnect")
	}
	secondConn := bridge.UdpConn
	if secondConn == nil || secondConn == firstConn {
		t.Fatal("reconnect did not create a fresh UDP connection")
	}
	if tun.starts != 2 {
		t.Fatalf("reconnect initialized TUN %d times total; want 2", tun.starts)
	}
	bridge.StopTunnel()
	if bridge.TunRunning || bridge.UdpConn != nil || tun.stops != 2 {
		t.Fatal("second disconnect did not release tunnel state")
	}
}


func TestLinuxTunInterfaceCreationFailureRollsBackPartialDevice(t *testing.T) {
	stages := []struct {
		name string
		fail string
	}{
		{name: "address assignment", fail: "ip addr add"},
		{name: "link up", fail: "ip link set csqtt0 up"},
		{name: "MTU configuration", fail: "ip link set csqtt0 mtu 1300"},
	}
	for _, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			logPath := installTunCommandShims(t, stage.fail)
			app := tunTestApp()
			err := app.tun.SetupRoutes("192.0.2.10", "8.8.8.8")
			if err == nil {
				t.Fatalf("expected injected %s failure", stage.name)
			}
			log := readTunCommandLog(t, logPath)
			if !strings.Contains(log, "SUDO ip tuntap del dev csqtt0 mode tun") {
				t.Fatalf("partially created TUN interface was not rolled back:\\n%s", log)
			}
			if strings.Contains(log, "SUDO ip route add ") || strings.Contains(log, "SUDO ip rule add pref 22020") {
				t.Fatalf("routing was mutated after interface creation failed:\\n%s", log)
			}
		})
	}
}

// These lifecycle tests use injected file/ioctl operations; they never require
// /dev/net/tun or mutate host networking.
func TestLinuxTunOpenFailureIsReturned(t *testing.T) {
	app := tunTestApp()
	app.tun.tunOpen = func() (*os.File, error) { return nil, os.ErrPermission }
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	if err := app.tun.StartWithError(left, new(bool)); err == nil || !strings.Contains(err.Error(), "open /dev/net/tun") {
		t.Fatalf("expected surfaced TUN open error, got %v", err)
	}
	if app.tun.tunFile != nil { t.Fatal("TUN file retained after open failure") }
}

func TestLinuxTunSetIFFFailureClosesOpenedFile(t *testing.T) {
	app := tunTestApp()
	f, err := os.CreateTemp(t.TempDir(), "fake-tun")
	if err != nil { t.Fatal(err) }
	app.tun.tunOpen = func() (*os.File, error) { return f, nil }
	app.tun.tunSetIFF = func(*os.File, *unix.Ifreq) error { return errors.New("injected TUNSETIFF failure") }
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	if err := app.tun.StartWithError(left, new(bool)); err == nil || !strings.Contains(err.Error(), "TUNSETIFF") {
		t.Fatalf("expected surfaced TUNSETIFF error, got %v", err)
	}
	if _, err := f.Stat(); err == nil { t.Fatal("file remained open after TUNSETIFF failure") }
	if app.tun.tunFile != nil { t.Fatal("TUN file retained after TUNSETIFF failure") }
}

func TestLinuxTunStopJoinsWorkersAndIsIdempotent(t *testing.T) {
	app := tunTestApp()
	f, err := os.CreateTemp(t.TempDir(), "fake-tun")
	if err != nil { t.Fatal(err) }
	app.tun.tunOpen = func() (*os.File, error) { return f, nil }
	app.tun.tunSetIFF = func(*os.File, *unix.Ifreq) error { return nil }
	left, right := net.Pipe()
	defer right.Close()
	if err := app.tun.StartWithError(left, new(bool)); err != nil { t.Fatalf("start: %v", err) }
	app.tun.Stop()
	if _, err := f.Stat(); err == nil { t.Fatal("TUN file remained open after Stop") }
	if app.tun.tunFile != nil || app.tun.udpConn != nil { t.Fatal("Stop retained resource references") }
	app.tun.Stop()
	app.tun.workers.Wait()
}


func TestDirectGeneratesDualStackNftDnsAndPolicyRules(t *testing.T) {
    logPath := installTunCommandShims(t, "")
    isolateDirectFilesystem(t)
    app := tunTestApp()
    if !app.SaveSettings(`{"directDomains":"example.com","directIPs":"192.0.2.44\n2001:db8::44"}`) {
        t.Fatal("could not persist Direct test settings")
    }
    if err := app.tun.SetupRoutes("192.0.2.10", "8.8.8.8"); err != nil {
        t.Fatalf("TUN + Direct setup failed: %v", err)
    }
    nftConfig, err := os.ReadFile(directNftPath)
    if err != nil { t.Fatal(err) }
    nft := string(nftConfig)
    for _, required := range []string{
        "table inet lalune_direct",
        "set direct4 { type ipv4_addr; flags interval; elements = { 192.0.2.44 }; }",
        "set direct6 { type ipv6_addr; flags interval; elements = { 2001:db8::44 }; }",
        "ip daddr @direct4 meta mark set 0x4c4c",
        "ip6 daddr @direct6 meta mark set 0x4c4c",
    } {
        if !strings.Contains(nft, required) {
            t.Fatalf("production nft Direct config missing %q:\\n%s", required, nft)
        }
    }
    dnsConfig, err := os.ReadFile(directDnsmasqPath)
    if err != nil { t.Fatalf("read production dnsmasq Direct config: %v", err) }
    dns := string(dnsConfig)
    combinedNFTSet := "nftset=/example.com/4#inet#lalune_direct#direct4,6#inet#lalune_direct#direct6"
    if strings.TrimSpace(string(dns)) != combinedNFTSet {
        t.Fatalf("production dnsmasq config must use the combined A/AAAA nftset directive; got:\\n%s", dns)
    }
    if strings.Contains(string(dns), "nftset=/example.com/6#inet#lalune_direct#direct6") {
        t.Fatalf("production dnsmasq config regressed to separate AAAA directive:\\n%s", dns)
    }
    mainConfig, err := os.ReadFile(directDnsmasqMain)
    if err != nil { t.Fatal(err) }
    if !strings.Contains(string(mainConfig), directDnsmasqInclude) {
        t.Fatalf("dnsmasq main config does not include Direct nftset rules:\\n%s", mainConfig)
    }
    log := readTunCommandLog(t, logPath)
    if !strings.Contains(log, "SUDO systemctl restart dnsmasq") {
        t.Fatalf("dnsmasq was not restarted after enabling domain Direct rules:\\n%s", log)
    }
    for _, required := range []string{
        "SUDO nft -c -f " + directNftPath,
        "SUDO nft -f " + directNftPath,
        "SUDO ip rule add pref 22010 fwmark 0x4c4c/0xffff lookup main",
        "SUDO ip -6 rule add pref 22010 fwmark 0x4c4c/0xffff lookup main",
    } {
        if !strings.Contains(log, required) { t.Fatalf("Direct command trace missing %q:\n%s", required, log) }
    }
    if strings.Contains(log, "ip rule del pref 22010 2>/dev/null") {
        t.Fatalf("Direct reconciliation used unsafe priority-only deletion:\n%s", log)
    }
}

func TestDirectCleanupUsesExactIPv4AndIPv6RuleSignature(t *testing.T) {
    logPath := installTunCommandShims(t, "")
    isolateDirectFilesystem(t)
    app := tunTestApp()
    app.tun.CleanupDirectRouting()
    log := readTunCommandLog(t, logPath)
    for _, required := range []string{
        "SUDO ip rule del pref 22010 fwmark 0x4c4c/0xffff lookup main",
        "SUDO ip -6 rule del pref 22010 fwmark 0x4c4c/0xffff lookup main",
    } {
        if !strings.Contains(log, required) { t.Fatalf("exact Direct cleanup missing %q:\n%s", required, log) }
    }
}


func TestDirectPartialApplyRollsBackEveryMutatingStage(t *testing.T) {
	stages := []struct {
		name string
		fail string
	}{
		{name: "nft validation", fail: "nft -c -f"},
		{name: "nft apply", fail: "nft -f"},
		{name: "dnsmasq restart", fail: "systemctl restart dnsmasq"},
		{name: "IPv4 policy rule", fail: "ip rule add pref 22010"},
		{name: "IPv6 policy rule", fail: "ip -6 rule add pref 22010"},
	}
	for _, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			logPath := installTunCommandShims(t, stage.fail)
			isolateDirectFilesystem(t)
			app := tunTestApp()
			if !app.SaveSettings(`{"directDomains":"example.com","directIPs":"192.0.2.44,2001:db8::44"}`) {
				t.Fatal("could not persist Direct rollback settings")
			}
			if err := app.tun.ApplyDirectRouting(); err == nil {
				t.Fatalf("injected %s failure unexpectedly succeeded", stage.name)
			}
			for _, path := range []string{directNftPath, directDnsmasqPath} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("rollback left Direct artifact %s: err=%v", path, err)
				}
			}
			mainConfig, err := os.ReadFile(directDnsmasqMain)
			if err != nil { t.Fatal(err) }
			if strings.Contains(string(mainConfig), directDnsmasqInclude) {
				t.Fatalf("rollback left Direct dnsmasq include: %s", mainConfig)
			}
			log := readTunCommandLog(t, logPath)
			for _, required := range []string{
				"SUDO ip rule del pref 22010 fwmark 0x4c4c/0xffff lookup main",
				"SUDO ip -6 rule del pref 22010 fwmark 0x4c4c/0xffff lookup main",
				"SUDO nft delete table inet lalune_direct",
			} {
				if !strings.Contains(log, required) {
					t.Fatalf("%s rollback omitted %q:\\n%s", stage.name, required, log)
				}
			}
			if strings.Contains(log, "ip rule del pref 22010 2>/dev/null") ||
				strings.Contains(log, "ip -6 rule del pref 22010 2>/dev/null") {
				t.Fatalf("rollback used priority-only deletion and could remove foreign rules:\\n%s", log)
			}
		})
	}
}

func TestDirectRollbackPreservesForeignPriority22010Rule(t *testing.T) {
	logPath := installTunCommandShims(t, "ip rule add pref 22010 fwmark 0x4c4c/0xffff lookup main")
	isolateDirectFilesystem(t)
	app := tunTestApp()
	if !app.SaveSettings(`{"directDomains":"example.com"}`) {
		t.Fatal("could not persist Direct conflict settings")
	}
	if err := app.tun.ApplyDirectRouting(); err == nil {
		t.Fatal("injected Direct rule conflict unexpectedly succeeded")
	}
	log := readTunCommandLog(t, logPath)
	if !strings.Contains(log, "SUDO ip rule del pref 22010 fwmark 0x4c4c/0xffff lookup main") {
		t.Fatalf("rollback did not target the exact CSQTT rule signature:\\n%s", log)
	}
	if strings.Contains(log, "ip rule del pref 22010 2>/dev/null") {
		t.Fatalf("rollback could delete an unrelated rule sharing priority 22010:\\n%s", log)
	}
	if !strings.Contains(log, "SUDO ip rule add pref 22010 fwmark 0x4c4c/0xffff lookup main") {
		t.Fatalf("test did not reach the injected foreign-priority collision:\\n%s", log)
	}
}
