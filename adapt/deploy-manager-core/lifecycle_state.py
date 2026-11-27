# Read-only evidence for the isolated lifecycle candidate. Never print secrets.
import hashlib, json, os, pathlib, sqlite3, stat, subprocess

def regular(path, limit=16777216):
    p = pathlib.Path(path)
    for parent in reversed(p.parents):
        st = parent.lstat()
        if not stat.S_ISDIR(st.st_mode) or st.st_uid != 0 or st.st_mode & 0o022:
            raise ValueError("unsafe parent")
    try:
        fd = os.open(p, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    except FileNotFoundError:
        return None
    with os.fdopen(fd, "rb") as f:
        st = os.fstat(f.fileno())
        if not stat.S_ISREG(st.st_mode) or st.st_uid != 0 or st.st_mode & 0o022 or st.st_nlink != 1 or st.st_size > limit:
            raise ValueError("unsafe file")
        data = f.read(limit+1)
        if len(data) > limit: raise ValueError("file too large")
        return data

def sha(data):
    return hashlib.sha256(data).hexdigest() if data is not None else ""

try:
    if os.geteuid() != 0: raise ValueError("requires root")
    result = {"machineID": regular("/etc/machine-id").decode().strip(),
              "virtual": subprocess.check_output(["systemd-detect-virt", "--vm"], timeout=5, text=True).strip()}
    # /etc/os-release is commonly a symlink; parse the standard data only.
    release = pathlib.Path("/etc/os-release").read_text()
    result["os"] = next((s[3:].strip('"') for s in release.splitlines() if s.startswith("ID=")), "")
    result["binary"] = sha(regular("/usr/local/bin/csqtt-core"))
    result["unit"] = sha(regular("/etc/systemd/system/csqtt-core.service"))
    root = pathlib.Path("/etc/csqtt-core")
    result.update(environment="", database="", config="")
    if root.exists() or root.is_symlink():
        st = root.lstat()
        if not stat.S_ISDIR(st.st_mode) or st.st_uid != 0 or st.st_mode & 0o022:
            raise ValueError("unsafe configuration")
        result["environment"] = sha(regular(root/"csqtt-core.env"))
        digest = hashlib.sha256()
        count = 0
        for path in sorted(root.rglob("*")):
            st = path.lstat()
            if st.st_uid != 0 or st.st_mode & 0o022 or stat.S_ISLNK(st.st_mode):
                raise ValueError("unsafe persistent state entry")
            if stat.S_ISDIR(st.st_mode): continue
            if path.name in ("csqtt.db", "csqtt.db-wal", "csqtt.db-shm", "deploy-overrides.json"): continue
            count += 1
            if count > 256: raise ValueError("too many configuration files")
            digest.update(str(path.relative_to(root)).encode()+b"\0")
            digest.update(regular(path))
        result["config"] = digest.hexdigest()
        db = root/"csqtt.db"
        if regular(db, 134217728) is not None:
            # Transaction provides a consistent view including the WAL.
            with sqlite3.connect(db.as_uri()+"?mode=ro", uri=True, timeout=5) as conn:
                conn.execute("PRAGMA query_only=ON")
                conn.execute("BEGIN")
                if conn.execute("PRAGMA integrity_check").fetchall() != [("ok",)]:
                    raise ValueError("SQLite integrity")
                digest = hashlib.sha256()
                total = 0
                for line in conn.iterdump():
                    data = line.encode()
                    total += len(data)
                    if total > 268435456: raise ValueError("SQLite evidence limit")
                    digest.update(data+b"\n")
                result["database"] = digest.hexdigest()
    print(json.dumps(result, separators=(",",":")))
except Exception:
    # No exception text: it may contain a secret value or SQL text.
    raise SystemExit(20)
