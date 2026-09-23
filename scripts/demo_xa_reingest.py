#!/usr/bin/env python3
"""Safely replay every XA demo study through every configured XA ingestion job.

The command is deliberately dry-run-first.  ``--execute`` is required before it
will stop jobs, write DICOMs, upload data, or delete a previous managed replay.
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
import re
import shlex
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from collections.abc import Callable, Iterable
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any
from zoneinfo import ZoneInfo

if sys.version_info < (3, 10):  # noqa: UP036 - operator-facing check for this standalone script
    raise SystemExit("demo_xa_reingest.py requires Python 3.10 or newer")

import pydicom
from pydicom.dataset import Dataset
from pydicom.multival import MultiValue
from pydicom.uid import generate_uid

SCHEMA_VERSION = 2
TERMINAL_FAILURES = {"failed", "skipped", "cancelled", "partial"}
UID_PATTERN = re.compile(r"^[0-9]+(?:\.[0-9]+)*$")
TENANT_PATTERN = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._:-]{0,254}$")
DATE_TIME_PAIRS = (
    ("StudyDate", "StudyTime"),
    ("SeriesDate", "SeriesTime"),
    ("AcquisitionDate", "AcquisitionTime"),
    ("ContentDate", "ContentTime"),
    ("PerformedProcedureStepStartDate", "PerformedProcedureStepStartTime"),
    ("PerformedProcedureStepEndDate", "PerformedProcedureStepEndTime"),
)
DATETIME_KEYWORDS = (
    "AcquisitionDateTime",
    "FrameAcquisitionDateTime",
    "RadiopharmaceuticalStartDateTime",
)
DEIDENTIFY_KEYWORDS = (
    "PatientName",
    "PatientBirthDate",
    "PatientBirthTime",
    "PatientAddress",
    "OtherPatientIDs",
    "OtherPatientNames",
    "MedicalRecordLocator",
    "EthnicGroup",
    "Occupation",
    "AdditionalPatientHistory",
    "AccessionNumber",
    "AdmissionID",
    "InstitutionName",
    "InstitutionAddress",
    "ReferringPhysicianName",
    "RequestingPhysician",
    "PerformingPhysicianName",
    "OperatorsName",
    "StudyID",
)


class ReingestionError(RuntimeError):
    """Expected operator-facing failure."""


def utc_now() -> datetime:
    return datetime.now(timezone.utc)


def iso_now() -> str:
    return utc_now().replace(microsecond=0).isoformat().replace("+00:00", "Z")


def log(message: str) -> None:
    print(f"[demo-xa] {message}", flush=True)


def load_env(path: Path) -> dict[str, str]:
    """Load a conservative KEY=VALUE env file without executing shell code."""
    if not path.exists():
        raise ReingestionError(f"configuration file not found: {path}")
    values: dict[str, str] = {}
    for number, raw_line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[7:].lstrip()
        if "=" not in line:
            raise ReingestionError(f"invalid configuration line {number} in {path.name}")
        key, raw_value = line.split("=", 1)
        key = key.strip()
        if not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", key):
            raise ReingestionError(f"invalid configuration key on line {number}")
        try:
            parsed = shlex.split(raw_value, comments=True, posix=True)
        except ValueError as exc:
            raise ReingestionError(f"invalid configuration line {number}: {exc}") from exc
        values[key] = " ".join(parsed) if parsed else ""
    return values


def env_value(values: dict[str, str], name: str, default: str = "") -> str:
    return os.environ.get(name, values.get(name, default)).strip()


def require(values: dict[str, str], name: str) -> str:
    value = env_value(values, name)
    if not value:
        raise ReingestionError(f"{name} must be set in the environment or config file")
    return value


@dataclass(frozen=True)
class Config:
    api_base_url: str
    tenant_id: str
    admin_email: str
    admin_password: str
    turnstile_token: str
    source_orthanc_url: str
    source_orthanc_user: str
    source_orthanc_password: str
    destination_orthanc_url: str
    destination_orthanc_user: str
    destination_orthanc_password: str
    study_service_url: str
    study_service_token: str
    work_dir: Path
    timezone_name: str
    poll_seconds: float
    drain_timeout_seconds: int
    processing_timeout_seconds: int
    stable_timeout_seconds: int
    compose_file: Path
    patient_id_prefix: str

    @classmethod
    def from_values(cls, values: dict[str, str], repo_root: Path) -> Config:
        api_url = require(values, "API_BASE_URL").rstrip("/")
        if not api_url.startswith("https://") and not re.match(
            r"^http://(?:localhost|127\.0\.0\.1)(?::[0-9]+)?(?:/|$)", api_url
        ):
            raise ReingestionError("API_BASE_URL must use HTTPS unless it targets loopback")
        return cls(
            api_base_url=api_url,
            tenant_id=require(values, "TENANT_ID"),
            admin_email=require(values, "PACS_ADMIN_EMAIL"),
            admin_password=require(values, "PACS_ADMIN_PASSWORD"),
            turnstile_token=env_value(values, "PACS_LOGIN_TURNSTILE_TOKEN"),
            source_orthanc_url=require(values, "DEMO_XA_SOURCE_ORTHANC_URL").rstrip("/"),
            source_orthanc_user=env_value(values, "DEMO_XA_SOURCE_ORTHANC_USER"),
            source_orthanc_password=env_value(values, "DEMO_XA_SOURCE_ORTHANC_PASSWORD"),
            destination_orthanc_url=env_value(
                values, "DEMO_XA_DESTINATION_ORTHANC_URL", "http://127.0.0.1:8042"
            ).rstrip("/"),
            destination_orthanc_user=env_value(values, "DEMO_XA_DESTINATION_ORTHANC_USER"),
            destination_orthanc_password=env_value(
                values, "DEMO_XA_DESTINATION_ORTHANC_PASSWORD"
            ),
            study_service_url=env_value(
                values, "DEMO_XA_STUDY_SERVICE_URL", "http://127.0.0.1:8600"
            ).rstrip("/"),
            study_service_token=env_value(values, "STUDY_SERVICE_OPERATOR_TOKEN"),
            work_dir=Path(
                env_value(values, "DEMO_XA_WORK_DIR", str(repo_root / ".demo-xa-runs"))
            ).expanduser().resolve(),
            timezone_name=env_value(values, "APP_TIMEZONE", "America/Toronto"),
            poll_seconds=float(env_value(values, "DEMO_XA_POLL_SECONDS", "10")),
            drain_timeout_seconds=int(env_value(values, "DEMO_XA_DRAIN_TIMEOUT_SECONDS", "300")),
            processing_timeout_seconds=int(
                env_value(values, "DEMO_XA_PROCESSING_TIMEOUT_SECONDS", "3600")
            ),
            stable_timeout_seconds=int(env_value(values, "DEMO_XA_STABLE_TIMEOUT_SECONDS", "300")),
            compose_file=Path(
                env_value(values, "DEMO_XA_COMPOSE_FILE", str(repo_root / "docker-compose.yml"))
            ).expanduser().resolve(),
            patient_id_prefix=env_value(values, "DEMO_XA_PATIENT_ID_PREFIX", "DEMO-XA"),
        )


class JsonHttpClient:
    def __init__(
        self,
        base_url: str,
        *,
        username: str = "",
        password: str = "",
        headers: dict[str, str] | None = None,
        timeout: int = 30,
    ) -> None:
        self.base_url = base_url.rstrip("/")
        self.timeout = timeout
        self.headers = dict(headers or {})
        if username or password:
            credentials = base64.b64encode(f"{username}:{password}".encode()).decode()
            self.headers["Authorization"] = f"Basic {credentials}"

    def request(
        self,
        method: str,
        path: str,
        *,
        body: Any | None = None,
        raw_body: bytes | None = None,
        content_type: str = "application/json",
        expected: Iterable[int] = (200,),
    ) -> Any:
        url = f"{self.base_url}{path}"
        headers = {"Accept": "application/json", **self.headers}
        data = raw_body
        if body is not None:
            data = json.dumps(body).encode("utf-8")
        if data is not None:
            headers["Content-Type"] = content_type
        request = urllib.request.Request(url, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(request, timeout=self.timeout) as response:
                payload = response.read()
                status = response.status
        except urllib.error.HTTPError as exc:
            payload = exc.read()
            status = exc.code
        except urllib.error.URLError as exc:
            raise ReingestionError(f"cannot reach {urllib.parse.urlsplit(url).netloc}: {exc.reason}") from exc
        if status not in set(expected):
            detail = ""
            try:
                parsed = json.loads(payload) if payload else {}
                detail = parsed.get("message") or parsed.get("detail") or ""
            except (json.JSONDecodeError, AttributeError):
                pass
            suffix = f": {detail}" if detail else ""
            raise ReingestionError(f"{method} {path} returned HTTP {status}{suffix}")
        if not payload:
            return None
        try:
            return json.loads(payload)
        except json.JSONDecodeError as exc:
            raise ReingestionError(f"{method} {path} returned invalid JSON") from exc

    def request_bytes(self, path: str) -> bytes:
        url = f"{self.base_url}{path}"
        request = urllib.request.Request(
            url, headers={"Accept": "application/dicom", **self.headers}, method="GET"
        )
        try:
            with urllib.request.urlopen(request, timeout=self.timeout) as response:
                payload = response.read()
                status = response.status
        except urllib.error.HTTPError as exc:
            payload = exc.read()
            status = exc.code
        except urllib.error.URLError as exc:
            raise ReingestionError(
                f"cannot reach {urllib.parse.urlsplit(url).netloc}: {exc.reason}"
            ) from exc
        if status != 200:
            raise ReingestionError(f"GET {path} returned HTTP {status}")
        return payload


class ApiPacsClient:
    def __init__(self, config: Config) -> None:
        self.http = JsonHttpClient(config.api_base_url)
        self.config = config

    def login(self) -> None:
        body: dict[str, str] = {
            "tenantId": self.config.tenant_id,
            "email": self.config.admin_email,
            "password": self.config.admin_password,
        }
        if self.config.turnstile_token:
            body["turnstileToken"] = self.config.turnstile_token
        response = self.http.request("POST", "/v1/iam/login", body=body, expected=(200, 201))
        if not response.get("success") or not response.get("data", {}).get("sessionToken"):
            raise ReingestionError(f"API login failed: {response.get('message', 'no session token')}")
        self.http.headers["Authorization"] = f"Bearer {response['data']['sessionToken']}"

    def _data(self, method: str, path: str, **kwargs: Any) -> Any:
        response = self.http.request(method, path, **kwargs)
        if not isinstance(response, dict) or not response.get("success"):
            message = response.get("message", "unexpected API response") if isinstance(response, dict) else "unexpected API response"
            raise ReingestionError(f"API operation failed: {message}")
        return response.get("data")

    def ingestion_jobs(self) -> list[dict[str, Any]]:
        return self._data("GET", "/v1/inference/ingestion/jobs") or []

    def set_job_running(self, job_id: str, running: bool) -> None:
        action = "start" if running else "stop"
        self._data("POST", f"/v1/inference/ingestion/job/{urllib.parse.quote(job_id)}/{action}")

    def candidate(self, job_id: str, study_uid: str) -> dict[str, Any] | None:
        path = (
            f"/v1/inference/ingestion/job/{urllib.parse.quote(job_id)}/study/"
            f"{urllib.parse.quote(study_uid, safe='')}"
        )
        candidates = self._data("GET", path) or []
        return candidates[0] if candidates else None

    def check_model(self, container_id: str) -> None:
        self._data(
            "GET",
            f"/v1/inference/model/proxy/container/{urllib.parse.quote(container_id)}/info",
        )


@dataclass(frozen=True)
class XaStudy:
    orthanc_id: str
    study_uid: str
    series_count: int
    instance_ids: tuple[str, ...]

    @property
    def instance_count(self) -> int:
        return len(self.instance_ids)


class OrthancClient:
    def __init__(self, url: str, username: str = "", password: str = "") -> None:
        self.http = JsonHttpClient(url, username=username, password=password, timeout=60)

    def check(self) -> None:
        self.http.request("GET", "/system")

    def find_studies(self, study_uid: str) -> list[str]:
        result = self.http.request(
            "POST",
            "/tools/find",
            body={"Level": "Study", "Query": {"StudyInstanceUID": study_uid}},
        )
        return [str(item) for item in result or []]

    def upload(self, payload: bytes) -> dict[str, Any]:
        return self.http.request(
            "POST",
            "/instances",
            raw_body=payload,
            content_type="application/dicom",
            expected=(200, 201),
        )

    def study(self, orthanc_id: str) -> dict[str, Any]:
        return self.http.request("GET", f"/studies/{urllib.parse.quote(orthanc_id)}")

    def series(self, orthanc_id: str) -> dict[str, Any]:
        return self.http.request("GET", f"/series/{urllib.parse.quote(orthanc_id)}")

    def xa_studies(self) -> tuple[XaStudy, ...]:
        studies: list[XaStudy] = []
        study_ids = self.http.request("GET", "/studies") or []
        for orthanc_id_value in study_ids:
            orthanc_id = str(orthanc_id_value)
            study = self.study(orthanc_id)
            study_uid = str(study.get("MainDicomTags", {}).get("StudyInstanceUID", "")).strip()
            all_series: list[dict[str, Any]] = []
            has_xa = False
            for series_id_value in study.get("Series", []):
                series = self.series(str(series_id_value))
                modality = str(series.get("MainDicomTags", {}).get("Modality", "")).upper()
                if modality == "XA":
                    has_xa = True
                all_series.append(series)
            if not has_xa:
                continue
            if not UID_PATTERN.fullmatch(study_uid):
                raise ReingestionError(
                    "Orthanc study containing XA has no valid StudyInstanceUID"
                )
            instance_ids = tuple(
                str(instance_id)
                for series in all_series
                for instance_id in series.get("Instances", [])
            )
            if not instance_ids or not all_series:
                raise ReingestionError("Orthanc study containing XA has no instances")
            studies.append(
                XaStudy(
                    orthanc_id=orthanc_id,
                    study_uid=study_uid,
                    series_count=len(all_series),
                    instance_ids=instance_ids,
                )
            )
        if not studies:
            raise ReingestionError("Orthanc contains no XA studies")
        return tuple(sorted(studies, key=lambda item: item.study_uid))

    def download_study(self, study: XaStudy, target_dir: Path) -> StudyInventory:
        target_dir.mkdir(parents=True, exist_ok=False)
        for index, instance_id in enumerate(study.instance_ids, 1):
            payload = self.http.request_bytes(
                f"/instances/{urllib.parse.quote(instance_id)}/file"
            )
            (target_dir / f"instance-{index:04d}.dcm").write_bytes(payload)
        inventory = inspect_study_files(target_dir)
        if inventory.study_uid != study.study_uid:
            raise ReingestionError("Orthanc study changed during snapshot")
        if inventory.instance_count != study.instance_count:
            raise ReingestionError("Orthanc instance count changed during snapshot")
        if inventory.series_count != study.series_count:
            raise ReingestionError("Orthanc series count changed during snapshot")
        return inventory

    def delete_study(self, orthanc_id: str) -> None:
        self.http.request("DELETE", f"/studies/{urllib.parse.quote(orthanc_id)}", expected=(200, 204))


class StudyServiceClient:
    def __init__(self, config: Config) -> None:
        headers = {"X-Tenant-ID": config.tenant_id}
        if config.study_service_token:
            headers["Authorization"] = f"Bearer {config.study_service_token}"
        self.http = JsonHttpClient(config.study_service_url, headers=headers)

    def check(self) -> None:
        self.http.request("GET", "/health/detailed")

    def jobs_by_candidate(self, candidate_id: str) -> list[dict[str, Any]]:
        response = self.http.request(
            "GET", f"/jobs/by-candidate/{urllib.parse.quote(candidate_id)}?page_size=250"
        )
        return response.get("jobs", [])

    def job(self, job_id: str) -> dict[str, Any]:
        return self.http.request("GET", f"/jobs/{urllib.parse.quote(job_id)}")


class ComposeRuntime:
    """Read Celery routing state and perform exact-UID database cleanup."""

    def __init__(self, compose_file: Path, runner: Callable[..., subprocess.CompletedProcess[str]] = subprocess.run) -> None:
        self.compose_file = compose_file
        self.runner = runner

    def _compose(self, args: list[str], *, timeout: int = 45) -> str:
        command = ["docker", "compose", "-f", str(self.compose_file), *args]
        try:
            completed = self.runner(
                command,
                check=True,
                capture_output=True,
                text=True,
                timeout=timeout,
            )
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired, FileNotFoundError) as exc:
            detail = getattr(exc, "stderr", "") or str(exc)
            raise ReingestionError(f"Docker Compose check failed: {detail.strip()}") from exc
        return completed.stdout

    def snapshot(self) -> dict[str, Any]:
        code = """
import json, os
from src.config.settings import MODEL_CONTAINERS
from src.workers.celery_app import app as celery_app
inspect = celery_app.control.inspect(timeout=5)
queues = {q for cfg in MODEL_CONTAINERS.values() for q in [cfg.get('queue')] if q}
depths = {}
try:
    import redis
    broker = os.environ.get('CELERY_BROKER_URL') or os.environ.get('REDIS_URL')
    client = redis.Redis.from_url(broker)
    depths = {q: int(client.llen(q)) for q in queues}
except Exception as exc:
    depths = {'__error__': type(exc).__name__}
payload = {
    'registry': MODEL_CONTAINERS,
    'active_queues': inspect.active_queues() or {},
    'active': inspect.active() or {},
    'reserved': inspect.reserved() or {},
    'scheduled': inspect.scheduled() or {},
    'queue_depths': depths,
}
print('DEMO_XA_JSON=' + json.dumps(payload, default=str))
""".strip()
        output = self._compose(["exec", "-T", "study-service", "uv", "run", "python", "-c", code])
        for line in reversed(output.splitlines()):
            if line.startswith("DEMO_XA_JSON="):
                return json.loads(line.split("=", 1)[1])
        raise ReingestionError("study-service runtime check produced no structured result")

    @staticmethod
    def _task_queue(task: dict[str, Any]) -> str:
        request = task.get("request") if isinstance(task.get("request"), dict) else task
        delivery = request.get("delivery_info", {}) if isinstance(request, dict) else {}
        return str(delivery.get("routing_key") or delivery.get("exchange") or "")

    @classmethod
    def busy_queues(cls, snapshot: dict[str, Any], queues: set[str]) -> set[str]:
        busy = {
            queue
            for queue in queues
            if int(snapshot.get("queue_depths", {}).get(queue, 0) or 0) > 0
        }
        for category in ("active", "reserved", "scheduled"):
            for tasks in snapshot.get(category, {}).values():
                for task in tasks or []:
                    queue = cls._task_queue(task)
                    if queue in queues:
                        busy.add(queue)
        return busy

    def cleanup_database_rows(self, study_uid: str, tenant_id: str) -> None:
        if not UID_PATTERN.fullmatch(study_uid):
            raise ReingestionError("refusing database cleanup for an invalid DICOM UID")
        if not TENANT_PATTERN.fullmatch(tenant_id):
            raise ReingestionError("refusing database cleanup for an invalid tenant ID")
        study_sql = (
            "BEGIN; "
            "DELETE FROM go_callback_dead_letters WHERE job_id IN "
            f"(SELECT job_id FROM pipeline_jobs WHERE study_instance_uid='{study_uid}' "
            f"AND tenant_id='{tenant_id}'); "
            f"DELETE FROM pipeline_jobs WHERE study_instance_uid='{study_uid}' "
            f"AND tenant_id='{tenant_id}'; "
            "COMMIT;"
        )
        inference_sql = (
            "BEGIN; "
            f"DELETE FROM ingestion_processing_runs WHERE study_instance_uid='{study_uid}' "
            f"AND tenant_id='{tenant_id}'; "
            f"DELETE FROM ingestion_candidates WHERE study_instance_uid='{study_uid}' "
            f"AND tenant_id='{tenant_id}'; "
            "COMMIT;"
        )
        self._compose(
            [
                "exec", "-T", "postgres", "sh", "-lc",
                'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" '
                '-c "$1"',
                "demo-xa-cleanup", study_sql,
            ]
        )
        self._compose(
            [
                "exec", "-T", "postgresql", "sh", "-lc",
                'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" '
                '-c "$1"',
                "demo-xa-cleanup", inference_sql,
            ]
        )


@dataclass(frozen=True)
class StudyInventory:
    files: tuple[Path, ...]
    study_uid: str
    series_count: int
    instance_count: int
    latest_timestamp: datetime


def parse_dicom_datetime(date_value: Any, time_value: Any) -> datetime | None:
    date_text = str(date_value or "").strip()
    time_text = str(time_value or "").strip()
    if not re.fullmatch(r"\d{8}", date_text):
        return None
    match = re.match(r"^(\d{2})(\d{2})(\d{2})(?:\.(\d{1,6}))?", time_text)
    if not match:
        return None
    fraction = (match.group(4) or "").ljust(6, "0")
    try:
        return datetime.strptime(  # noqa: DTZ007 - DICOM DA/TM has no timezone component
            f"{date_text}{match.group(1)}{match.group(2)}{match.group(3)}{fraction}",
            "%Y%m%d%H%M%S%f",
        )
    except ValueError:
        return None


def format_dicom_time(value: datetime) -> str:
    return value.strftime("%H%M%S.%f").rstrip("0").rstrip(".")


def parse_dicom_dt(value: Any) -> datetime | None:
    text = str(value or "").strip()
    match = re.match(r"^(\d{8})(\d{2})(\d{2})(\d{2})(?:\.(\d{1,6}))?", text)
    if not match:
        return None
    fraction = (match.group(5) or "").ljust(6, "0")
    try:
        return datetime.strptime(  # noqa: DTZ007 - DICOM DT timezone suffix is not rewritten
            "".join(match.groups()[:4]) + fraction, "%Y%m%d%H%M%S%f"
        )
    except ValueError:
        return None


def inspect_study_files(study_dir: Path) -> StudyInventory:
    if not study_dir.is_dir():
        raise ReingestionError(f"study snapshot directory does not exist: {study_dir}")
    files = tuple(sorted(path for path in study_dir.rglob("*") if path.is_file()))
    dicom_files: list[Path] = []
    study_uids: set[str] = set()
    series_uids: set[str] = set()
    modalities: set[str] = set()
    timestamps: list[datetime] = []
    for path in files:
        try:
            dataset = pydicom.dcmread(path, stop_before_pixels=True, force=False)
        except Exception:
            continue
        modality = str(dataset.get("Modality", "")).upper()
        modalities.add(modality or "UNKNOWN")
        study_uid = str(dataset.get("StudyInstanceUID", "")).strip()
        series_uid = str(dataset.get("SeriesInstanceUID", "")).strip()
        sop_uid = str(dataset.get("SOPInstanceUID", "")).strip()
        if not study_uid or not series_uid or not sop_uid:
            raise ReingestionError(
                "study snapshot DICOM is missing a required study, series, or SOP UID"
            )
        dicom_files.append(path)
        study_uids.add(study_uid)
        series_uids.add(series_uid)
        for date_keyword, time_keyword in DATE_TIME_PAIRS:
            parsed = parse_dicom_datetime(dataset.get(date_keyword), dataset.get(time_keyword))
            if parsed:
                timestamps.append(parsed)
    if not dicom_files:
        raise ReingestionError("study snapshot contains no readable DICOM instances")
    if len(study_uids) != 1:
        raise ReingestionError("study snapshot must contain exactly one DICOM study")
    if "XA" not in modalities:
        raise ReingestionError("study snapshot no longer contains an XA series")
    latest = (
        max(timestamps)
        if timestamps
        else datetime.now(timezone.utc).replace(tzinfo=None, microsecond=0)
    )
    return StudyInventory(
        tuple(dicom_files),
        next(iter(study_uids)),
        len(series_uids),
        len(dicom_files),
        latest,
    )


def _replace_uid_value(value: Any, mapping: dict[str, str]) -> Any:
    if isinstance(value, (list, tuple, MultiValue)):
        return [mapping.get(str(item), str(item)) for item in value]
    return mapping.get(str(value), str(value))


def rewrite_dataset(
    dataset: Dataset,
    uid_mapping: dict[str, str],
    shift: timedelta,
    patient_id: str,
    discovery_time: datetime,
) -> None:
    for element in dataset.iterall():
        if element.VR == "UI":
            element.value = _replace_uid_value(element.value, uid_mapping)
    for date_keyword, time_keyword in DATE_TIME_PAIRS:
        parsed = parse_dicom_datetime(dataset.get(date_keyword), dataset.get(time_keyword))
        if parsed:
            shifted = parsed + shift
            setattr(dataset, date_keyword, shifted.strftime("%Y%m%d"))
            setattr(dataset, time_keyword, format_dicom_time(shifted))
    for keyword in DATETIME_KEYWORDS:
        parsed = parse_dicom_dt(dataset.get(keyword))
        if parsed:
            setattr(dataset, keyword, (parsed + shift).strftime("%Y%m%d%H%M%S.%f").rstrip("0"))
    # PACS_QUERY discovery is constrained by StudyDate/StudyTime. Some valid XA
    # studies only carry content-level timestamps, so shifting existing values
    # is insufficient: missing study-level values make the replay invisible to
    # C-FIND. Stamp every instance consistently at the intended replay time.
    dataset.StudyDate = discovery_time.strftime("%Y%m%d")
    dataset.StudyTime = format_dicom_time(discovery_time)
    dataset.PatientID = patient_id
    for keyword in DEIDENTIFY_KEYWORDS:
        if keyword in dataset:
            setattr(dataset, keyword, "")
    dataset.PatientIdentityRemoved = "YES"
    dataset.DeidentificationMethod = "PACS-AI controlled XA demo replay"
    if getattr(dataset, "file_meta", None) and "MediaStorageSOPInstanceUID" in dataset.file_meta:
        old = str(dataset.file_meta.MediaStorageSOPInstanceUID)
        dataset.file_meta.MediaStorageSOPInstanceUID = uid_mapping.get(old, old)


def build_replay(
    inventory: StudyInventory,
    output_dir: Path,
    *,
    target_time: datetime,
    patient_id: str,
) -> dict[str, Any]:
    datasets: list[tuple[Path, Dataset, str]] = []
    study_uids: set[str] = set()
    series_uids: set[str] = set()
    sop_uids: set[str] = set()
    for path in inventory.files:
        dataset = pydicom.dcmread(path, force=False)
        pixel_hash = hashlib.sha256(bytes(dataset.get("PixelData", b""))).hexdigest()
        datasets.append((path, dataset, pixel_hash))
        study_uids.add(str(dataset.StudyInstanceUID))
        series_uids.add(str(dataset.SeriesInstanceUID))
        sop_uids.add(str(dataset.SOPInstanceUID))
    uid_mapping = {uid: generate_uid() for uid in study_uids | series_uids | sop_uids}
    shift = target_time.replace(tzinfo=None) - inventory.latest_timestamp
    output_dir.mkdir(parents=True, exist_ok=False)
    outputs: list[str] = []
    for index, (source_path, dataset, pixel_hash) in enumerate(datasets, 1):
        rewrite_dataset(dataset, uid_mapping, shift, patient_id, target_time)
        if hashlib.sha256(bytes(dataset.get("PixelData", b""))).hexdigest() != pixel_hash:
            raise ReingestionError("pixel-data invariant failed during DICOM rewrite")
        suffix = source_path.suffix if source_path.suffix else ".dcm"
        target_path = output_dir / f"instance-{index:04d}{suffix}"
        dataset.save_as(target_path, enforce_file_format=True)
        outputs.append(target_path.name)
    return {
        "study_uid": uid_mapping[inventory.study_uid],
        "uid_mapping": uid_mapping,
        "files": outputs,
        "shift_seconds": int(shift.total_seconds()),
    }


def xa_jobs(jobs: list[dict[str, Any]]) -> list[dict[str, Any]]:
    return [
        job
        for job in jobs
        if any(str(modality).upper() == "XA" for modality in job.get("modalities", []))
    ]


def validate_active_job_windows(jobs: list[dict[str, Any]], now: datetime) -> None:
    """Fail early when a running job cannot discover a study stamped now."""
    failures: list[str] = []
    unix_now = int(now.timestamp())
    wall_time = now.strftime("%H%M%S")
    for job in jobs:
        if str(job.get("status", "")).upper() != "RUNNING":
            continue
        label = f"{job.get('modelName')} {job.get('modelVersion')}"
        start_timestamp = int(job.get("scheduleStartTimestamp") or 0)
        end_timestamp = int(job.get("scheduleEndTimestamp") or 0)
        if start_timestamp and unix_now < start_timestamp:
            failures.append(f"{label}: schedule has not started")
        if end_timestamp and unix_now > end_timestamp:
            failures.append(f"{label}: schedule has ended")
        start_time = str(job.get("studyTimeStart") or "000000")
        end_time = str(job.get("studyTimeEnd") or "235959")
        if not start_time <= wall_time <= end_time:
            failures.append(f"{label}: current local time is outside its study-time filter")
        stability = int(job.get("stabilityMinutes") or 0)
        recent_window = int(job.get("recentWindowMinutes") or 0)
        if recent_window and stability >= recent_window:
            failures.append(f"{label}: stability window is not smaller than recent window")
    if failures:
        raise ReingestionError("XA active-window preflight failed:\n  - " + "\n  - ".join(failures))


def worker_queues(snapshot: dict[str, Any]) -> set[str]:
    return {
        str(queue.get("name"))
        for queues in snapshot.get("active_queues", {}).values()
        for queue in queues or []
        if queue.get("name")
    }


def validate_routes(jobs: list[dict[str, Any]], snapshot: dict[str, Any]) -> dict[str, str]:
    registry = snapshot.get("registry", {})
    live_queues = worker_queues(snapshot)
    routes: dict[str, str] = {}
    failures: list[str] = []
    for job in jobs:
        name = str(job.get("modelName", ""))
        version = str(job.get("modelVersion", ""))
        route = registry.get(name)
        if not route:
            failures.append(f"{name} {version}: exact model name is not routable")
            continue
        if str(route.get("version")) != version:
            failures.append(
                f"{name} {version}: registry version is {route.get('version', 'missing')}"
            )
        queue = str(route.get("queue", "default"))
        if queue not in live_queues:
            failures.append(f"{name} {version}: no live Celery worker consumes queue {queue}")
        routes[str(job["id"])] = queue
    if failures:
        raise ReingestionError("XA routing preflight failed:\n  - " + "\n  - ".join(failures))
    return routes


def write_json(path: Path, value: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    os.chmod(temporary, 0o600)
    temporary.replace(path)


class DemoXaReingestion:
    def __init__(
        self,
        config: Config,
        *,
        execute: bool,
        prune_previous: bool,
        allow_database_cleanup: bool,
        api: ApiPacsClient | None = None,
        source: OrthancClient | None = None,
        destination: OrthancClient | None = None,
        study: StudyServiceClient | None = None,
        runtime: ComposeRuntime | None = None,
        sleeper: Callable[[float], None] = time.sleep,
    ) -> None:
        self.config = config
        self.execute = execute
        self.prune_previous = prune_previous
        self.allow_database_cleanup = allow_database_cleanup
        self.api = api or ApiPacsClient(config)
        self.source = source or OrthancClient(
            config.source_orthanc_url, config.source_orthanc_user, config.source_orthanc_password
        )
        self.destination = destination or OrthancClient(
            config.destination_orthanc_url,
            config.destination_orthanc_user,
            config.destination_orthanc_password,
        )
        self.study = study or StudyServiceClient(config)
        self.runtime = runtime or ComposeRuntime(config.compose_file)
        self.sleep = sleeper
        self.manifest: dict[str, Any] = {}

    @property
    def latest_path(self) -> Path:
        return self.config.work_dir / "latest-success.json"

    def _preflight(
        self,
    ) -> tuple[list[dict[str, Any]], dict[str, str], tuple[XaStudy, ...]]:
        log("Authenticating and running read-only preflight checks")
        self.api.login()
        jobs = xa_jobs(self.api.ingestion_jobs())
        if not jobs:
            raise ReingestionError("no XA ingestion jobs are registered for this tenant")
        if not any(str(job.get("status", "")).upper() == "RUNNING" for job in jobs):
            raise ReingestionError("no XA ingestion jobs are running for this tenant")
        validate_active_job_windows(jobs, datetime.now(ZoneInfo(self.config.timezone_name)))
        snapshot = self.runtime.snapshot()
        routes = validate_routes(jobs, snapshot)
        self.source.check()
        self.destination.check()
        self.study.check()
        for job in jobs:
            self.api.check_model(str(job["containerId"]))
        current_studies = self.source.xa_studies()
        log(
            f"Preflight passed: {len(jobs)} XA models, {len(current_studies)} studies, "
            f"{sum(item.instance_count for item in current_studies)} instances, "
            f"{sum(item.series_count for item in current_studies)} series"
        )
        return jobs, routes, current_studies

    def _wait_for_idle(self, queues: set[str]) -> None:
        deadline = time.monotonic() + self.config.drain_timeout_seconds
        while True:
            busy = self.runtime.busy_queues(self.runtime.snapshot(), queues)
            if not busy:
                return
            if time.monotonic() >= deadline:
                raise ReingestionError("timed out waiting for XA Celery queues to drain")
            log(f"Waiting for {len(busy)} XA queue(s) to drain")
            self.sleep(self.config.poll_seconds)

    def _pause(self, jobs: list[dict[str, Any]], routes: dict[str, str]) -> list[str]:
        running = [str(job["id"]) for job in jobs if str(job.get("status", "")).upper() == "RUNNING"]
        for job_id in running:
            self.api.set_job_running(job_id, False)
        if running:
            log(f"Paused {len(running)} running XA discovery job(s)")
        self._wait_for_idle({routes[job_id] for job_id in running})
        return running

    def _restore(self, running_job_ids: list[str]) -> None:
        errors: list[str] = []
        for job_id in running_job_ids:
            try:
                self.api.set_job_running(job_id, True)
            except Exception as exc:
                errors.append(str(exc))
        if errors:
            raise ReingestionError(
                f"CRITICAL: failed to restore {len(errors)} XA ingestion job(s): {errors[0]}"
            )
        if running_job_ids:
            log(f"Restored {len(running_job_ids)} XA discovery job(s) to RUNNING")

    def _upload(self, dicom_dir: Path, file_names: list[str]) -> str:
        orthanc_study_ids: set[str] = set()
        for name in file_names:
            response = self.source.upload((dicom_dir / name).read_bytes())
            parent = str(response.get("ParentStudy") or "")
            if parent:
                orthanc_study_ids.add(parent)
        if len(orthanc_study_ids) != 1:
            raise ReingestionError("Orthanc upload did not produce exactly one study")
        return next(iter(orthanc_study_ids))

    def _wait_for_uploaded_studies(self, expected_counts: dict[str, int]) -> None:
        pending = dict(expected_counts)
        deadline = time.monotonic() + self.config.stable_timeout_seconds
        while pending:
            for orthanc_id, expected_count in tuple(pending.items()):
                study = self.source.study(orthanc_id)
                actual_count = len(study.get("Instances", []))
                if bool(study.get("IsStable")) and (
                    actual_count == expected_count or not actual_count
                ):
                    del pending[orthanc_id]
            if not pending:
                return
            if time.monotonic() >= deadline:
                raise ReingestionError(
                    f"timed out waiting for {len(pending)} uploaded XA study/studies to become stable"
                )
            self.sleep(self.config.poll_seconds)

    def _monitor(
        self, jobs: list[dict[str, Any]], study_uids: list[str]
    ) -> list[dict[str, Any]]:
        expected_jobs = {
            str(job["id"]): job
            for job in jobs
            if str(job.get("status", "")).upper() == "RUNNING"
        }
        if not expected_jobs:
            raise ReingestionError("no XA ingestion jobs were running before the replay")
        if not study_uids:
            raise ReingestionError("no replayed XA studies to monitor")
        completed: dict[str, dict[str, Any]] = {study_uid: {} for study_uid in study_uids}
        expected_total = len(expected_jobs) * len(study_uids)
        deadline = time.monotonic() + self.config.processing_timeout_seconds
        while sum(len(results) for results in completed.values()) < expected_total:
            for study_uid, study_results in completed.items():
                for job_id, ingestion_job in expected_jobs.items():
                    if job_id in study_results:
                        continue
                    candidate = self.api.candidate(job_id, study_uid)
                    if not candidate:
                        continue
                    if str(candidate.get("status", "")).upper() == "FAILED":
                        raise ReingestionError(
                            f"{ingestion_job['modelName']} {ingestion_job['modelVersion']} "
                            "retrieval failed"
                        )
                    candidate_status = str(candidate.get("processingStatus", "")).lower()
                    if candidate_status in TERMINAL_FAILURES:
                        raise ReingestionError(
                            f"{ingestion_job['modelName']} {ingestion_job['modelVersion']} ended as "
                            f"{candidate_status or candidate.get('status', 'failed')}"
                        )
                    pipeline_jobs = self.study.jobs_by_candidate(str(candidate["id"]))
                    matching = [
                        item
                        for item in pipeline_jobs
                        if item.get("model_name") == ingestion_job.get("modelName")
                        and str(item.get("model_version") or "")
                        == str(ingestion_job.get("modelVersion") or "")
                    ]
                    if not matching:
                        continue
                    newest = matching[0]
                    status = str(newest.get("status", "")).lower()
                    if status in TERMINAL_FAILURES:
                        raise ReingestionError(
                            f"{ingestion_job['modelName']} {ingestion_job['modelVersion']} "
                            f"pipeline ended as {status}"
                        )
                    if status == "completed":
                        detail = self.study.job(str(newest["job_id"]))
                        if detail.get("result_json") is None:
                            raise ReingestionError(
                                f"{ingestion_job['modelName']} completed without a stored result"
                            )
                        study_results[job_id] = {
                            "model_name": ingestion_job["modelName"],
                            "model_version": ingestion_job["modelVersion"],
                            "candidate_id": candidate["id"],
                            "pipeline_job_id": newest["job_id"],
                            "status": "completed",
                        }
                        completed_count = sum(len(results) for results in completed.values())
                        log(f"Completed {completed_count}/{expected_total} XA model result(s)")
            if sum(len(results) for results in completed.values()) == expected_total:
                break
            if time.monotonic() >= deadline:
                completed_count = sum(len(results) for results in completed.values())
                raise ReingestionError(
                    f"timed out waiting for {expected_total - completed_count} XA model-study result(s)"
                )
            self.sleep(self.config.poll_seconds)
        return [
            {"study_uid": study_uid, "models": completed[study_uid]}
            for study_uid in study_uids
        ]

    def _cleanup_previous(self, study_uid: str) -> dict[str, Any]:
        result: dict[str, Any] = {"study_uid": study_uid, "source_studies": 0, "destination_studies": 0}
        for label, client in (("source_studies", self.source), ("destination_studies", self.destination)):
            ids = client.find_studies(study_uid)
            for orthanc_id in ids:
                client.delete_study(orthanc_id)
            result[label] = len(ids)
        if self.allow_database_cleanup:
            self.runtime.cleanup_database_rows(study_uid, self.config.tenant_id)
            result["database_rows"] = "deleted_by_exact_study_uid_and_tenant"
        else:
            result["database_rows"] = "retained"
        return result

    def run(self) -> int:
        jobs, routes, current_studies = self._preflight()
        running_count = sum(
            1 for job in jobs if str(job.get("status", "")).upper() == "RUNNING"
        )
        plan = {
            "mode": "execute" if self.execute else "dry-run",
            "xa_models": [f"{job['modelName']} {job['modelVersion']}" for job in jobs],
            "running_jobs_to_pause": running_count,
            "current_xa_studies": len(current_studies),
            "current_xa_instances": sum(item.instance_count for item in current_studies),
            "current_xa_series": sum(item.series_count for item in current_studies),
            "expected_model_results": running_count * len(current_studies),
            "cleanup_requested": self.prune_previous,
            "database_cleanup_requested": self.allow_database_cleanup,
            "cleanup_target_source": "PACS_QUERY source Orthanc XA snapshot",
        }
        print(json.dumps(plan, indent=2))
        if not self.execute:
            log("Dry run complete; no jobs, DICOMs, PACS data, or database rows were changed")
            return 0

        run_id = utc_now().strftime("%Y%m%dT%H%M%SZ") + "-" + uuid.uuid4().hex[:8]
        run_dir = self.config.work_dir / run_id
        manifest_path = run_dir / "manifest.json"
        self.manifest = {
            "schema_version": SCHEMA_VERSION,
            "run_id": run_id,
            "started_at": iso_now(),
            "status": "running",
            "input": {
                "orthanc": "source",
                "study_count": len(current_studies),
                "instance_count": sum(item.instance_count for item in current_studies),
                "series_count": sum(item.series_count for item in current_studies),
                "studies": [
                    {
                        "orthanc_study_id": item.orthanc_id,
                        "study_uid": item.study_uid,
                        "instance_count": item.instance_count,
                        "series_count": item.series_count,
                    }
                    for item in current_studies
                ],
            },
            "ingestion_jobs": [
                {
                    "id": job["id"],
                    "model_name": job["modelName"],
                    "model_version": job["modelVersion"],
                    "original_status": job["status"],
                    "queue": routes[str(job["id"])],
                }
                for job in jobs
            ],
        }
        run_dir.mkdir(parents=True, exist_ok=False)
        write_json(manifest_path, self.manifest)
        # Capture this before attempting the first stop. If the pause or drain
        # phase fails halfway through, finally still restores every originally
        # running job (starting an already-running job is idempotent).
        running_ids = [
            str(job["id"])
            for job in jobs
            if str(job.get("status", "")).upper() == "RUNNING"
        ]
        restored = False
        try:
            self._pause(jobs, routes)
            if self.source.xa_studies() != current_studies:
                raise ReingestionError(
                    "PACS_QUERY XA inventory changed while discovery was draining; "
                    "rerun the dry-run"
                )
            target_time = (
                datetime.now(ZoneInfo(self.config.timezone_name)) - timedelta(seconds=5)
            ).replace(tzinfo=None)
            replays: list[dict[str, Any]] = []
            for index, current_study in enumerate(current_studies, 1):
                study_name = f"study-{index:04d}"
                inventory = self.source.download_study(
                    current_study, run_dir / "snapshot" / study_name
                )
                patient_id = f"{self.config.patient_id_prefix}-{run_id[-8:]}-{index:04d}"
                replay = build_replay(
                    inventory,
                    run_dir / "dicom" / study_name,
                    target_time=target_time,
                    patient_id=patient_id,
                )
                replay["input_study_uid"] = current_study.study_uid
                replay["input_orthanc_study_id"] = current_study.orthanc_id
                replay["patient_id"] = patient_id
                replay["directory"] = study_name
                replays.append(replay)
                log(f"Prepared {index}/{len(current_studies)} XA study replay(s)")
            old_uids = {item.study_uid for item in current_studies}
            new_uids = {str(replay["study_uid"]) for replay in replays}
            if old_uids & new_uids:
                raise ReingestionError("refusing cleanup because an old and new StudyInstanceUID match")
            if len(new_uids) != len(replays):
                raise ReingestionError("generated duplicate StudyInstanceUID values")
            self.manifest["replays"] = replays
            write_json(manifest_path, self.manifest)
            uploaded_counts: dict[str, int] = {}
            for index, (current_study, replay) in enumerate(
                zip(current_studies, replays, strict=True), 1
            ):
                dicom_dir = run_dir / "dicom" / str(replay["directory"])
                replay_orthanc_id = self._upload(dicom_dir, replay["files"])
                if replay_orthanc_id in uploaded_counts:
                    raise ReingestionError("multiple replay uploads resolved to one Orthanc study")
                replay["replay_orthanc_study_id"] = replay_orthanc_id
                uploaded_counts[replay_orthanc_id] = current_study.instance_count
                write_json(manifest_path, self.manifest)
                log(f"Uploaded {index}/{len(replays)} XA study replay(s)")
            self._wait_for_uploaded_studies(uploaded_counts)
            self._restore(running_ids)
            restored = True
            self.manifest["results"] = self._monitor(
                jobs, [str(replay["study_uid"]) for replay in replays]
            )
            if self.prune_previous:
                self.manifest["cleanup"] = [
                    self._cleanup_previous(current_study.study_uid)
                    for current_study in current_studies
                ]
            else:
                self.manifest["cleanup"] = {"status": "retained", "reason": "cleanup not requested"}
            self.manifest["status"] = "succeeded"
            self.manifest["completed_at"] = iso_now()
            write_json(manifest_path, self.manifest)
            write_json(self.latest_path, self.manifest)
            log(f"XA demo reingestion succeeded; manifest: {manifest_path}")
            return 0
        except BaseException as exc:
            self.manifest["status"] = "interrupted" if isinstance(exc, KeyboardInterrupt) else "failed"
            self.manifest["completed_at"] = iso_now()
            self.manifest["error_type"] = type(exc).__name__
            write_json(manifest_path, self.manifest)
            raise
        finally:
            if running_ids and not restored:
                self._restore(running_ids)


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--env-file",
        type=Path,
        default=Path(__file__).resolve().parent / ".env.demo-xa",
        help="configuration file (default: scripts/.env.demo-xa)",
    )
    parser.add_argument("--execute", action="store_true", help="perform the replay; default is read-only")
    parser.add_argument(
        "--prune-previous",
        action="store_true",
        help="after every model succeeds, delete only the snapshotted source XA studies",
    )
    parser.add_argument(
        "--allow-database-cleanup",
        action="store_true",
        help="with --prune-previous, delete exact-study pipeline history from both databases",
    )
    args = parser.parse_args(argv)
    if args.prune_previous and not args.execute:
        parser.error("--prune-previous requires --execute")
    if args.allow_database_cleanup and not args.prune_previous:
        parser.error("--allow-database-cleanup requires --prune-previous")
    return args


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    repo_root = Path(__file__).resolve().parent.parent
    try:
        config = Config.from_values(load_env(args.env_file), repo_root)
        runner = DemoXaReingestion(
            config,
            execute=args.execute,
            prune_previous=args.prune_previous,
            allow_database_cleanup=args.allow_database_cleanup,
        )
        return runner.run()
    except KeyboardInterrupt:
        log("Interrupted; original XA discovery state was restored")
        return 130
    except (ReingestionError, ValueError, OSError) as exc:
        print(f"[demo-xa] ERROR: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
