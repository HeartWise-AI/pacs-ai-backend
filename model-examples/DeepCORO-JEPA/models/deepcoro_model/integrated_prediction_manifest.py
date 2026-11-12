"""Bind saved wide predictions to the exact actual ordered study input bags."""
from __future__ import annotations

import json
from pathlib import Path

import pandas as pd

from experiment_protocol import write_json_atomic
from integrated_data import sha256_file


def write_complete_manifest(path, prediction_path, pass_audit, checkpoint_path,
                            input_protocol, *, lineage, split, base=None):
    prediction_path, checkpoint_path = Path(prediction_path), Path(checkpoint_path)
    if not prediction_path.is_file() or not checkpoint_path.is_file():
        raise ValueError("Predictions and checkpoint must be successfully saved before marking complete")
    predictions = pd.read_parquet(prediction_path)
    if predictions.index.has_duplicates or pass_audit.StudyInstanceUID.duplicated().any():
        raise ValueError("Prediction/pass audit contains duplicate studies")
    if predictions.index.astype(str).tolist() != pass_audit.StudyInstanceUID.astype(str).tolist():
        raise ValueError("Saved predictions and actual pass audit study order differ")
    cases = []
    for row in pass_audit.itertuples():
        provenance = json.loads(row.video_provenance)
        files = [v["FileName"] for v in provenance]
        if not files or len(files) != int(row.full_views):
            raise ValueError("Actual selected video provenance does not match the measured full-view count")
        cases.append({"StudyInstanceUID": str(row.StudyInstanceUID), "FileNames": files})
    payload = {**(base or {}), "state": "complete", "input_protocol": input_protocol, "split": split,
               "checkpoint_sha256": sha256_file(checkpoint_path), "lineage": lineage,
               "canonical_cohort_sha256": lineage.get("canonical_cohort_sha256"),
               "studies": cases, "runtime": {"predictions_sha256": sha256_file(prediction_path),
                                               "predictions_file": prediction_path.name,
                                               "predicted_studies": len(predictions)},
               "target_gates_claimed": False}
    write_json_atomic(path, payload)
    return payload
