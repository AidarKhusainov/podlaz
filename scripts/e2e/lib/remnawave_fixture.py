#!/usr/bin/env python3
import argparse
import datetime as dt
import json
import os
import pathlib
import secrets
import shutil
import socket
import subprocess
import time
import urllib.error
import urllib.request

PANEL_VERSION = os.environ.get("REMNAWAVE_PANEL_VERSION", "3.4.5")
NODE_VERSION = os.environ.get("REMNAWAVE_NODE_VERSION", "3.4.2")
PANEL_IMAGE = os.environ.get("REMNAWAVE_PANEL_IMAGE", `ghcr.io/remnawave/backend:${PANEL_VERSION}`)
NODE_IMAGE = os.environ.get("REMNAWAVE_NODE_IMAGE", `ghcr.io/remnawave/node:${NODE_VERSION}`)
POSTGRES_IMAGE = os.environ.get("REMNAWAVE_POSTGRES_IMAGE", "postgres:18.4")
VALKEY_IMAGE = os.environ.get("REMNAWAVE_VALKEY_IMAGE", "valkey/valkey:9-alpine")
PANEL_BASE = "http://127.0.0.1:3000"
METRICS_BASE = "http://127.0.0.1:3001"
XRAY_PORT = int(os.environ.get("REMNAWAVE_XRAY_PORT", "19443"))
NODE_PORT = int(os.environ.get("REMNAWAVE_NODE_PORT", "2222"))


def fail(msg: str):
    raise RuntimeError(msg)


def run(cmd, *, env=None, capture=False, check=True):
    process = subprocess.run(
        cmd,
        env=env,
        text=True,
        stdout=subprocess.PIPE if capture else subprocess.DEVNULL,
        stderr=subprocess.PIPE if capture else subprocess.DEVNULL,
    )
    if check and process.returncode != 0:
        raise RuntimeError(f"command failed ({cmd[0]}) rc={process.returncode}")
    return process.stdout.strip() if capture else process.returncode


def write_private(path: pathlib.Path, data: str):
    path.parent.mkdir(parents=True, exist_ok=True)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as handle:
        handle.write(data)


def load_state(root):
    return json.loads((root / "state.json").read_text())


def save_state(root, state):
    write_private(root / "state.json", json.dumps(state, separators=(",", ":")) + "\n")


def http(method, path, body=None, token=None, *, expect=(200, 201), retries=1):
    data = None if body is None else json.dumps(body).encode()
    headers = {"Accept": "application/json"}
    if data is not None:
        headers["Content-Type"] = "application/json"
    if token:
        headers["Authorization"] = f"Bearer {token}"
    last = None
    for attempt in range(retries):
        request = urllib.request.Request(
            PANEL_BASE + path, data=data, headers=headers, method=method
        )
        try:
            with urllib.request.urlopen(request, timeout=8) as response:
                raw = response.read()
                if response.status not in expect:
                    last = RuntimeError(
                        f"API {method} {path} unexpected status {response.status}"
                    )
                else:
                    return json.loads(raw) if raw else {}
        except urllib.error.HTTPError as error:
            last = RuntimeError(f"API {method} {path} status {error.code}")
        except (urllib.error.URLError, TimeoutError, socket.timeout):
            last = RuntimeError(f"API {method} {path} unavailable")
        if attempt + 1 < retries:
            time.sleep(min(1 + attempt, 3))
    raise last or RuntimeError(f"API {method} {path} failed")


def wait_http(url, timeout=180):
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=3) as response:
                if 200 <= response.status < 300:
                    return
        except Exception:
            pass
        time.sleep(2)
    fail("fixture health deadline exceeded")


def primary_ipv4():
    output = run(["ip", "-4", "route", "get", "1.1.1.1"], capture=True)
    parts = output.split()
    if "src" not in parts:
        fail("cannot determine runner IPv4")
    ip = parts[parts.index("src") + 1]
    socket.inet_aton(ip)
    return ip


def rand_alnum(length=48):
    alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
    return "".join(secrets.choice(alphabet) for _ in range(length))


def compose_text(app_secret, db_pass, metrics_user, metrics_pass):
    return f"""services:
  db:
    image: {POSTGRES_IMAGE}
    environment:
      POSTGRES_USER: remnawave
      POSTGRES_PASSWORD: {db_pass}
      POSTGRES_DB: remnawave
      TZ: UTC
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U remnawave -d remnawave"]
      interval: 3s
      timeout: 5s
      retries: 20
    volumes:
      - db-data:/var/lib/postgresql
  valkey:
    image: {VALKEY_IMAGE}
    command: ["valkey-server", "--save", "", "--appendonly", "no", "--maxmemory-policy", "noeviction", "--loglevel", "warning", "--unixsocket", "/var/run/valkey/valkey.sock", "--unixsocketperm", "777", "--port", "0"]
    healthcheck:
      test: ["CMD", "valkey-cli", "-s", "/var/run/valkey/valkey.sock", "ping"]
      interval: 3s
      timeout: 3s
      retries: 20
    volumes:
      - valkey-socket:/var/run/valkey
  panel:
    image: {PANEL_IMAGE}
    environment:
      APP_PORT: "3000"
      METRICS_PORT: "3001"
      API_INSTANCES: "1"
      DATABASE_URL: postgresql://remnawave:{db_pass}@db:5432/remnawave
      REDIS_SOCKET: /var/run/valkey/valkey.sock
      APP_SECRET: {app_secret}
      FRONT_END_DOMAIN: "*"
      PANEL_DOMAIN: localhost
      SUB_PUBLIC_DOMAIN: localhost/api/sub
      METRICS_USER: {metrics_user}
      METRICS_PASS: {metrics_pass}
      IS_TELEGRAM_NOTIFICATIONS_ENABLED: "false"
      WEBHOOK_ENABLED: "false"
      EXPORT_TO_STREAM_ENABLED: "false"
      SERVICE_SNI_VERIFICATION: "true"
    ports:
      - "127.0.0.1:3000:3000"
      - "127.0.0.1:3001:3001"
    extra_hosts:
      - "host.docker.internal:host-gateway"
    healthcheck:
      test: ["CMD-SHELL", "curl -fsS http://localhost:3001/health >/dev/null"]
      interval: 5s
      timeout: 5s
      retries: 30
      start_period: 10s
    depends_on:
      db:
        condition: service_healthy
      valkey:
        condition: service_healthy
    volumes:
      - valkey-socket:/var/run/valkey
volumes:
  db-data:
  valkey-socket:
"""


def bootstrap(root: pathlib.Path):
    root.mkdir(parents=True, exist_ok=False)
    os.chmod(root, 0o700)
    project = "podlaz-rw-" + secrets.token_hex(4)
    app_secret = rand_alnum(64)
    db_pass = rand_alnum(40)
    metrics_user = "m" + secrets.token_hex(8)
    metrics_pass = rand_alnum(32)
    admin_user = "admin_" + secrets.token_hex(5)
    admin_pass = "A" + rand_alnum(28) + "9a"
    compose = root / "compose.yml"
    write_private(
        compose, compose_text(app_secret, db_pass, metrics_user, metrics_pass)
    )
    env = dict(os.environ)
    env["COMPOSE_PROJECT_NAME"] = project
    run(["docker", "compose", "-f", str(compose), "pull"], env=env)
    run(
        ["docker", "compose", "-f", str(compose), "up", "-d", "--wait"], env=env
    )
    wait_http(METRICS_BASE + "/health")

    registered = http(
        "POST",
        "/api/auth/register",
        {"username": admin_user, "password": admin_pass},
        expect=(200, 201),
    )
    token = registered["response"]["accessToken"]
    settings = http("GET", "/api/subscription-settings", token=token)["response"]
    http(
        "PATCH",
        "/api/subscription-settings",
        {
            "uuid": settings["uuid"],
            "hwidSettings": {
                "enabled": True,
                "fallbackDeviceLimit": 1,
                "maxDevicesAnnounce": None,
            },
        },
        token=token,
        expect=(200, 201),
    )

    xray_config = {
        "log": {
            "access": "/var/log/remnanode/access.log",
            "error": "/var/log/remnanode/error.log",
            "loglevel": "warning",
        },
        "inbounds": [
            {
                "tag": "PODLAZ_CI_VLESS",
                "listen": "0.0.0.0",
                "port": XRAY_PORT,
                "protocol": "vless",
                "settings": {"clients": [], "decryption": "none"},
                "streamSettings": {"network": "tcp", "security": "none"},
                "sniffing": {
                    "enabled": True,
                    "destOverride": ["http", "tls"],
                },
            }
        ],
        "outbounds": [{"tag": "DIRECT", "protocol": "freedom"}],
    }
    profile = http(
        "POST",
        "/api/config-profiles",
        {"name": "Podlaz CI", "config": xray_config},
        token=token,
        expect=(200, 201),
    )["response"]
    if len(profile.get("inbounds", [])) != 1:
        fail("Remnawave config profile did not expose exactly one inbound")
    inbound_uuid = profile["inbounds"][0]["uuid"]

    secret_key = http("GET", "/api/keygen", token=token)["response"]["secretKey"]
    log_dir = root / "node-logs"
    log_dir.mkdir(mode=0o700)
    node_name = project + "-node"
    node_env = dict(os.environ)
    node_env["NODE_PORT"] = str(NODE_PORT)
    node_env["SECRET_KEY"] = secret_key
    run(
        [
            "docker",
            "run",
            "-d",
            "--name",
            node_name,
            "--network",
            "host",
            "--label",
            f"podlaz.remnawave.project={project}",
            "-e",
            "NODE_PORT",
            "-e",
            "SECRET_KEY",
            "-v",
            f"{log_dir}:/var/log/remnanode",
            NODE_IMAGE,
        ],
        env=node_env,
    )

    node = http(
        "POST",
        "/api/nodes",
        {
            "name": "Podlaz CI Node",
            "address": "host.docker.internal",
            "port": NODE_PORT,
            "isTrafficTrackingActive": False,
            "countryCode": "XX",
            "configProfile": {
                "activeConfigProfileUuid": profile["uuid"],
                "activeInbounds": [inbound_uuid],
            },
        },
        token=token,
        expect=(200, 201),
    )["response"]
    deadline = time.time() + 120
    while time.time() < deadline:
        connected = http("GET", f"/api/nodes/{node['uuid']}", token=token)[
            "response"
        ]
        if connected.get("isConnected") and connected.get("versions"):
            break
        time.sleep(2)
    else:
        fail("Remnawave Node did not become connected")

    squad = http(
        "POST",
        "/api/internal-squads",
        {"name": "Podlaz CI Squad", "inbounds": [inbound_uuid]},
        token=token,
        expect=(200, 201),
    )["response"]
    runner_ip = primary_ipv4()
    host = http(
        "POST",
        "/api/hosts",
        {
            "inbound": {
                "configProfileUuid": profile["uuid"],
                "configProfileInboundUuid": inbound_uuid,
            },
            "remark": "Podlaz CI",
            "address": runner_ip,
            "port": XRAY_PORT,
            "path": None,
            "sni": None,
            "host": None,
            "alpn": None,
            "fingerprint": None,
            "securityLayer": "NONE",
            "nodes": [node["uuid"]],
            "internalSquads": {
                "mode": "ALLOW_ONLY",
                "squads": [squad["uuid"]],
            },
        },
        token=token,
        expect=(200, 201),
    )["response"]

    state = {
        "project": project,
        "compose": str(compose),
        "token": token,
        "admin_user": admin_user,
        "admin_pass": admin_pass,
        "panel_version": PANEL_VERSION,
        "node_version": NODE_VERSION,
        "images": {
            "panel": PANEL_IMAGE,
            "node": NODE_IMAGE,
            "postgres": POSTGRES_IMAGE,
            "valkey": VALKEY_IMAGE,
        },
        "node_container": node_name,
        "node_uuid": node["uuid"],
        "profile_uuid": profile["uuid"],
        "inbound_uuid": inbound_uuid,
        "squad_uuid": squad["uuid"],
        "host_uuid": host["uuid"],
        "runner_ip": runner_ip,
        "node_access_log": str(log_dir / "access.log"),
        "users": {},
    }
    save_state(root, state)
    create_user(root, "hwid", 1)
    create_user(root, "data-plane", 1)


def create_user(root, name, limit):
    state = load_state(root)
    token = state["token"]
    expire = (
        dt.datetime.now(dt.timezone.utc) + dt.timedelta(hours=3)
    ).isoformat().replace("+00:00", "Z")
    user = http(
        "POST",
        "/api/users",
        {
            "username": f"podlaz_{name}_{secrets.token_hex(4)}",
            "expireAt": expire,
            "hwidDeviceLimit": limit,
            "activeInternalSquads": [state["squad_uuid"]],
        },
        token=token,
        expect=(200, 201),
    )["response"]
    short_uuid = user["shortUuid"]
    vless_uuid = user["vlessUuid"]
    user_dir = root / "users" / name
    user_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
    sub_url = f"{PANEL_BASE}/api/sub/{short_uuid}"
    profile_uri = (
        f"vless://{vless_uuid}@{state['runner_ip']}:{XRAY_PORT}"
        "?type=tcp&security=none&encryption=none#remnawave-ci"
    )
    write_private(user_dir / "subscription-url", sub_url + "\n")
    write_private(user_dir / "profile-uri", profile_uri + "\n")
    state["users"][name] = {
        "id": user["id"],
        "uuid": user["uuid"],
        "short_uuid": short_uuid,
        "vless_uuid": vless_uuid,
        "limit": limit,
    }
    save_state(root, state)


def hwid_snapshot(root, name, output):
    state = load_state(root)
    user = state["users"][name]
    data = http(
        "GET", f"/api/hwid/devices/{user['id']}", token=state["token"]
    )
    write_private(output, json.dumps(data["response"], separators=(",", ":")) + "\n")


def hwid_clear(root, name):
    state = load_state(root)
    user = state["users"][name]
    data = http(
        "GET", f"/api/hwid/devices/{user['id']}", token=state["token"]
    )["response"]
    for device in data.get("devices", []):
        http(
            "POST",
            "/api/hwid/devices/delete",
            {"userId": user["id"], "hwid": device["hwid"]},
            token=state["token"],
            expect=(200, 201),
        )


def digests(root, output):
    state = load_state(root)
    rows = {}
    for key, image_name in state["images"].items():
        raw = run(
            [
                "docker",
                "image",
                "inspect",
                image_name,
                "--format",
                "{{json .RepoDigests}}",
            ],
            capture=True,
        )
        values = json.loads(raw or "[]")
        digest = ""
        for value in values:
            if "@sha256:" in value:
                digest = value.split("@", 1)[1]
                break
        if not digest:
            fail(f"image digest unavailable for {key}")
        rows[key] = digest
    write_private(output, json.dumps(rows, sort_keys=True) + "\n")


def cleanup(root):
    if not root.exists():
        return
    try:
        state = load_state(root)
    except Exception:
        state = {}
    node = state.get("node_container")
    if node:
        run(["docker", "rm", "-f", node], check=False)
    compose = state.get("compose")
    project = state.get("project")
    if compose and project and pathlib.Path(compose).exists():
        env = dict(os.environ)
        env["COMPOSE_PROJECT_NAME"] = project
        run(
            [
                "docker",
                "compose",
                "-f",
                compose,
                "down",
                "-v",
                "--remove-orphans",
            ],
            env=env,
            check=False,
        )
    if project:
        leftover = run(
            [
                "docker",
                "ps",
                "-aq",
                "--filter",
                f"label=podlaz.remnawave.project={project}",
            ],
            capture=True,
            check=False,
        )
        if leftover:
            fail("fixture container cleanup incomplete")
    shutil.rmtree(root, ignore_errors=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", required=True)
    subparsers = parser.add_subparsers(dest="cmd", required=True)
    subparsers.add_parser("bootstrap")
    create = subparsers.add_parser("create-user")
    create.add_argument("--name", required=True)
    create.add_argument("--limit", type=int, required=True)
    snapshot = subparsers.add_parser("hwid-snapshot")
    snapshot.add_argument("--name", required=True)
    snapshot.add_argument("--output", required=True)
    clear = subparsers.add_parser("hwid-clear")
    clear.add_argument("--name", required=True)
    digest_parser = subparsers.add_parser("digests")
    digest_parser.add_argument("--output", required=True)
    subparsers.add_parser("cleanup")
    args = parser.parse_args()
    root = pathlib.Path(args.root).resolve()
    if args.cmd == "bootstrap":
        bootstrap(root)
    elif args.cmd == "create-user":
        create_user(root, args.name, args.limit)
    elif args.cmd == "hwid-snapshot":
        hwid_snapshot(root, args.name, pathlib.Path(args.output).resolve())
    elif args.cmd == "hwid-clear":
        hwid_clear(root, args.name)
    elif args.cmd == "digests":
        digests(root, pathlib.Path(args.output).resolve())
    elif args.cmd == "cleanup":
        cleanup(root)


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(f"remnawave fixture: {error}", file=os.sys.stderr)
        raise SystemExit(1)
