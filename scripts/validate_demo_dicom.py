#!/usr/bin/env python3
"""Validate a demo DICOM dataset against the repository de-identification policy.

The validator intentionally never writes DICOM values, UIDs, filenames, or URLs to
its reports. Instance references are one-way hashes so validation evidence can be
stored in the repository without reintroducing identifying information.
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
import re
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request
from collections import Counter
from collections.abc import Iterable, Iterator
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

import pydicom
from pydicom.dataset import Dataset
from pydicom.tag import Tag

REPORT_SCHEMA_VERSION = 1
FREE_TEXT_VRS = {"AE", "LO", "LT", "PN", "SH", "ST", "UC", "UR", "UT"}


@dataclass(frozen=True)
class InstanceInput:
    reference_seed: str
    payload: bytes | None
    source_error_type: str | None = None


@dataclass
class ScanState:
    instance_hashes: list[str] = field(default_factory=list)
    study_refs: set[str] = field(default_factory=set)
    series_refs: set[str] = field(default_factory=set)
    violations: list[dict[str, str]] = field(default_factory=list)
    errors: list[dict[str, str]] = field(default_factory=list)
    counts: Counter[str] = field(default_factory=Counter)


def utc_now() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def sha256_text(value: str) -> str:
    return hashlib.sha256(value.encode("utf-8", errors="replace")).hexdigest()


def short_ref(value: str) -> str:
    return sha256_text(value)[:16]


def canonical_tag(value: str | int | Tag) -> str:
    if isinstance(value, str):
        normalized = (
            value.upper().replace("(", "").replace(")", "").replace(",", "").replace(" ", "")
        )
        if not re.fullmatch(r"[0-9A-F]{8}", normalized):
            raise ValueError(f"invalid DICOM tag in policy: {value!r}")
        return normalized
    return f"{int(value):08X}"


def load_json(path: Path) -> dict[str, Any]:
    with path.open("r", encoding="utf-8") as handle:
        value = json.load(handle)
    if not isinstance(value, dict):
        raise ValueError(f"{path.name} must contain a JSON object")
    return value


def validate_policy(policy: dict[str, Any]) -> None:
    if policy.get("schema_version") != 1:
        raise ValueError("policy schema_version must be 1")
    for name in ("policy_id", "policy_version", "approval"):
        if name not in policy:
            raise ValueError(f"policy is missing {name}")
    approval = policy["approval"]
    if not isinstance(approval, dict) or "status" not in approval:
        raise ValueError("policy approval must contain status")
    for rule_name in ("prohibited_nonempty_tags", "required_values", "allowed_value_patterns"):
        rules = policy.get(rule_name, {})
        if not isinstance(rules, dict):
            raise ValueError(f"policy {rule_name} must be an object")
        for tag_value in rules:
            canonical_tag(tag_value)
    for pattern in policy.get("suspicious_value_patterns", []):
        re.compile(pattern["pattern"])


def policy_is_approved(policy: dict[str, Any]) -> bool:
    approval = policy.get("approval", {})
    approved_at = str(approval.get("approved_at", "")).strip()
    try:
        parsed_approval_date = datetime.fromisoformat(approved_at.replace("Z", "+00:00"))
    except ValueError:
        return False
    return bool(
        approval.get("status") == "approved"
        and str(approval.get("approved_by", "")).strip()
        and parsed_approval_date.tzinfo is not None
    )


def safe_instance_ref(dataset: Dataset, fallback_seed: str) -> str:
    sop_uid = str(dataset.get("SOPInstanceUID", "")).strip()
    return short_ref(sop_uid or fallback_seed)


def add_violation(
    state: ScanState,
    instance_ref: str,
    code: str,
    tag: str,
    keyword: str,
    reason: str,
) -> None:
    state.violations.append(
        {
            "instance_ref": instance_ref,
            "code": code,
            "tag": tag,
            "keyword": keyword,
            "reason": reason,
        }
    )
    state.counts[code] += 1


def string_value(element: Any) -> str:
    value = element.value
    if value is None:
        return ""
    if isinstance(value, bytes):
        return ""
    if isinstance(value, (list, tuple)):
        return "\\".join(str(item) for item in value)
    return str(value)


def scan_dataset(
    dataset: Dataset, policy: dict[str, Any], state: ScanState, fallback_seed: str
) -> None:
    instance_ref = safe_instance_ref(dataset, fallback_seed)
    study_uid = str(dataset.get("StudyInstanceUID", "")).strip()
    series_uid = str(dataset.get("SeriesInstanceUID", "")).strip()
    if study_uid:
        state.study_refs.add(short_ref(study_uid))
    if series_uid:
        state.series_refs.add(short_ref(series_uid))

    prohibited = {
        canonical_tag(key): value
        for key, value in policy.get("prohibited_nonempty_tags", {}).items()
    }
    allowed_patterns = {
        canonical_tag(key): re.compile(value["pattern"])
        for key, value in policy.get("allowed_value_patterns", {}).items()
    }
    required = {
        canonical_tag(key): value for key, value in policy.get("required_values", {}).items()
    }
    ignored_value_tags = {
        canonical_tag(value) for value in policy.get("ignore_value_scan_tags", [])
    }
    suspicious_patterns = [
        (entry["id"], re.compile(entry["pattern"], re.IGNORECASE))
        for entry in policy.get("suspicious_value_patterns", [])
    ]

    for tag_value, rule in required.items():
        element = dataset.get(Tag(int(tag_value, 16)))
        if element is None:
            add_violation(
                state,
                instance_ref,
                "required_tag_missing",
                tag_value,
                rule.get("keyword", ""),
                rule["reason"],
            )
            continue
        actual = string_value(element).strip()
        expected = rule.get("equals")
        if expected is not None and actual.upper() != str(expected).upper():
            add_violation(
                state,
                instance_ref,
                "required_value_mismatch",
                tag_value,
                element.keyword,
                rule["reason"],
            )
        if rule.get("non_empty") and not actual:
            add_violation(
                state,
                instance_ref,
                "required_value_empty",
                tag_value,
                element.keyword,
                rule["reason"],
            )

    prohibit_private = policy.get("private_tags", {}).get("mode") == "prohibit"
    for element in dataset.iterall():
        tag_value = canonical_tag(element.tag)
        value = string_value(element).strip()
        if tag_value in prohibited and value:
            add_violation(
                state,
                instance_ref,
                "prohibited_tag_populated",
                tag_value,
                element.keyword,
                prohibited[tag_value]["reason"],
            )
        if (
            tag_value in allowed_patterns
            and value
            and not allowed_patterns[tag_value].fullmatch(value)
        ):
            add_violation(
                state,
                instance_ref,
                "value_outside_allowlist",
                tag_value,
                element.keyword,
                policy["allowed_value_patterns"][tag_value]["reason"],
            )
        if prohibit_private and element.tag.is_private:
            add_violation(
                state,
                instance_ref,
                "private_tag_present",
                tag_value,
                element.keyword or "PrivateElement",
                "Private DICOM elements are prohibited unless the approved policy is changed.",
            )
        if (
            element.VR not in FREE_TEXT_VRS
            or not value
            or tag_value in ignored_value_tags
            or tag_value in allowed_patterns
        ):
            continue
        for pattern_id, pattern in suspicious_patterns:
            if pattern.search(value):
                add_violation(
                    state,
                    instance_ref,
                    "suspicious_text_pattern",
                    tag_value,
                    element.keyword,
                    f"Text matched prohibited pattern {pattern_id}; the value is intentionally omitted.",
                )


def iter_local_instances(root: Path, all_files: bool) -> Iterator[InstanceInput]:
    root_is_file = root.is_file()
    if root_is_file:
        candidates: Iterable[Path] = [root]
    else:
        candidates = sorted(path for path in root.rglob("*") if path.is_file())
    for path in candidates:
        if not root_is_file and not all_files and path.suffix.lower() not in {".dcm", ".dicom"}:
            continue
        reference_seed = sha256_text(str(path.resolve()))
        try:
            yield InstanceInput(reference_seed=reference_seed, payload=path.read_bytes())
        except OSError as exc:
            yield InstanceInput(
                reference_seed=reference_seed,
                payload=None,
                source_error_type=type(exc).__name__,
            )


def orthanc_request(
    url: str, username: str, password: str, accept: str = "application/json"
) -> bytes:
    request = urllib.request.Request(url, headers={"Accept": accept})
    if username or password:
        token = base64.b64encode(f"{username}:{password}".encode()).decode()
        request.add_header("Authorization", f"Basic {token}")
    with urllib.request.urlopen(request, timeout=60) as response:
        return response.read()


def iter_orthanc_instances(
    base_url: str, username: str, password: str
) -> Iterator[InstanceInput]:
    base_url = base_url.rstrip("/")
    raw_ids = orthanc_request(f"{base_url}/instances", username, password)
    instance_ids = json.loads(raw_ids)
    if not isinstance(instance_ids, list):
        raise ValueError("Orthanc /instances did not return a JSON array")
    for instance_id in sorted(str(value) for value in instance_ids):
        encoded_id = urllib.parse.quote(instance_id, safe="")
        reference_seed = sha256_text(instance_id)
        try:
            payload = orthanc_request(
                f"{base_url}/instances/{encoded_id}/file",
                username,
                password,
                "application/dicom",
            )
            yield InstanceInput(reference_seed=reference_seed, payload=payload)
        except (OSError, urllib.error.URLError) as exc:
            yield InstanceInput(
                reference_seed=reference_seed,
                payload=None,
                source_error_type=type(exc).__name__,
            )


def dataset_fingerprint(instance_hashes: list[str]) -> str:
    digest = hashlib.sha256()
    for value in sorted(instance_hashes):
        digest.update(value.encode("ascii"))
        digest.update(b"\n")
    return digest.hexdigest()


def pixel_attestation_status(
    policy: dict[str, Any], attestation_path: Path | None, fingerprint: str
) -> tuple[str, str | None]:
    if not policy.get("require_pixel_review_attestation", True):
        return "not_required", None
    if attestation_path is None:
        return "missing", "A burned-in text review attestation is required by policy."
    try:
        attestation = load_json(attestation_path)
    except (OSError, ValueError, json.JSONDecodeError):
        return "invalid", "The burned-in text review attestation could not be read."
    required = ("reviewer", "reviewed_at", "dataset_fingerprint")
    if attestation.get("status") != "passed" or any(
        not str(attestation.get(key, "")).strip() for key in required
    ):
        return "invalid", "The burned-in text review attestation is incomplete or not passed."
    if attestation["dataset_fingerprint"] != fingerprint:
        return (
            "stale",
            "The burned-in text review attestation belongs to a different dataset fingerprint.",
        )
    return "passed", None


def build_report(
    policy: dict[str, Any], state: ScanState, source_type: str, attestation_path: Path | None
) -> dict[str, Any]:
    fingerprint = dataset_fingerprint(state.instance_hashes)
    pixel_status, pixel_message = pixel_attestation_status(policy, attestation_path, fingerprint)
    blockers: list[str] = []
    if not policy_is_approved(policy):
        blockers.append("The de-identification policy has not been approved by a named reviewer.")
    if not state.instance_hashes:
        blockers.append("No DICOM instances were validated.")
    if state.errors:
        blockers.append("One or more instances could not be validated.")
    if pixel_message:
        blockers.append(pixel_message)

    if state.violations:
        result = "FAIL"
    elif blockers:
        result = "INCOMPLETE"
    else:
        result = "PASS"

    approval = policy.get("approval", {})
    return {
        "schema_version": REPORT_SCHEMA_VERSION,
        "generated_at": utc_now(),
        "result": result,
        "policy": {
            "id": policy.get("policy_id"),
            "version": policy.get("policy_version"),
            "approval_status": approval.get("status", "unknown"),
            "approved_by": approval.get("approved_by", ""),
            "approved_at": approval.get("approved_at", ""),
        },
        "source": {"type": source_type},
        "dataset": {
            "fingerprint": fingerprint,
            "studies": len(state.study_refs),
            "series": len(state.series_refs),
            "instances": len(state.instance_hashes),
        },
        "pixel_review": {"status": pixel_status},
        "summary": {
            "violations": len(state.violations),
            "validation_errors": len(state.errors),
            "violations_by_code": dict(sorted(state.counts.items())),
        },
        "blockers": blockers,
        "violations": state.violations,
        "validation_errors": state.errors,
        "privacy_note": "DICOM values, UIDs, filenames, URLs, and credentials are intentionally omitted.",
    }


def markdown_report(report: dict[str, Any]) -> str:
    dataset = report["dataset"]
    summary = report["summary"]
    lines = [
        "# Public demo DICOM de-identification validation",
        "",
        f"- Result: **{report['result']}**",
        f"- Generated: `{report['generated_at']}`",
        f"- Policy: `{report['policy']['id']}` version `{report['policy']['version']}` ({report['policy']['approval_status']})",
        f"- Dataset fingerprint: `{dataset['fingerprint']}`",
        f"- Studies: {dataset['studies']}",
        f"- Series: {dataset['series']}",
        f"- Instances: {dataset['instances']}",
        f"- Metadata violations: {summary['violations']}",
        f"- Validation errors: {summary['validation_errors']}",
        f"- Burned-in text review: `{report['pixel_review']['status']}`",
        "",
    ]
    if report["blockers"]:
        lines.extend(["## Blockers", ""])
        lines.extend(f"- {value}" for value in report["blockers"])
        lines.append("")
    if summary["violations_by_code"]:
        lines.extend(["## Violation counts", "", "| Code | Count |", "|---|---:|"])
        lines.extend(
            f"| `{key}` | {value} |" for key, value in summary["violations_by_code"].items()
        )
        lines.append("")
    if report["violations"]:
        lines.extend(
            [
                "## Violations",
                "",
                "| Instance ref | Tag | Keyword | Code | Reason |",
                "|---|---|---|---|---|",
            ]
        )
        for violation in report["violations"]:
            reason = violation["reason"].replace("|", "\\|")
            lines.append(
                f"| `{violation['instance_ref']}` | `{violation['tag']}` | "
                f"`{violation['keyword']}` | `{violation['code']}` | {reason} |"
            )
        lines.append("")
    if report["validation_errors"]:
        lines.extend(["## Validation errors", ""])
        lines.extend(
            f"- `{value['instance_ref']}`: {value['reason']}"
            for value in report["validation_errors"]
        )
        lines.append("")
    lines.extend(["## Privacy", "", report["privacy_note"], ""])
    return "\n".join(lines)


def scan_instances(instances: Iterable[InstanceInput], policy: dict[str, Any]) -> ScanState:
    state = ScanState()
    for ordinal, instance in enumerate(instances, start=1):
        fallback_ref = short_ref(instance.reference_seed)
        if instance.payload is None:
            error_type = instance.source_error_type or "MissingPayload"
            state.errors.append(
                {
                    "instance_ref": fallback_ref,
                    "reason": f"Instance {ordinal} could not be read ({error_type}).",
                }
            )
            continue
        try:
            payload_hash = hashlib.sha256(instance.payload).hexdigest()
            with tempfile.SpooledTemporaryFile(max_size=16 * 1024 * 1024) as handle:
                handle.write(instance.payload)
                handle.seek(0)
                dataset = pydicom.dcmread(handle, force=False)
            state.instance_hashes.append(payload_hash)
            scan_dataset(dataset, policy, state, instance.reference_seed)
        except Exception as exc:  # a partial scan must never pass
            state.errors.append(
                {
                    "instance_ref": fallback_ref,
                    "reason": f"Instance {ordinal} could not be parsed ({type(exc).__name__}).",
                }
            )
    return state


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--path", type=Path, help="DICOM file or directory to scan recursively")
    source.add_argument(
        "--orthanc-url", help="Direct Orthanc REST base URL, normally http://127.0.0.1:8042"
    )
    parser.add_argument(
        "--all-files", action="store_true", help="Attempt every local file, not only .dcm/.dicom"
    )
    parser.add_argument("--policy", type=Path, required=True)
    parser.add_argument("--pixel-review-attestation", type=Path)
    parser.add_argument("--json-report", type=Path, required=True)
    parser.add_argument("--markdown-report", type=Path, required=True)
    parser.add_argument("--orthanc-username-env", default="ORTHANC_USERNAME")
    parser.add_argument("--orthanc-password-env", default="ORTHANC_PASSWORD")
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv or sys.argv[1:])
    try:
        policy = load_json(args.policy)
        validate_policy(policy)
        if args.path:
            instances = iter_local_instances(args.path, args.all_files)
            source_type = "local_directory" if args.path.is_dir() else "local_file"
        else:
            username = os.environ.get(args.orthanc_username_env, "")
            password = os.environ.get(args.orthanc_password_env, "")
            instances = iter_orthanc_instances(args.orthanc_url, username, password)
            source_type = "orthanc_rest"
        state = scan_instances(instances, policy)
        report = build_report(policy, state, source_type, args.pixel_review_attestation)
    except (OSError, ValueError, json.JSONDecodeError, urllib.error.URLError) as exc:
        print(f"Validation could not start: {type(exc).__name__}: {exc}", file=sys.stderr)
        return 2

    args.json_report.parent.mkdir(parents=True, exist_ok=True)
    args.markdown_report.parent.mkdir(parents=True, exist_ok=True)
    args.json_report.write_text(
        json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    args.markdown_report.write_text(markdown_report(report), encoding="utf-8")
    print(
        f"{report['result']}: {report['dataset']['instances']} instances, "
        f"{report['summary']['violations']} violations, fingerprint {report['dataset']['fingerprint']}"
    )
    return {"PASS": 0, "FAIL": 1, "INCOMPLETE": 2}[report["result"]]


if __name__ == "__main__":
    raise SystemExit(main())
