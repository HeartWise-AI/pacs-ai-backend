"""Write a scripts/probe_report.py probe (joblib) in the layout models/probes.py loads.

Not part of the image. Run it where the fitted probes are, with scikit-learn and joblib:

    python convert_probe.py heads/shd.joblib --out probes/shd --id shd --task binary \\
        --labels "Structural heart disease" --thresholds 0.0812 \\
        --description "SHD (EchoNext definition) on an echocardiogram within 1 year"
    python convert_probe.py heads/cls77.joblib --out probes/patterns77 --id patterns77 \\
        --task multilabel --thresholds-json cls77_val_youden.json --description "77 ECG patterns"
    (cd probes && sha256sum */* > SHA256SUMS)

probe_report.py saves {"scaler", "clf", ...} for a binary head and {"scaler", "labels", "clfs":
{label: clf}} for the 77-class head; both scalers are StandardScaler and the classifiers are
LogisticRegression on the 1536-d frozen features. The thresholds are the validation Youden
points of each output; pass them explicitly, they are not stored in the joblib.
"""
import argparse
import json
from pathlib import Path

import joblib
import numpy as np
import torch
from safetensors.torch import save_file


def convert(head: dict, labels: list[str] | None) -> tuple[dict, list[str]]:
    scaler = head["scaler"]
    if "clfs" in head:
        names = labels or [str(n) for n in head["labels"]]
        clfs = [head["clfs"][n] for n in names]
    else:
        clfs, names = [head["clf"]], labels
    weight = np.concatenate([c.coef_.reshape(1, -1) for c in clfs]).astype(np.float64)
    bias = np.array([float(np.ravel(c.intercept_)[0]) for c in clfs], dtype=np.float64)
    tensors = {"weight": torch.from_numpy(weight), "bias": torch.from_numpy(bias)}
    if scaler is not None:
        tensors["scaler_mean"] = torch.from_numpy(np.asarray(scaler.mean_, dtype=np.float64))
        tensors["scaler_scale"] = torch.from_numpy(np.asarray(scaler.scale_, dtype=np.float64))
    return tensors, names


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("joblib_path")
    parser.add_argument("--out", required=True, type=Path)
    parser.add_argument("--id", required=True)
    parser.add_argument("--task", required=True, choices=("binary", "multilabel", "multiclass"))
    parser.add_argument("--description", default="")
    parser.add_argument("--labels", nargs="*")
    parser.add_argument("--thresholds", nargs="*", type=float)
    parser.add_argument("--thresholds-json", help="{label: validation Youden threshold}")
    parser.add_argument("--source-json", help="provenance: checkpoint, commit, features, splits")
    parser.add_argument("--metrics-json", help="held-out test metrics")
    args = parser.parse_args()

    tensors, names = convert(joblib.load(args.joblib_path), args.labels)
    if args.thresholds_json:
        by_label = json.loads(Path(args.thresholds_json).read_text())
        thresholds = [by_label[n] for n in names]
    else:
        thresholds = args.thresholds
    manifest = {"id": args.id, "task": args.task, "description": args.description,
                "labels": names, "thresholds": thresholds}
    for key, path in (("source", args.source_json), ("metrics", args.metrics_json)):
        if path:
            manifest[key] = json.loads(Path(path).read_text())
    args.out.mkdir(parents=True, exist_ok=True)
    save_file(tensors, str(args.out / "classifier.safetensors"))
    (args.out / "head.json").write_text(json.dumps(manifest, indent=2) + "\n")
    print(f"Wrote {args.out} ({len(names)} output(s))")


if __name__ == "__main__":
    main()
