#!/usr/bin/python3
"""Receptor exclusivo de releases del bot; no acepta rutas ni comandos del cliente."""

import hashlib
import io
import re
import tarfile
import fcntl
import json
import os
import signal
import sqlite3
import subprocess
import sys
import tempfile
import time
from dataclasses import dataclass
from contextlib import closing
from datetime import datetime, timezone
from pathlib import Path

MAX_PACKAGE = 64 * 1024 * 1024
RELEASE_FILES = {"bot", "metricas", "REVISION", "SHA256SUMS"}


class HealthError(RuntimeError):
    """Readiness failure with a controlled message, never raw process logs."""


@dataclass(frozen=True)
class Paths:
    target: Path = Path("/opt/bot-marangatu")
    database: Path = Path("/var/lib/bot-marangatu/facturas.db")
    backups: Path = Path("/var/backups/bot-marangatu")
    lock: Path = Path("/run/lock/bot-marangatu-deploy.lock")


def deploy_release(payload, paths, service):
    files = validate_package(payload)
    revision = files["REVISION"].decode().strip()
    with paths.lock.open("a") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as error:
            raise RuntimeError("otro despliegue está en curso") from error
        previous = {name: (paths.target / name).read_bytes() for name in ("bot", "metricas", "REVISION")}
        paths.backups.mkdir(mode=0o700, parents=True, exist_ok=True)
        os.chmod(paths.backups, 0o700)
        stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
        backup = Path(tempfile.mkdtemp(prefix=f"pre-update-{revision[:12]}-{stamp}-", dir=paths.backups))
        try:
            service.stop()
            with closing(sqlite3.connect(paths.database.as_uri() + "?mode=ro", uri=True)) as source:
                with closing(sqlite3.connect(backup / "facturas.db")) as destination:
                    source.backup(destination)
                    if destination.execute("PRAGMA quick_check").fetchone() != ("ok",):
                        raise RuntimeError("la copia de SQLite no pasó quick_check")
            os.chmod(backup / "facturas.db", 0o600)
            for name, content in previous.items():
                atomic_write(backup / name, content, 0o600)
            for name in ("bot", "metricas", "REVISION"):
                atomic_write(paths.target / name, files[name], 0o644 if name == "REVISION" else 0o755)
            service.start()
            service.health(hashlib.sha256(files["bot"]).hexdigest())
        except Exception as error:
            # La base puede tener migraciones aditivas o nuevos datos. Nunca
            # restaurarla automáticamente sobre las facturas del usuario.
            try:
                service.stop()
                for name, content in previous.items():
                    atomic_write(paths.target / name, content, 0o644 if name == "REVISION" else 0o755)
                service.start()
                service.health(hashlib.sha256(previous["bot"]).hexdigest())
            except Exception as rollback_error:
                raise RuntimeError(f"despliegue y recuperación fallaron; respaldo: {backup}") from rollback_error
            reason = f"; comprobación: {error}" if isinstance(error, HealthError) else ""
            raise RuntimeError(f"despliegue falló{reason}; ejecutable anterior restaurado; respaldo: {backup}") from error
        return {"revision": revision, "backup": str(backup), "sha256": hashlib.sha256(files["bot"]).hexdigest()}


def atomic_write(path, content, mode):
    descriptor, temporary = tempfile.mkstemp(prefix=".deploy-", dir=path.parent)
    try:
        with os.fdopen(descriptor, "wb") as output:
            output.write(content)
            output.flush()
            os.fsync(output.fileno())
        os.chmod(temporary, mode)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


class Systemd:
    unit = "bot-marangatu.service"

    def stop(self):
        subprocess.run(["/usr/bin/systemctl", "stop", self.unit], check=True, timeout=45)

    def start(self):
        subprocess.run(["/usr/bin/systemctl", "start", self.unit], check=True, timeout=30)

    def state(self):
        output = subprocess.check_output(
            ["/usr/bin/systemctl", "show", self.unit, "-p", "ActiveState", "-p", "MainPID"],
            text=True, timeout=10,
        )
        return dict(line.split("=", 1) for line in output.splitlines())

    def health(self, expected_hash):
        stable = 0
        last_pid = None
        failure = "no se confirmó el inicio del bot"
        for _ in range(45):
            state = self.state()
            pid = state.get("MainPID", "0")
            status = state.get("ActiveState")
            if status not in ("active", "activating"):
                raise HealthError("el servicio no está activo")
            # Type=simple returns from start before execve. The PID may still
            # run systemd's pre-exec helper; wait, but never accept its hash.
            try:
                actual_hash = None
                if status == "active" and pid != "0":
                    with open(f"/proc/{int(pid)}/exe", "rb") as binary:
                        digest = hashlib.sha256()
                        for chunk in iter(lambda: binary.read(1024 * 1024), b""):
                            digest.update(chunk)
                        actual_hash = digest.hexdigest()
                failure = "el servicio no está activo" if actual_hash is None else "el proceso ejecuta una versión diferente"
            except (OSError, ValueError):
                failure = "no se pudo verificar el ejecutable activo"
            if actual_hash == expected_hash:
                logs = subprocess.check_output(
                    ["/usr/bin/journalctl", "-u", self.unit, f"_PID={int(pid)}", "-n", "20", "--no-pager", "-o", "cat"],
                    text=True, timeout=10,
                )
                failure = "no se confirmó el inicio del bot"
                stable = stable + 1 if pid == last_pid and "bot iniciado, esperando facturas" in logs else 0
                if stable >= 2:
                    return
                last_pid = pid
            else:
                stable = 0
                last_pid = None
            time.sleep(1)
        raise HealthError(failure)


def main():
    os.umask(0o077)
    paths = Paths()
    service = Systemd()
    if sys.argv[1:] == ["--status"]:
        state = service.state()
        state["revision"] = (paths.target / "REVISION").read_text().strip()
        print(json.dumps(state))
        return
    if sys.argv[1:] != ["--apply"] or os.geteuid() != 0:
        raise ValueError("operación no permitida")
    payload = sys.stdin.buffer.read(MAX_PACKAGE + 1)

    def interrupted(signum, frame):
        for event in (signal.SIGTERM, signal.SIGHUP, signal.SIGINT):
            signal.signal(event, signal.SIG_IGN)
        raise RuntimeError("despliegue interrumpido")

    for event in (signal.SIGTERM, signal.SIGHUP, signal.SIGINT):
        signal.signal(event, interrupted)
    print(json.dumps(deploy_release(payload, paths, service)))


def validate_package(payload):
    if len(payload) > MAX_PACKAGE:
        raise ValueError("release demasiado grande")
    files = {}
    try:
        with tarfile.open(fileobj=io.BytesIO(payload), mode="r:") as archive:
            for member in archive:
                if member.name not in RELEASE_FILES or member.name in files or not member.isfile():
                    raise ValueError("archivo no permitido en release")
                limit = 32 * 1024 * 1024 if member.name in ("bot", "metricas") else 1024
                if not 0 < member.size <= limit:
                    raise ValueError("tamaño de archivo inválido")
                files[member.name] = archive.extractfile(member).read()
    except tarfile.TarError as error:
        raise ValueError("paquete tar inválido") from error
    if files.keys() != RELEASE_FILES:
        raise ValueError("release incompleto")
    if not re.fullmatch(rb"[0-9a-f]{40}\n", files["REVISION"]):
        raise ValueError("revisión inválida")
    for name in ("bot", "metricas"):
        binary = files[name]
        if len(binary) < 20 or binary[:6] != b"\x7fELF\x02\x01" or binary[18:20] != b"\x3e\x00":
            raise ValueError("se requiere un ejecutable Linux amd64")
    try:
        entries = files["SHA256SUMS"].decode("ascii").splitlines()
    except UnicodeDecodeError as error:
        raise ValueError("manifest inválido") from error
    checked = set()
    for entry in entries:
        match = re.fullmatch(r"([0-9a-f]{64})  (bot|metricas|REVISION)", entry)
        if match is None or match[2] in checked:
            raise ValueError("manifest inválido")
        name = match[2]
        if hashlib.sha256(files[name]).hexdigest() != match[1]:
            raise ValueError("checksum incorrecto")
        checked.add(name)
    if checked != {"bot", "metricas", "REVISION"}:
        raise ValueError("manifest incompleto")
    return files


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(f"ERROR: {error}", file=sys.stderr)
        sys.exit(1)
