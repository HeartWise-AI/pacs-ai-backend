"""Verify the immutable files behind the per-video classifier contract."""

import hashlib
from pathlib import Path
import re


def verified_preprocessing_contract(root, declared, model_path, mapping_path, source_revision):
    if not isinstance(source_revision, str) or not re.fullmatch(r"[0-9a-f]{40}", source_revision):
        raise ValueError("Classifier requires its immutable source revision")
    if declared.get("schemaVersion") != "angiogram-procedure-view-sop-v1":
        raise ValueError("Unsupported preprocessing identity contract")
    result = dict(declared)
    models = (Path(root) / "models").resolve()
    for name, relative in [("weightsSha256", model_path), ("classMappingSha256", mapping_path)]:
        expected = declared.get(name)
        target = (models / relative).resolve()
        if not target.is_relative_to(models) or not re.fullmatch(r"[0-9a-f]{64}", str(expected)):
            raise ValueError("Invalid classifier artifact identity")
        digest = hashlib.sha256()
        with target.open("rb") as handle:
            for chunk in iter(lambda: handle.read(1024 * 1024), b""):
                digest.update(chunk)
        if digest.hexdigest() != expected:
            raise ValueError("Classifier artifact does not match declared identity")
    result["sourceRevision"] = source_revision
    return result
