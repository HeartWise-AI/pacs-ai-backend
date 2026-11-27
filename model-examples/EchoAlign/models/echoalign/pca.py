"""Fit EchoAlign's PCA bases on the training studies and write them into a weights file.

    python -m echoalign.pca --weights my_weights.pt --embeddings embeddings/my_cohort/echoalign.npz \
        --labels data/labels/my_cohort_probe.csv --out my_weights_pca.pt
"""
import argparse
from pathlib import Path

import numpy as np
import pandas as pd
import torch
from sklearn.decomposition import PCA

K = 64


def iter_npz(path):
    # Imported here so that `import echoalign` does not need the eval package.
    from eval.analysis.data import iter_npz
    return iter_npz(path)


def study_vectors(path, train):
    ids, vecs = [], []
    for part in iter_npz(path):
        keep = np.isin(part["study_id"], train)
        ids.append(part["study_id"][keep])
        vecs.append(part["vectors"][keep].astype(np.float64))
    return np.concatenate(ids), np.concatenate(vecs)


def clip_means(path, train):
    ids, means = [], []
    for part in iter_npz(path):
        keep = np.isin(part["study_id"], train)
        sid = part["study_id"][keep]
        x = np.concatenate([part["global_vector"][keep][:, None], part["structures"][keep]], axis=1)
        uniq, inv = np.unique(sid, return_inverse=True)
        s = np.zeros((len(uniq),) + x.shape[1:], np.float64)
        np.add.at(s, inv, x)
        ids.append(uniq)
        means.append((s / np.bincount(inv)[:, None, None]).astype(np.float32))
    return np.concatenate(ids), np.concatenate(means)


def svd_bases(x):
    comps, mean = [], []
    for slot in range(x.shape[1]):
        center = x[:, slot].mean(0)
        _, _, vt = np.linalg.svd(x[:, slot] - center, full_matrices=False)
        comps.append(vt[:K]); mean.append(center)
    return np.stack(comps), np.stack(mean)


def pca_bases(x):
    comps, mean = [], []
    for slot in range(x.shape[1]):
        m = PCA(n_components=K, svd_solver="randomized", random_state=0, iterated_power=4)
        m.fit(np.asarray(x[:, slot], np.float64))
        comps.append(m.components_); mean.append(m.mean_)
    return np.stack(comps), np.stack(mean)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--weights", required=True)
    ap.add_argument("--embeddings", required=True, help="study file from scripts/embed.py; its _clips file sits next to it")
    ap.add_argument("--labels", required=True)
    ap.add_argument("--out", required=True)
    a = ap.parse_args()

    lab = pd.read_csv(a.labels, usecols=["study_id", "split"], dtype=str)
    train = np.array(sorted(lab.loc[lab.split == "TRAIN", "study_id"].unique()))
    emb = Path(a.embeddings)
    blob = torch.load(a.weights, map_location="cpu", weights_only=True)
    for level, read, fit in (("study", study_vectors, svd_bases), ("clip", clip_means, pca_bases)):
        path = emb if level == "study" else emb.with_name(emb.stem + "_clips.npz")
        ids, x = read(path, train)
        assert len(np.unique(ids)) == len(ids), f"a study appears in more than one {level} shard"
        comps, mean = fit(x)
        blob[f"pca_{level}"] = {"components": torch.from_numpy(comps),
                                "mean": torch.from_numpy(mean)}
        print(f"{level}: {len(ids)} of {len(train)} TRAIN studies, components {tuple(comps.shape)}", flush=True)
    torch.save(blob, a.out)
    print(f"wrote {a.out}")


if __name__ == "__main__":
    main()
