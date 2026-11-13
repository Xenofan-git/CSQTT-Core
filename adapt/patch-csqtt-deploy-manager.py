#!/usr/bin/env python3
"""Adapt pinned LaLune DeployManager to use the original CSQTT v2.1.9 deploy.sh.

CSQTT keeps upstream's SSH -> deploy.sh install/uninstall and health checks.
Only bundled assets, ports, secrets transport, and namespaced runtime ownership
are adapted. No remote host is changed by this source patcher.
"""
from pathlib import Path

ROOT = Path("upstream-lalune")

def replace_once(path: Path, old: str, new: str) -> None:
    source = path.read_text()
    count = source.count(old)
    if count != 1:
        raise SystemExit(f"expected one marker in {path}, found {count}: {old[:100]!r}")
    path.write_text(source.replace(old, new, 1))

main = ROOT / "Core/DeployManager/src/main.rs"
replace_once(
    main,
    "    pub listen_port: Option<u16>,",
    """    pub listen_port: Option<u16>,

    /// Local directory containing the pinned, namespaced CSQTT v2.1.9 assets.
    #[arg(long)]
    pub local_binary_dir: Option<String>,

    /// One-shot local file containing the tunnel/web credentials.
    #[arg(long)]
    pub secrets_file: Option<String>,

    /// Docker mode is rejected for the isolated LaLune service.
    #[arg(long, default_value_t = false)]
    pub install_in_docker: bool,

    /// Remove only the namespaced LaLune CSQTT runtime.
    #[arg(long, default_value_t = false)]
    pub uninstall: bool,""",
)

protocol = ROOT / "Core/DeployManager/src/protocol.rs"
replace_once(protocol, '            Protocol::Csqtt => "csqtt-core",', '            Protocol::Csqtt => "csqtt-lalune",')
replace_once(protocol, "            Protocol::Csqtt => (443, 0, 1080),", "            Protocol::Csqtt => (47000, 47002, 0),")

deploy = ROOT / "Core/DeployManager/src/deploy.rs"
replace_once(
    deploy,
    "pub fn deploy(args: &DeployArgs, proto: Protocol, ports: &Ports) -> Result<()> {\n    let remote =",
    """pub fn deploy(args: &DeployArgs, proto: Protocol, ports: &Ports) -> Result<()> {
    if proto == Protocol::Csqtt {
        return deploy_official_csqtt(args, ports);
    }
    let remote =""",
)

run_scp = r'''fn run_scp(args: &DeployArgs, remote: &str, local: &str, remote_stage: &str) -> Result<()> {
    let mut scp_args = vec![
        "-r".to_string(),
        "-o".to_string(),
        "StrictHostKeyChecking=accept-new".to_string(),
        "-o".to_string(),
        "ConnectTimeout=20".to_string(),
        "-P".to_string(),
        args.ssh_port.to_string(),
    ];
    if let Some(key) = &args.key {
        scp_args.push("-i".to_string());
        scp_args.push(key.clone());
    }

    let target = format!("{remote}:{remote_stage}/");
    let mut askpass_env: Option<(std::path::PathBuf, String)> = None;
    let (program, mut command_args) = if args.password.is_some() && have_sshpass() {
        let mut a = vec!["-e".to_string(), "scp".to_string()];
        a.extend(scp_args.clone());
        ("sshpass".to_string(), a)
    } else {
        if let Some(password) = &args.password {
            let helper = write_askpass_helper()?;
            askpass_env = Some((helper, password.clone()));
            scp_args.push("-o".to_string());
            scp_args.push("PreferredAuthentications=password,keyboard-interactive".to_string());
            scp_args.push("-o".to_string());
            scp_args.push("PubkeyAuthentication=no".to_string());
        }
        ("scp".to_string(), scp_args.clone())
    };
    command_args.push(local.to_string());
    command_args.push(target);

    let mut command = Command::new(&program);
    command.args(&command_args);
    if let Some(password) = &args.password {
        if program == "sshpass" {
            command.env("SSHPASS", password);
        }
    }
    if let Some((helper, password)) = &askpass_env {
        command.env("SSHPASS", password);
        command.env("SSH_ASKPASS", helper);
        command.env("SSH_ASKPASS_REQUIRE", "force");
        if std::env::var_os("DISPLAY").is_none() {
            command.env("DISPLAY", ":0");
        }
    }

    let result = command.output();
    if let Some((helper, _)) = askpass_env {
        let _ = std::fs::remove_file(helper);
    }
    let output = result.context("scp failed to start")?;
    if !output.status.success() {
        anyhow::bail!("scp failed: {}", String::from_utf8_lossy(&output.stderr));
    }
    Ok(())
}

'''
replace_once(deploy, "fn run_ssh(args: &DeployArgs, remote: &str, script: &str) -> Result<std::process::Output> {", run_scp + "fn run_ssh(args: &DeployArgs, remote: &str, script: &str) -> Result<std::process::Output> {")

official = r'''fn deploy_official_csqtt(args: &DeployArgs, ports: &Ports) -> Result<()> {
    if args.install_in_docker {
        anyhow::bail!("Docker mode is disabled for the isolated LaLune service; no remote changes were made.");
    }

    let source_dir = args.local_binary_dir.as_deref()
        .ok_or_else(|| anyhow::anyhow!("bundled LaLune CSQTT assets directory was not found"))?;
    let source = std::path::Path::new(source_dir);
    let deploy_script = source.join("deploy-csqtt-lalune.sh");
    if !deploy_script.is_file() {
        anyhow::bail!("namespaced original CSQTT 2.1.9 deploy script is missing: {}", deploy_script.display());
    }

    let mut main_password = String::new();
    let mut web_user = String::new();
    let mut web_password = String::new();
    let mut device_id = String::new();
    if !args.uninstall {
        let secrets_path = args.secrets_file.as_deref()
            .ok_or_else(|| anyhow::anyhow!("CSQTT requires the one-shot secrets file"))?;
        let secret_text = std::fs::read_to_string(secrets_path)
            .context("read CSQTT deployment credentials")?;
        let _ = std::fs::remove_file(secrets_path);
        let mut lines = secret_text.lines();
        main_password = lines.next().unwrap_or("").trim().to_owned();
        web_user = lines.next().unwrap_or("").trim().to_owned();
        web_password = lines.next().unwrap_or("").trim().to_owned();
        device_id = lines.next().unwrap_or("").trim().to_owned();
        let valid_secret = |s: &str| !s.is_empty() && s.bytes().all(|b| b.is_ascii_alphanumeric());
        let valid_device = |s: &str| !s.is_empty() && s.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'-');
        if !valid_secret(&main_password) || !valid_secret(&web_user) || !valid_secret(&web_password) {
            anyhow::bail!("tunnel password and web credentials must contain only Latin letters and digits");
        }
        if !valid_device(&device_id) {
            anyhow::bail!("LaLune device ID is missing or invalid; reopen the panel and retry");
        }
    }

    let nonce = std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH)?.as_nanos();
    let bundle_name = format!("lalune-csqtt-deploy-{}-{nonce:x}", std::process::id());
    let bundle = std::env::temp_dir().join(&bundle_name);
    std::fs::create_dir(&bundle).context("create temporary CSQTT deploy bundle")?;
    #[cfg(unix)]
    std::fs::set_permissions(&bundle, std::os::unix::fs::PermissionsExt::from_mode(0o700))
        .context("restrict temporary CSQTT bundle")?;

    let result = (|| -> Result<()> {
        std::fs::copy(&deploy_script, bundle.join("deploy-csqtt-lalune.sh"))
            .context("stage the adapted original deploy.sh")?;
        if !args.uninstall {
            for name in ["csqtt-lalune-linux-arm64", "csqtt-lalune-linux-amd64", "sing-box-linux-arm64", "sing-box-linux-amd64"] {
                let path = source.join(name);
                if !path.is_file() {
                    anyhow::bail!("required pinned CSQTT server asset is missing: {}", path.display());
                }
                std::fs::copy(path, bundle.join(name))
                    .with_context(|| format!("stage pinned server asset {name}"))?;
            }
            std::fs::write(
                bundle.join(".csqtt-lalune-upload-web.env"),
                format!("CSQTT_WEB_USER={web_user}\nCSQTT_WEB_PASS={web_password}\n"),
            ).context("write temporary web credentials")?;
            std::fs::write(
                bundle.join(".csqtt-lalune-upload-overrides.json"),
                format!("{{\"main_password\":\"{main_password}\",\"device_id\":\"{device_id}\"}}\n"),
            ).context("write temporary CSQTT deploy overrides")?;
            #[cfg(unix)]
            for name in [".csqtt-lalune-upload-web.env", ".csqtt-lalune-upload-overrides.json"] {
                std::fs::set_permissions(
                    bundle.join(name),
                    std::os::unix::fs::PermissionsExt::from_mode(0o600),
                ).context("restrict temporary CSQTT credentials")?;
            }
        }

        let remote_host = if args.host.contains(':') && !args.host.starts_with('[') {
            format!("[{}]", args.host)
        } else {
            args.host.clone()
        };
        let remote = format!("{}@{}", args.user, remote_host);
        let remote_stage = format!("/tmp/lalune-csqtt-deploy-{}-{nonce:x}", std::process::id());
        let prepare = format!("set -eu; test ! -e '{remote_stage}'; install -d -m 700 '{remote_stage}'");
        let prepared = run_ssh(args, &remote, &prepare)
            .context("prepare unique remote CSQTT staging directory")?;
        if !prepared.status.success() {
            anyhow::bail!("could not prepare remote staging directory: {}", String::from_utf8_lossy(&prepared.stderr));
        }
        if let Err(error) = run_scp(args, &remote, bundle.to_str().unwrap_or(""), &remote_stage) {
            let _ = run_ssh(args, &remote, &format!("rm -rf -- '{remote_stage}'"));
            return Err(error).context("upload original CSQTT deploy bundle");
        }

        let remote_bundle = format!("{remote_stage}/{bundle_name}");
        let peer_port = ports.core.unwrap_or(47000);
        let web_port = ports.warp.unwrap_or(47002);
        let ssh_port = args.ssh_port;
        let script = if args.uninstall {
            format!(r#"set -Eeuo pipefail
STAGE='{remote_stage}'
BUNDLE='{remote_bundle}'
trap 'rm -rf -- "$STAGE"' EXIT
if [ "$(id -u)" -eq 0 ]; then
  env CSQTT_DEPLOY_STAGE="$BUNDLE" CSQTT_PEER_PORT='{peer_port}' CSQTT_WEB_PORT='{web_port}' CSQTT_SSH_PORT='{ssh_port}' CSQTT_DEPLOY_MODE=systemd bash "$BUNDLE/deploy-csqtt-lalune.sh" uninstall
else
  sudo -n env CSQTT_DEPLOY_STAGE="$BUNDLE" CSQTT_PEER_PORT='{peer_port}' CSQTT_WEB_PORT='{web_port}' CSQTT_SSH_PORT='{ssh_port}' CSQTT_DEPLOY_MODE=systemd bash "$BUNDLE/deploy-csqtt-lalune.sh" uninstall
fi
if systemctl is-active --quiet csqtt-lalune.service; then
  echo "LaLune CSQTT service remains active after uninstall" >&2
  exit 1
fi
if ip link show csqtt-lalune0 >/dev/null 2>&1; then
  echo "LaLune TUN interface remains after uninstall" >&2
  exit 1
fi
echo CSQTT_LALUNE_UNINSTALL_OK
"#, peer_port=peer_port, web_port=web_port, ssh_port=ssh_port)
        } else {
            format!(r#"set -Eeuo pipefail
STAGE='{remote_stage}'
BUNDLE='{remote_bundle}'
trap 'rm -rf -- "$STAGE"' EXIT
case "$(uname -m)" in
  aarch64|arm64) ASSET="$BUNDLE/csqtt-lalune-linux-arm64"; SING_BOX_ASSET="$BUNDLE/sing-box-linux-arm64" ;;
  x86_64|amd64) ASSET="$BUNDLE/csqtt-lalune-linux-amd64"; SING_BOX_ASSET="$BUNDLE/sing-box-linux-amd64" ;;
  *) echo "unsupported server architecture; no runtime files changed" >&2; exit 2 ;;
esac
install -m 0755 "$ASSET" "$BUNDLE/.csqtt-lalune-upload-server"
test -s "$SING_BOX_ASSET"
install -m 0755 "$SING_BOX_ASSET" "$BUNDLE/.csqtt-lalune-upload-sing-box"
chmod 0600 "$BUNDLE/.csqtt-lalune-upload-web.env" "$BUNDLE/.csqtt-lalune-upload-overrides.json"
if [ "$(id -u)" -eq 0 ]; then
  env CSQTT_DEPLOY_STAGE="$BUNDLE" CSQTT_PEER_PORT='{peer_port}' CSQTT_WEB_PORT='{web_port}' CSQTT_SSH_PORT='{ssh_port}' CSQTT_DEPLOY_MODE=systemd bash "$BUNDLE/deploy-csqtt-lalune.sh" install
else
  sudo -n env CSQTT_DEPLOY_STAGE="$BUNDLE" CSQTT_PEER_PORT='{peer_port}' CSQTT_WEB_PORT='{web_port}' CSQTT_SSH_PORT='{ssh_port}' CSQTT_DEPLOY_MODE=systemd bash "$BUNDLE/deploy-csqtt-lalune.sh" install
fi
systemctl is-active --quiet csqtt-lalune.service
ip link show csqtt-lalune0 >/dev/null
ss -H -lun "sport = :{peer_port}" | grep -q .
ss -H -ltn "sport = :{web_port}" | grep -q .
echo CSQTT_LALUNE_DEPLOY_OK
echo "PANEL_PORT={web_port}"
"#, peer_port=peer_port, web_port=web_port, ssh_port=ssh_port)
        };
        // The remote EXIT trap runs only if the install shell starts. An SSH
        // transport failure can leave the uploaded staging directory behind.
        let output = match run_ssh(args, &remote, &script) {
            Ok(output) => output,
            Err(error) => {
                let _ = run_ssh(args, &remote, &format!("rm -rf -- '{remote_stage}'"));
                return Err(error).context("run namespaced original CSQTT deploy.sh over SSH");
            }
        };
        let stdout = String::from_utf8_lossy(&output.stdout);
        let stderr = String::from_utf8_lossy(&output.stderr);
        for line in stdout.lines() { println!("[remote] {line}"); }
        for line in stderr.lines() { println!("[remote:err] {line}"); }
        let expected = if args.uninstall { "CSQTT_LALUNE_UNINSTALL_OK" } else { "CSQTT_LALUNE_DEPLOY_OK" };
        if !output.status.success() || !stdout.lines().any(|line| line.trim() == expected) {
            anyhow::bail!("namespaced original CSQTT deployment did not confirm {expected}");
        }
        println!("{expected}");
        if !args.uninstall {
            let host_for_url = if args.host.contains(':') && !args.host.starts_with('[') {
                format!("[{}]", args.host)
            } else {
                args.host.clone()
            };
            println!("PANEL_URL=https://{host_for_url}:{web_port}");
        }
        Ok(())
    })();
    let _ = std::fs::remove_dir_all(&bundle);
    result
}

'''
replace_once(deploy, "fn write_askpass_helper() -> Result<std::path::PathBuf> {", official + "fn write_askpass_helper() -> Result<std::path::PathBuf> {")

deploy_go = ROOT / "Desktop/Libs/deploy.go"
replace_once(
    deploy_go,
    '\tListenPort  int    `json:"listenPort"`',
    '\tListenPort  int    `json:"listenPort"`\n\tMainPassword string `json:"mainPassword"`\n\tWebUser string `json:"webUser"`\n\tWebPassword string `json:"webPassword"`\n\tDeviceID string `json:"deviceId"`\n\tDockerInstall bool `json:"dockerInstall"`\n\tUninstall bool `json:"uninstall"`',
)
replace_once(
    deploy_go,
    '\tif strings.TrimSpace(req.Host) == "" {',
    '''\tif strings.EqualFold(strings.TrimSpace(req.Protocol), "CSQTT") {
\t\tif err := validateCSQTTDeployTarget(req.Host); err != nil { deployAppend("[deploy] "+err.Error()); return false }
\t\tif !req.Uninstall {
\t\t\tif !validCSQTTSecret(req.MainPassword) || !validCSQTTSecret(req.WebUser) || !validCSQTTSecret(req.WebPassword) {
\t\t\t\tdeployAppend("[deploy] CSQTT: задайте пароль туннеля, логин и пароль WEB только латиницей и цифрами")
\t\t\t\treturn false
\t\t\t}
\t\t\tif !validCSQTTDeviceID(req.DeviceID) { deployAppend("[deploy] не удалось получить ID устройства LaLune"); return false }
\t\t}
\t}
\tif strings.TrimSpace(req.Host) == "" {''',
)

replace_once(
    deploy_go,
    'import (\n',
    'import (\n\t"fmt"\n\t"net"\n',
)
target_test = ROOT / "Desktop/Libs/deploy_target_test.go"
target_test.write_text("""package libs

import "testing"

func TestValidateCSQTTDeployTargetRejectsLocalAndPrivateAddresses(t *testing.T) {
    for _, host := range []string{"127.0.0.1", "::1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.1.1", "localhost"} {
        t.Run(host, func(t *testing.T) {
            if err := validateCSQTTDeployTarget(host); err == nil {
                t.Fatalf("local/private target %s was accepted", host)
            }
        })
    }
}

func TestValidateCSQTTDeployTargetAllowsPublicAndTailscaleAddresses(t *testing.T) {
    for _, host := range []string{"8.8.8.8", "100.70.73.57", "fd7a:115c:a1e0::1234"} {
        t.Run(host, func(t *testing.T) {
            if err := validateCSQTTDeployTarget(host); err != nil {
                t.Fatalf("valid remote target %s was rejected: %v", host, err)
            }
        })
    }
}
""")

replace_once(
    deploy_go,
    'func DeployProtocol(reqJSON string) bool {',
    '''func validateCSQTTDeployTarget(host string) error {
\t// The only local-target exception is explicitly enabled inside the disposable CI VM.
\tif os.Getenv("CSQTT_DEPLOY_ALLOW_LOCAL_TARGETS") == "1" { return nil }
\thost = strings.TrimSpace(strings.Trim(host, "[]"))
\tif host == "" { return fmt.Errorf("хост сервера не задан") }
\tips, err := net.LookupIP(host)
\tif err != nil || len(ips) == 0 { return fmt.Errorf("не удалось безопасно разрешить адрес сервера %q", host) }
\tlocal := map[string]bool{}
\tifaces, _ := net.Interfaces()
\tfor _, iface := range ifaces {
\t\taddrs, _ := iface.Addrs()
\t\tfor _, addr := range addrs {
\t\t\tif ipnet, ok := addr.(*net.IPNet); ok { local[ipnet.IP.String()] = true }
\t\t\tif ipaddr, ok := addr.(*net.IPAddr); ok { local[ipaddr.IP.String()] = true }
\t\t}
\t}
\tfor _, ip := range ips {
\t\ttailscale := false
\t\tif v4 := ip.To4(); v4 != nil {
\t\t\ttailscale = v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
\t\t} else {
\t\t\t_, tailnet, _ := net.ParseCIDR("fd7a:115c:a1e0::/48")
\t\t\ttailscale = tailnet != nil && tailnet.Contains(ip)
\t\t}
\t\tif local[ip.String()] || (!tailscale && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast())) {
\t\t\treturn fmt.Errorf("адрес %s относится к локальной/частной сети; деплой CSQTT разрешён только на отдельный удалённый VPS (публичный адрес или адрес Tailscale)", ip)
\t\t}
\t}
\treturn nil
}

func validCSQTTSecret(value string) bool {
\tif value == "" { return false }
\tfor _, r := range value { if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) { return false } }
\treturn true
}

func validCSQTTDeviceID(value string) bool {
\tif value == "" { return false }
\tfor _, r := range value { if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-') { return false } }
\treturn true
}

func DeployProtocol(reqJSON string) bool {''',
)
replace_once(
    deploy_go,
    '\tif req.SSHPort > 0 {',
    '''\tassetsDir := filepath.Join(filepath.Dir(binPath), "server-assets")
\tif st, err := os.Stat(assetsDir); err != nil || !st.IsDir() {
\t\tassetsDir = filepath.Join("/usr/local/lib/lalune", "server-assets")
\t}
\targs = append(args, "--local-binary-dir", assetsDir)
\tsecretsFile := ""
\tif strings.EqualFold(strings.TrimSpace(req.Protocol), "CSQTT") && !req.Uninstall {
\t\tf, err := os.CreateTemp("", "csqtt-deploy-secrets-*.txt")
\t\tif err != nil { deployAppend("[deploy] не удалось создать временный файл авторизации"); return false }
\t\t_ = f.Chmod(0600)
\t\t_, writeErr := f.WriteString(req.MainPassword + "\\n" + req.WebUser + "\\n" + req.WebPassword + "\\n" + req.DeviceID + "\\n")
\t\tcloseErr := f.Close()
\t\tif writeErr != nil || closeErr != nil { _ = os.Remove(f.Name()); deployAppend("[deploy] не удалось сохранить временные данные авторизации"); return false }
\t\tsecretsFile = f.Name()
\t\targs = append(args, "--secrets-file", secretsFile)
\t\tif req.DockerInstall { args = append(args, "--install-in-docker") }
\t}
\tif req.Uninstall { args = append(args, "--uninstall") }
\tif req.SSHPort > 0 {''',
)
replace_once(
    deploy_go,
    '\tif err != nil {\n\t\tdeployAppend("[deploy] ошибка запуска: " + err.Error())\n\t\treturn false\n\t}\n\tstderr, err := cmd.StderrPipe()',
    '\tif err != nil {\n\t\tif secretsFile != "" { _ = os.Remove(secretsFile) }\n\t\tdeployAppend("[deploy] ошибка запуска: " + err.Error())\n\t\treturn false\n\t}\n\tstderr, err := cmd.StderrPipe()',
)
replace_once(
    deploy_go,
    '\tif err != nil {\n\t\tdeployAppend("[deploy] ошибка запуска: " + err.Error())\n\t\treturn false\n\t}\n\n\tif err := cmd.Start(); err != nil {',
    '\tif err != nil {\n\t\tif secretsFile != "" { _ = os.Remove(secretsFile) }\n\t\tdeployAppend("[deploy] ошибка запуска: " + err.Error())\n\t\treturn false\n\t}\n\n\tif err := cmd.Start(); err != nil {',
)
replace_once(
    deploy_go,
    '\tif err := cmd.Start(); err != nil {\n\t\tdeployAppend("[deploy] не удалось запустить DeployManager: " + err.Error())',
    '\tif err := cmd.Start(); err != nil {\n\t\tif secretsFile != "" { _ = os.Remove(secretsFile) }\n\t\tdeployAppend("[deploy] не удалось запустить DeployManager: " + err.Error())',
)
replace_once(
    deploy_go,
    '\t\terr := cmd.Wait()\n\t\tif err != nil {',
    '\t\terr := cmd.Wait()\n\t\tif secretsFile != "" { _ = os.Remove(secretsFile) }\n\t\tif err != nil {',
)

print("CSQTT DeployManager now stages and invokes the namespaced original deploy.sh")
