"""Shared, CPU-only validation rules for coronary experiments.

Missing patient identities cannot establish a patient-disjoint cohort. They are
excluded explicitly, never collapsed into a single synthetic patient.
"""
from __future__ import annotations

import hashlib
import json
import shutil
import subprocess
from pathlib import Path

import numpy as np
import pandas as pd

DISTAL = ("dist_lad", "dist_lcx", "pda", "posterolateral")
STENOSIS_SEGMENTS = ("prox_rca", "mid_rca", "dist_rca", "pda", "posterolateral", "left_main",
                     "prox_lad", "mid_lad", "dist_lad", "D1", "D2", "prox_lcx", "mid_lcx",
                     "dist_lcx", "om1", "om2", "bx", "lvp")


def threshold(segment):
    return 50.0 if segment == "left_main" else 70.0


def known_patient_mask(values):
    s = pd.Series(values, copy=False).astype("string").str.strip()
    return (s.notna() & ~s.str.lower().isin(["", "nan", "none", "null", "<na>"])).to_numpy(dtype=bool)


def assert_patient_disjoint(*frames):
    """Check study and patient isolation; callers explicitly exclude unknown IDs."""
    patient_sets, study_sets = [], []
    for frame in frames:
        if not known_patient_mask(frame.patient_id_anon).all():
            raise ValueError("Missing patient identity: exclude and record these rows before checking splits")
        patient_sets.append(set(frame.patient_id_anon.astype(str).str.strip()))
        study_sets.append(set(frame.index))
    for i in range(len(frames)):
        for j in range(i + 1, len(frames)):
            p, s = patient_sets[i] & patient_sets[j], study_sets[i] & study_sets[j]
            if p or s:
                raise ValueError(f"Split overlap {i}/{j}: {len(p)} patients, {len(s)} studies")


def load_canonical_cohort(path, expected_sha256=None):
    """Load a frozen, outcome-independent identity/exposure audit.

    An incomplete ancestor audit remains explicit; absence of documented
    exposure is not silently upgraded to proof of no exposure.
    """
    path = Path(path)
    digest = file_sha256(path)
    if expected_sha256 is not None and digest != expected_sha256:
        raise ValueError("Canonical cohort SHA256 does not match the frozen protocol")
    cohort = pd.read_parquet(path)
    required = {"StudyInstanceUID", "canonical_patient_hash", "original_split", "identity_known",
                "supervised_exposed", "ssl_exposed", "ssl_exposure_audit_complete",
                "canonical_patient_crosses_original_splits"} | {f"eligible_corrected_{s}" for s in ("train", "val", "test")}
    if not required.issubset(cohort):
        raise ValueError(f"Canonical cohort lacks required audit fields: {sorted(required - set(cohort))}")
    if cohort.StudyInstanceUID.duplicated().any():
        raise ValueError("Canonical cohort has duplicate study IDs")
    cohort = cohort.set_index("StudyInstanceUID")
    flags = required - {"StudyInstanceUID", "canonical_patient_hash", "original_split"}
    for field in flags:
        if cohort[field].isna().any() or not cohort[field].isin([True, False]).all():
            raise ValueError(f"Invalid boolean audit field: {field}")
    if not known_patient_mask(cohort.loc[cohort.identity_known, "canonical_patient_hash"]).all():
        raise ValueError("Known identity lacks a canonical patient hash")
    for split in ("train", "val", "test"):
        allowed = cohort[f"eligible_corrected_{split}"]
        invalid = ~cohort.identity_known | ~cohort.original_split.eq(split) | cohort.canonical_patient_crosses_original_splits
        if split != "train":
            invalid |= cohort.supervised_exposed | cohort.ssl_exposed
        if (allowed & invalid).any():
            raise ValueError(f"Canonical {split} eligibility contradicts identity/exposure evidence")
    eligible = [cohort.loc[cohort[f"eligible_corrected_{s}"]].rename(columns={"canonical_patient_hash": "patient_id_anon"}) for s in ("train", "val", "test")]
    assert_patient_disjoint(*eligible)
    cohort.attrs.update(path=str(path.resolve()), sha256=digest,
                        ancestry_audit_complete=bool(cohort.ssl_exposure_audit_complete.all()))
    return cohort


def apply_canonical_cohort(metadata, cohort, split):
    """Filter an indexed study label table and replace patient groups with HMACs.

    Returns (filtered metadata, explicit audit counts). No outcomes are used.
    `metadata` must be indexed by StudyInstanceUID; source split is preserved.
    """
    if split not in ("train", "val", "test"):
        raise ValueError(f"Unknown cohort split {split}")
    if not metadata.index.is_unique:
        raise ValueError("Study metadata index must be unique")
    missing = metadata.index.difference(cohort.index)
    if len(missing):
        raise ValueError(f"{len(missing)} studies absent from frozen canonical audit")
    audit = cohort.reindex(metadata.index)
    if "Split" in metadata and not metadata.Split.eq(audit.original_split).all():
        raise ValueError("Source split disagrees with canonical audit")
    allowed = audit[f"eligible_corrected_{split}"]
    result = metadata.loc[allowed].copy()
    if "patient_id_anon" in result:
        result["patient_id_anon_original"] = result.patient_id_anon
    result["patient_id_anon"] = audit.loc[allowed, "canonical_patient_hash"]
    result["canonical_patient_hash"] = audit.loc[allowed, "canonical_patient_hash"]
    result["Split"] = audit.loc[allowed, "original_split"]
    counts = {"source_studies": len(metadata), "eligible_studies": len(result),
              "excluded_studies": int((~allowed).sum()), "patients": int(result.patient_id_anon.nunique()),
              "documented_ssl_exposed_studies": int(audit.ssl_exposed.sum()),
              "ambiguous_identity_studies": int((~audit.identity_known).sum()),
              "canonical_cohort_sha256": cohort.attrs.get("sha256"),
              "ancestry_audit_complete": cohort.attrs.get("ancestry_audit_complete", False)}
    return result, counts


def patient_bootstrap_indices(patients, n_bootstrap=1000, seed=0):
    """Resample patients with replacement, retaining ALL studies of each draw."""
    if not known_patient_mask(patients).all():
        raise ValueError("Patient bootstrap requires non-missing patient identifiers")
    groups = pd.Series(np.arange(len(patients))).groupby(np.asarray(patients)).apply(list)
    rows = [np.asarray(x, dtype=np.int64) for x in groups]
    rng = np.random.default_rng(seed)
    for _ in range(n_bootstrap):
        yield np.concatenate([rows[j] for j in rng.integers(0, len(rows), len(rows))])


def pearson(y, p):
    y, p = np.asarray(y, dtype=float), np.asarray(p, dtype=float)
    if len(y) < 3 or np.ptp(y) == 0 or np.ptp(p) == 0:
        return float("nan")
    return float(np.corrcoef(y, p)[0, 1])


def confidence_interval(values):
    v = np.asarray(values, dtype=float)
    v = v[np.isfinite(v)]
    return np.percentile(v, [2.5, 97.5]).tolist() if len(v) else [float("nan"), float("nan")]


def file_sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as stream:
        for chunk in iter(lambda: stream.read(8 * 1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def git_revision(path):
    result = subprocess.run(["git", "-C", str(path), "rev-parse", "HEAD"], capture_output=True, text=True)
    return result.stdout.strip() if result.returncode == 0 else None


def snapshot_sources(out, files):
    """Retain source bytes, not only a dirty-worktree commit/hash reference."""
    destination = Path(out) / "source"
    destination.mkdir(parents=True, exist_ok=True)
    records = {}
    for source in map(Path, files):
        target = destination / source.name
        digest = file_sha256(source)
        if target.exists() and file_sha256(target) != digest:
            raise ValueError(f"Existing source snapshot differs: {source.name}; use a new run directory")
        if not target.exists():
            shutil.copyfile(source, target)
        records[source.name] = {"sha256": file_sha256(target), "snapshot": str(target.resolve())}
    return records


def write_json_atomic(path, payload):
    path = Path(path)
    tmp = path.with_name(path.name + ".tmp")
    def clean(value):
        if isinstance(value, dict):
            return {str(k): clean(v) for k, v in value.items()}
        if isinstance(value, (list, tuple)):
            return [clean(v) for v in value]
        if isinstance(value, (float, np.floating)) and not np.isfinite(value):
            return None
        if isinstance(value, np.generic):
            return value.item()
        return value
    tmp.write_text(json.dumps(clean(payload), indent=2, allow_nan=False) + "\n")
    tmp.replace(path)
