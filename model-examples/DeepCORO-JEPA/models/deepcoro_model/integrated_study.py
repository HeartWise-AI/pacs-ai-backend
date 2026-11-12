"""Train, export, and evaluate the genuinely integrated two-pass study model.

Unlike finetune_mil2.py, this path reads raw videos and generates its own masks,
full-view pooled features, and crops. One shared encoder is invoked at most
twice per study. Checkpoint selection uses validation only. Test inference is
an explicit separate command, once a selected checkpoint is available.

Example (after integrated_cache.py and integrated_train.py):
  python integrated_study.py export --decoder integrated_decoder_v1/best.pt --mil-init ft_all18w_c/best.pt --out integrated_joint_v1
  python integrated_study.py train --checkpoint integrated_joint_v1/model.pt --train-videos train_extra_videos.parquet train_y_videos.parquet --val-videos val_videos.parquet --out integrated_joint_ft --wandb
  python integrated_study.py predict --checkpoint integrated_joint_ft/best.pt --videos test_videos.parquet --split test --out integrated_joint_test --bootstrap 1000 --wandb

Decoder-only and warm-start exports are unvalidated candidates. Teacher models
and caches are offline training resources and never enter study inference.
"""
from __future__ import annotations

import argparse
from contextlib import nullcontext
import json
from pathlib import Path
import subprocess
import shutil
import time

import numpy as np
import pandas as pd
from scipy.stats import pearsonr
from sklearn.metrics import roc_auc_score
import torch
import torch.nn.functional as F
from torch.utils.data import DataLoader

from integrated_data import CachedMasks, StudyClips, one_study, sha256_file
from integrated_model import ALL_SEGMENTS, DISTAL_CLASSES, IntegratedStudyModel, load_encoder, segmentation_loss
from integrated_losses import study_supervision_loss
from integrated_batching import ViewBoundedStudyBatch, mask_retention_loss
from integrated_prediction_manifest import write_complete_manifest


def amp(device):
    return torch.autocast("cuda", dtype=torch.bfloat16) if str(device).startswith("cuda") else nullcontext()


def study_rows(video_files, split, groups, cohort=None):
    from fit_heads import P, labels
    from experiment_protocol import apply_canonical_cohort, known_patient_mask
    videos = (video_files.copy() if isinstance(video_files, pd.DataFrame) else
              pd.concat([pd.read_parquet(p) for p in video_files], ignore_index=True)).drop_duplicates("FileName")
    input_video_count = len(videos)
    source = pd.read_parquet(P, columns=["FileName", "StudyInstanceUID", "status", "Split", "patient_id_anon"])
    source_study_counts = source.groupby("FileName").StudyInstanceUID.nunique()
    ambiguous_files = set(source_study_counts[source_study_counts > 1].index)
    excluded_ambiguous_files = int(videos.FileName.isin(ambiguous_files).sum())
    videos = videos[~videos.FileName.isin(ambiguous_files)].copy()
    authoritative = source.drop_duplicates("FileName").set_index("FileName")
    status = videos.FileName.map(authoritative.status)
    videos = videos[status.eq("diagnostic")].copy()
    # A hand-written video manifest cannot relabel an intervention video or
    # assign a video to a different study.
    expected_uid = videos.FileName.map(authoritative.StudyInstanceUID)
    if not videos.StudyInstanceUID.eq(expected_uid).all():
        raise ValueError("Video manifest StudyInstanceUID disagrees with the source cohort")
    lab = labels(list(videos.StudyInstanceUID.unique()))
    lab = lab[lab.Split == split].copy()
    unknown_patients = int((~known_patient_mask(lab.patient_id_anon)).sum())
    canonical_counts = None
    recovered, overlapping_studies = 0, 0
    if cohort is not None:
        # Apply the frozen mapping BEFORE excluding original blank identifiers:
        # paired-record evidence can recover their validated canonical identity.
        lab, canonical_counts = apply_canonical_cohort(lab, cohort, split)
        recovered = int((~known_patient_mask(lab.patient_id_anon_original)).sum())
        unknown_excluded = canonical_counts["ambiguous_identity_studies"]
    else:
        lab = lab[known_patient_mask(lab.patient_id_anon)].copy()
        lab["patient_id_anon"] = lab.patient_id_anon.astype(str).str.strip()
        full = source.drop_duplicates("StudyInstanceUID")
        full = full[known_patient_mask(full.patient_id_anon)].copy()
        full["patient_id_anon"] = full.patient_id_anon.astype(str).str.strip()
        patients_by_split = full.groupby("patient_id_anon").Split.nunique()
        overlaps = set(patients_by_split[patients_by_split > 1].index)
        overlapping_studies = int(lab.patient_id_anon.isin(overlaps).sum())
        lab = lab[~lab.patient_id_anon.isin(overlaps)]
        unknown_excluded = unknown_patients
    files = videos.groupby("StudyInstanceUID").FileName.apply(list)
    lab["files"] = files.reindex(lab.index)
    lab["uid"] = lab.index
    audit = {"input_videos": input_video_count, "diagnostic_videos": len(videos),
             "excluded_nondiagnostic_or_unverified_videos": input_video_count-len(videos)-excluded_ambiguous_files,
             "excluded_ambiguous_video_study_paths": excluded_ambiguous_files,
             "original_unknown_patient_studies": unknown_patients,
             "recovered_originally_unknown_patient_studies": recovered,
             "excluded_unknown_patient_studies": unknown_excluded,
             "excluded_cross_split_patient_studies": overlapping_studies,
             "canonical_cohort": canonical_counts,
             "identity_scope": "frozen canonical paired-record HMAC identity" if cohort is not None else "observed raw identifiers; canonical alias audit is pending",
             "upstream_training_exposure": "documented exposures excluded according to frozen policy; unresolved ancestry remains explicit" if cohort is not None else "not excluded here; a frozen defensible cohort is required for target claims"}
    result = lab.reset_index(drop=True)
    result.attrs["cohort_audit"] = audit
    return result


def load_model(args):
    if args.checkpoint:
        ckpt = torch.load(args.checkpoint, map_location="cpu", weights_only=False)
        from integrated_multilayer import BUNDLE_SCHEMA
        if ckpt.get("bundle_schema") == BUNDLE_SCHEMA or getattr(args, "decoder_variant", "v1") == "multilayer_v2" or getattr(args, "multilayer_decoder", None):
            from integrated_multilayer import multilayer_model_from_payload
            layers = tuple(int(i) for i in getattr(args, "multilayer_blocks", "5,11,17").split(","))
            model = multilayer_model_from_payload(ckpt, device=args.device, train_blocks=args.train_blocks,
                                                  upgrade_layers=layers)
            lineage = {**ckpt["lineage"], "decoder_variant": "multilayer_v2"}
            if ckpt.get("bundle_schema") != BUNDLE_SCHEMA:
                lineage.update(validated=False, upgraded_from_v1_bundle_sha256=sha256_file(args.checkpoint),
                               upgrade_function="exact v1 warm start through zero intermediate residual gates")
            if getattr(args, "multilayer_decoder", None):
                from integrated_multilayer import DECODER_SCHEMA, validate_feature_metadata
                decoder = torch.load(args.multilayer_decoder, map_location="cpu", weights_only=False)
                if decoder.get("decoder_schema") != DECODER_SCHEMA:
                    raise ValueError("--multilayer-decoder requires a genuine v2 decoder checkpoint")
                spec = validate_feature_metadata(decoder["cache_metadata"], model.block_indices)
                expected_bundle = spec["encoder"].get("bundle_sha256")
                if expected_bundle and expected_bundle != sha256_file(args.checkpoint):
                    raise ValueError("The decoder cache was encoded by a different full model bundle")
                if spec["encoder"]["base_sha256"] != lineage["base_encoder_sha256"] or spec["encoder"].get("init_sha256") != lineage.get("mil_init_sha256"):
                    raise ValueError("Multilayer decoder and study encoder ancestry differ")
                model.mask_decoder.load_state_dict(decoder["decoder"], strict=True)
                lineage.update(multilayer_decoder_sha256=sha256_file(args.multilayer_decoder),
                               multilayer_feature_fingerprint=decoder["feature_fingerprint"], validated=False)
            return model, lineage, ckpt.get("physiology")
        groups = ckpt["groups"]
        # Full bundle is self-contained: no second encoder artifact is read.
        enc = load_encoder(None, "cpu")
        model = IntegratedStudyModel(enc, groups, args.train_blocks, ckpt["decoder_width"])
        if ckpt.get("physiology"):
            from physiology_head import PhysiologyHead
            model.physiology_head = PhysiologyHead(**ckpt["physiology"])
        model.load_state_dict(ckpt["state_dict"], strict=True)
        return model.to(args.device), ckpt["lineage"], ckpt.get("physiology")
    groups = tuple(args.groups.split(","))
    enc = load_encoder(args.echojepa, "cpu")
    dec = torch.load(args.decoder, map_location="cpu", weights_only=False)
    model = IntegratedStudyModel(enc, groups, args.train_blocks, dec["decoder_width"])
    old_groups = tuple(args.init_groups.split(","))
    if args.mil_init:
        model.warm_start_mil2(args.mil_init, old_groups)
    metadata = dec["cache_metadata"]
    base_hash = sha256_file(args.echojepa)
    init_hash = sha256_file(args.mil_init) if args.mil_init else None
    if metadata["base_encoder_sha256"] != base_hash or metadata.get("encoder_init_sha256") != init_hash:
        raise ValueError("The decoder was trained on another encoder. Re-cache using the same --encoder-init as --mil-init.")
    model.mask_decoder.load_state_dict(dec["decoder"], strict=True)
    physiology = None
    if args.physiology:
        from physiology_head import PhysiologyHead
        indices = tuple(groups.index(g) for g in ("left_main", "prox_lad", "mid_lad", "dist_lad", "D1", "D2"))
        physiology = dict(feature_dim=len(indices) * enc.embed_dim,
                          geometry_dim=getattr(args, "geometry_dim", 0), group_indices=indices)
        model.physiology_head = PhysiologyHead(**physiology)
    lineage = {"base_encoder_sha256": base_hash, "mil_init_sha256": init_hash,
               "decoder_sha256": sha256_file(args.decoder), "decoder_cache": metadata,
               "deployment": "one shared encoder, own masks/crops, uniform raw-video sampling; <=2 encoder passes/study",
               "validated": False}
    return model.to(args.device), lineage, physiology


def save_model(model, path, lineage, physiology_config, args, **extra):
    payload = {"state_dict": {k: v.detach().cpu() for k, v in model.state_dict().items()},
                "groups": model.groups, "decoder_width": model.mask_decoder.width,
                "lineage": lineage, "physiology": physiology_config,
                "args": vars(args), **extra}
    if hasattr(model, "block_indices"):
        from integrated_multilayer import BUNDLE_SCHEMA
        payload.update(bundle_schema=BUNDLE_SCHEMA, block_indices_zero_based=list(model.block_indices))
    temporary = Path(str(path)+".tmp")
    torch.save(payload, temporary)
    temporary.replace(path)


def forward(model, clips, args):
    with amp(args.device):
        return model(clips, full_res=args.full_res, crop_res=args.crop_res,
                     max_full_views=args.max_views, crops_per_group=args.crops_per_group)


def measured_forward(model, clips, args):
    encoder_calls = []
    handle = model.enc.register_forward_hook(lambda module, inputs, outputs: encoder_calls.append(1))
    try:
        result = forward(model, clips, args)
    finally:
        handle.remove()
    if len(encoder_calls) > 2 or len(encoder_calls) != result["encoder_passes"]:
        raise RuntimeError(f"Study pass budget exceeded or misreported: {len(encoder_calls)} measured encoder calls")
    result["measured_encoder_calls"] = len(encoder_calls)
    return result


@torch.inference_mode()
def predictions(model, rows, args, save_features=False):
    model.eval()
    dataset = StudyClips(rows, model.groups, max_views=args.max_views, sampling=getattr(args, "frame_sampling", "uniform16"))
    loader = DataLoader(dataset, batch_size=1, collate_fn=one_study, num_workers=args.workers)
    out, audit, features = [], [], []
    for i, sample in enumerate(loader):
        start = time.monotonic()
        result = measured_forward(model, sample["clips"], args)
        pred = {"StudyInstanceUID": sample["uid"]}
        for g, s, p in zip(model.groups, result["severity"].float().cpu().numpy(), result["binary_probability"].float().cpu().numpy()):
            pred[f"{g}_sev"] = float(s)
            pred[f"{g}_bin"] = float(p)
        if "physiology" in result:
            pred.update({k: float(v.float().cpu().reshape(-1)[0]) for k, v in result["physiology"].items()})
        out.append(pred)
        audit.append({"StudyInstanceUID": sample["uid"], "encoder_passes": result["encoder_passes"],
                      "measured_encoder_calls": result["measured_encoder_calls"],
                      "full_views": min(len(sample["clips"]), args.max_views), "own_mask_crops": len(result["crop_specs"]),
                      "seconds": time.monotonic() - start, "video_provenance": json.dumps(sample["video_provenance"])})
        if i == 0:
            study_mask_image(result, sample["clips"][0], Path(args.out) / "study_model_masks.png")
        if save_features:
            features.append(result["study_features"].float().cpu().numpy())
        if i % 25 == 0:
            print(f"predict {i+1}/{len(rows)}: { {k:v for k,v in audit[-1].items() if k != 'video_provenance'} }", flush=True)
    table = pd.DataFrame(out).set_index("StudyInstanceUID")
    return table, pd.DataFrame(audit), np.stack(features) if features else None


def safe_r(y, p):
    if len(y) < 3 or np.std(y) == 0 or np.std(p) == 0:
        return float("nan")
    return float(pearsonr(y, p).statistic)


def metrics(pred, rows, groups, bootstraps=0, seed=17):
    rows = rows.set_index("uid").reindex(pred.index)
    patients = rows.patient_id_anon.values
    uniq = np.unique(patients)
    indices = {p: np.flatnonzero(patients == p) for p in uniq}
    rng = np.random.default_rng(seed)
    bootstrap = [np.concatenate([indices[p] for p in rng.choice(uniq, len(uniq), replace=True)]) for _ in range(bootstraps)]
    result = []
    for group in groups:
        y = rows[f"{group}_stenosis"].values.astype(float)
        s, p = pred[f"{group}_sev"].values, pred[f"{group}_bin"].values
        valid = np.isfinite(y) & np.isfinite(s) & np.isfinite(p)
        binary = y >= (50 if group == "left_main" else 70)
        auc = float(roc_auc_score(binary[valid], p[valid])) if len(np.unique(binary[valid])) == 2 else float("nan")
        present = valid & (y > 0)
        row = {"segment": group, "n": int(valid.sum()), "missing_or_nonfinite_n": int((~valid).sum()),
               "patients": len(np.unique(patients[valid])), "positive_n": int(binary[valid].sum()),
               "r": safe_r(y[valid], s[valid]), "r_stenosis_present": safe_r(y[present], s[present]),
               "stenosis_present_n": int(present.sum()), "mae": float(np.abs(y[valid]-s[valid]).mean()), "auroc": auc}
        if bootstrap:
            rs, aucs, present_rs = [], [], []
            for ind in bootstrap:
                ind = ind[valid[ind]]
                rs.append(safe_r(y[ind], s[ind]))
                pos = ind[y[ind] > 0]
                present_rs.append(safe_r(y[pos], s[pos]))
                if len(np.unique(binary[ind])) == 2:
                    aucs.append(roc_auc_score(binary[ind], p[ind]))
            for name, values in (("r", rs), ("auroc", aucs), ("r_stenosis_present", present_rs)):
                finite = np.asarray(values)[np.isfinite(values)]
                row[f"{name}_ci_low"], row[f"{name}_ci_high"] = np.percentile(finite, [2.5, 97.5]) if len(finite) else (np.nan, np.nan)
        result.append(row)
    return pd.DataFrame(result)


def study_mask_image(result, clip, path):
    import matplotlib
    matplotlib.use("Agg")
    import matplotlib.pyplot as plt
    pred = result["mask_logits"][0].float().argmax(1).detach().cpu().numpy()
    fig, axes = plt.subplots(2, 4, figsize=(12, 6))
    for j, frame in enumerate((0, 5, 10, 15)):
        image = F.interpolate(clip[frame:frame+1, None], pred.shape[-2:], mode="bilinear", align_corners=False)[0, 0].cpu().numpy()
        axes[0, j].imshow(image, cmap="gray")
        axes[1, j].imshow(image, cmap="gray")
        axes[1, j].imshow(np.ma.masked_where(pred[frame] == 0, pred[frame]), cmap="turbo", vmin=0, vmax=25, alpha=.7)
        axes[0, j].set_title(f"Raw frame {frame}")
        axes[1, j].set_title("Model mask (no manual reference)")
        axes[0, j].axis("off")
        axes[1, j].axis("off")
    fig.tight_layout()
    fig.savefig(path, dpi=125)
    plt.close(fig)


def configure_study_training(model, args, mask_data_available):
    model.mask_decoder.requires_grad_(bool(args.train_mask_decoder and mask_data_available))
    if hasattr(model.mask_decoder, "intermediate_proj"):
        # Keep the proven v1 body fixed unless requested, while always learning
        # the new spatial-layer adapters and their initially-zero gates.
        model.mask_decoder.intermediate_proj.requires_grad_(True)
        model.mask_decoder.intermediate_gate.requires_grad_(True)
    if args.train_blocks:
        model.enc.use_activation_checkpointing = args.activation_checkpointing
    encoder_params = [p for n,p in model.named_parameters() if p.requires_grad and n.startswith("enc.")]
    decoder_params = [p for n,p in model.named_parameters() if p.requires_grad and n.startswith("mask_decoder.")]
    head_params = [p for n,p in model.named_parameters() if p.requires_grad and not n.startswith(("enc.", "mask_decoder."))]
    parameter_groups = []
    for params, rate in ((encoder_params, args.encoder_lr or args.lr),
                         (decoder_params, args.decoder_lr or args.lr), (head_params, args.head_lr or args.lr)):
        if params:
            parameter_groups.append({"params": params, "lr": rate})
    optimizer = torch.optim.AdamW(parameter_groups, weight_decay=.01)
    return optimizer, [p for p in model.parameters() if p.requires_grad]


def train(model, lineage, physiology_config, args, out, run, cohort=None):
    if not args.smoke and cohort is None:
        raise ValueError("Non-smoke joint training requires an explicitly frozen canonical cohort")
    tr = study_rows(args.train_videos, "train", model.groups, cohort=cohort)
    va = study_rows(args.val_videos, "val", model.groups, cohort=cohort)
    if args.train_bags_manifest:
        from integrated_inputs import with_frozen_input_bags
        tr, _ = with_frozen_input_bags(tr, args.train_bags_manifest, args.max_views, cohort.attrs["sha256"] if cohort is not None else None)
    if args.validation_bags_manifest:
        from integrated_inputs import with_frozen_input_bags
        va, _ = with_frozen_input_bags(va, args.validation_bags_manifest, args.max_views, cohort.attrs["sha256"] if cohort is not None else None)
        args.validation_input_protocol = "frozen_bags"
    elif args.validation_input_protocol == "raw_diagnostic":
        from integrated_inputs import with_authoritative_diagnostic_inputs
        va, _ = with_authoritative_diagnostic_inputs(va)
    if args.train_limit:
        tr = tr.iloc[:args.train_limit]
    if args.val_limit:
        va = va.iloc[:args.val_limit]
    if set(tr.patient_id_anon) & set(va.patient_id_anon):
        raise ValueError("Train/validation patient overlap")
    if not len(tr) or not len(va):
        raise ValueError("No patient-disjoint train/validation studies")
    manifest = {"state": "training", "train_studies": len(tr), "val_studies": len(va), "train_patients": tr.patient_id_anon.nunique(),
                "val_patients": va.patient_id_anon.nunique(), "test_used_for_selection": False,
                "missing_report_segment": "masked loss and excluded from that segment's metrics", "lineage": lineage,
                "train_cohort_audit": tr.attrs.get("cohort_audit"), "val_cohort_audit": va.attrs.get("cohort_audit"),
                "validation_input_protocol": args.validation_input_protocol,
                "validation_raw_input_audit": va.attrs.get("raw_input_audit"),
                "training_frozen_input_bags": tr.attrs.get("frozen_input_bags"),
                "validation_frozen_input_bags": va.attrs.get("frozen_input_bags"),
                "train_uids": tr.uid.astype(str).tolist(), "val_uids": va.uid.astype(str).tolist()}
    (out / "manifest.json").write_text(json.dumps(manifest, indent=2))
    loader = DataLoader(StudyClips(tr, model.groups, args.max_views, sampling=getattr(args, "frame_sampling", "uniform16")), batch_size=1,
                        collate_fn=one_study, shuffle=True, num_workers=args.workers)
    mask_data = CachedMasks(args.mask_manifest, "train", augment=True) if args.mask_manifest else None
    if mask_data is not None and cohort is not None:
        allowed_train = set(cohort.index[cohort.eligible_corrected_train].astype(str))
        mask_data.rows = [r for r in mask_data.rows if r["source"] == "arcade" or str(r.get("uid")) in allowed_train]
        if not len(mask_data):
            raise ValueError("No eligible gold or canonical-training mask samples")
    if args.train_blocks and mask_data is None:
        raise ValueError("Encoder fine-tuning needs --mask-manifest to retain segmentation supervision")
    optimizer, trainable = configure_study_training(model, args, mask_data is not None)
    threshold = torch.tensor([.5 if g == "left_main" else .7 for g in model.groups], device=args.device)
    class_weight = torch.ones(26, device=args.device)
    class_weight[0], class_weight[list(DISTAL_CLASSES)] = .15, 3
    phys = None
    if args.physiology_labels:
        if model.physiology_head is None:
            raise ValueError("Physiology labels require an attached --physiology head")
        phys = pd.read_parquet(args.physiology_labels)
        required = {"StudyInstanceUID", "value", "kind", "Split", "patient_id_anon"}
        if not required.issubset(phys):
            raise ValueError(f"Physiology labels require columns {sorted(required)}")
        phys = phys[(phys.Split == "train") & phys.StudyInstanceUID.isin(tr.uid)]
        if cohort is not None:
            canonical_patient = tr.set_index("uid").patient_id_anon
            phys = phys.copy()
            phys["patient_id_anon_original"] = phys.patient_id_anon
            phys["patient_id_anon"] = phys.StudyInstanceUID.map(canonical_patient)
        if not set(phys.patient_id_anon).issubset(set(tr.patient_id_anon)):
            raise ValueError("Physiology patient provenance disagrees with study labels")
    best, history, best_saved = -float("inf"), [], False
    timings, graph_records, seen_studies = [], [], set()
    for epoch in range(args.epochs):
        model.train()
        if not args.train_blocks:
            model.enc.eval()
        if not args.train_mask_decoder:
            model.mask_decoder.eval()
        start, passes = time.monotonic(), []
        accumulator = ViewBoundedStudyBatch(optimizer, trainable, threshold,
                                            max_views=args.graph_view_budget, max_studies=args.study_batch_size,
                                            optimizer_studies=args.optimizer_studies, severity_loss=args.severity_loss,
                                            ccc_weight=args.ccc_weight, binary_weight=args.binary_weight)
        effective_steps = min(len(loader), args.steps_per_epoch) if args.steps_per_epoch else len(loader)
        for step, sample in enumerate(loader):
            step_start = time.monotonic()
            full_views = min(len(sample["clips"]), args.max_views)
            accumulator.before_study(full_views)
            result = measured_forward(model, sample["clips"], args)
            y = sample["severity"].to(args.device)
            aux = result["severity"].new_zeros((), dtype=torch.float32)
            if phys is not None:
                from physiology_head import physiology_loss
                q = phys[phys.StudyInstanceUID == sample["uid"]]
                if len(q):
                    # One image forward, supervise every observed modality row.
                    pred = {k: v.expand(len(q)) for k, v in result["physiology"].items()}
                    aux = aux + args.physiology_weight * physiology_loss(pred, torch.tensor(q.value.to_numpy(float), device=args.device),
                                                                           torch.tensor(q.kind.eq("FFR").to_numpy(bool), device=args.device))
            if mask_data is not None and step % args.mask_every == 0:
                mloss, _ = mask_retention_loss(model, mask_data[np.random.randint(len(mask_data))], args.device, class_weight, amp)
                aux = aux + args.mask_weight * mloss
            accumulator.add(result["severity"].float()/100, result["binary_logits"].float(), y,
                            aux, full_views, uid=sample["uid"])
            passes.append(result["encoder_passes"])
            seen_studies.add(sample["uid"])
            if step+1 == effective_steps:
                accumulator.finish()
            if step == 0:
                study_mask_image(result, sample["clips"][0], out / "train_masks.png")
            if step % 25 == 0:
                if accumulator.records:
                    print(f"epoch {epoch} step {step} loss {np.mean([r['loss'] for r in accumulator.records]):.4f}", flush=True)
                else:
                    print(f"epoch {epoch} step {step}, retaining {len(accumulator.pending)} graphs within {args.graph_view_budget} views", flush=True)
            if str(args.device).startswith("cuda"):
                torch.cuda.synchronize(args.device)
            timings.append({"epoch": epoch, "step": step, "uid": sample["uid"], "full_views": full_views,
                            "crops": len(result["crop_specs"]), "measured_encoder_calls": result["measured_encoder_calls"],
                            "seconds": time.monotonic()-step_start,
                            "peak_allocated_gib": torch.cuda.max_memory_allocated(args.device)/1024**3 if str(args.device).startswith("cuda") else 0})
            del result, aux
            if step+1 >= effective_steps:
                break
        loss_parts = accumulator.records
        graph_records.extend({"epoch": epoch, **r} for r in loss_parts)
        pd.DataFrame(timings).to_csv(out / "training_timings.csv", index=False)
        pd.DataFrame(graph_records).to_csv(out / "graph_batches.csv", index=False)
        (out / "seen_train_studies.json").write_text(json.dumps({"unique_studies": len(seen_studies),
                                                                 "study_visits": len(timings), "uids": sorted(seen_studies)}, indent=2))
        pv, audit, _ = predictions(model, va, args)
        m = metrics(pv, va, model.groups)
        distal = m[m.segment.isin(("dist_lad", "dist_lcx", "pda", "posterolateral"))]
        score = float(distal.r.mean()) if len(distal) and np.isfinite(distal.r).all() else float("nan")
        log = {"epoch": epoch, "train/loss": float(np.mean([r["loss"] for r in loss_parts])), "val/distal_mean_r": score,
               "val/all_segment_mean_r": float(m.r.mean()),
               "val/valid_distal_segments": int(np.isfinite(distal.r).sum()),
               **{f"train/{k}": float(np.mean([p[k] for p in loss_parts])) for k in ("mae", "ccc_loss", "ccc_valid_segments", "bce", "auxiliary")},
               "train/graph_batches": len(loss_parts), "train/singleton_graph_batches": sum(r["studies"] == 1 for r in loss_parts),
               "train/max_graph_views": max(r["full_views"] for r in loss_parts), "train/optimizer_updates": accumulator.optimizer_updates,
               "train/unique_studies_seen_total": len(seen_studies),
               "val/macro_auroc": float(m.auroc.mean()), "seconds": time.monotonic()-start,
               "encoder_passes_max": max(passes), **{f"val/r_{r.segment}": r.r for r in m.itertuples()}}
        history.append(log)
        pd.DataFrame(history).to_csv(out / "history.csv", index=False)
        epoch_checkpoint = out/f"epoch_{epoch:02d}.pt"
        epoch_dir = out/f"epoch_{epoch:02d}_val"
        epoch_dir.mkdir(exist_ok=True)
        save_model(model, epoch_checkpoint, lineage, physiology_config, args, epoch=epoch, val_score=score)
        pv.to_parquet(epoch_dir/"val_predictions.parquet")
        audit.to_csv(epoch_dir/"val_pass_audit.csv", index=False)
        m.to_csv(epoch_dir/"val_metrics.csv", index=False)
        write_complete_manifest(epoch_dir/"manifest.json", epoch_dir/"val_predictions.parquet", audit,
                                 epoch_checkpoint, args.validation_input_protocol,
                                 lineage=lineage, split="val", base={"epoch": epoch, "target_gates_claimed": False})
        if score > best:
            best = score
            best_saved = True
            lineage = {**lineage, "validated": not args.smoke,
                       "study_severity_loss": args.severity_loss, "ccc_weight": args.ccc_weight,
                       "study_batch_size": args.study_batch_size, "smoke_only": args.smoke,
                       "validation_scope": "validation studies listed in manifest; target gates unclaimed"}
            save_model(model, out / "best.pt", lineage, physiology_config, args, epoch=epoch, val_score=score)
            shutil.copyfile(epoch_dir/"val_predictions.parquet", out/"val_predictions.parquet")
            audit.to_csv(out / "val_pass_audit.csv", index=False)
            m.to_csv(out / "val_metrics.csv", index=False)
            if (out/"study_model_masks.png").exists():
                shutil.copyfile(out/"study_model_masks.png", out/"selected_val_masks.png")
        if run:
            import wandb
            run.log(log | {"train/masks": wandb.Image(str(out / "train_masks.png"))})
        print(json.dumps(log), flush=True)
    if not best_saved:
        raise RuntimeError("No checkpoint selected: every configured distal segment needs a finite validation r; use a validation cohort with observed variation")
    selected_audit = pd.read_csv(out/"val_pass_audit.csv")
    write_complete_manifest(out/"manifest.json", out/"val_predictions.parquet", selected_audit, out/"best.pt",
                             args.validation_input_protocol, lineage=lineage, split="val", base=manifest)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("command", choices=("train", "predict", "export"))
    p.add_argument("--out", required=True)
    p.add_argument("--checkpoint")
    p.add_argument("--cohort-manifest", help="Frozen canonical identity/exposure parquet; mandatory for non-smoke training")
    p.add_argument("--cohort-sha256", help="Expected frozen cohort SHA256; mandatory for non-smoke training")
    p.add_argument("--decoder")
    p.add_argument("--decoder-variant", choices=("v1", "multilayer_v2"), default="v1")
    p.add_argument("--multilayer-blocks", default="5,11,17")
    p.add_argument("--multilayer-decoder", help="Attach a trained v2 decoder to the explicitly matching encoder bundle")
    p.add_argument("--echojepa", default="../frozen/echojepa/ej_ft_ep15_encoder.pt")
    p.add_argument("--mil-init", default="ft_all18w_c/best.pt")
    p.add_argument("--groups", default=",".join(ALL_SEGMENTS))
    p.add_argument("--init-groups", default=",".join(ALL_SEGMENTS))
    p.add_argument("--physiology", action="store_true")
    p.add_argument("--geometry-dim", type=int, choices=(0, 38), default=0,
                   help="For new physiology-head exports; 38 uses deterministic own-mask measurements")
    p.add_argument("--physiology-labels")
    p.add_argument("--physiology-weight", type=float, default=1)
    p.add_argument("--mask-manifest")
    p.add_argument("--mask-weight", type=float, default=.2)
    p.add_argument("--mask-every", type=int, default=4)
    p.add_argument("--train-mask-decoder", action="store_true", help="Otherwise retain the pretrained decoder weights while its loss supervises the shared encoder")
    p.add_argument("--train-videos", nargs="+")
    p.add_argument("--val-videos", nargs="+")
    p.add_argument("--videos", nargs="+")
    p.add_argument("--study-uids", help="StudyInstanceUID parquet for raw input inference; no preselected filenames needed")
    p.add_argument("--bags-manifest", help="Completed/frozen shared literal input bags; no runtime refill")
    p.add_argument("--train-bags-manifest")
    p.add_argument("--validation-bags-manifest")
    p.add_argument("--input-protocol", choices=("provided_videos", "raw_diagnostic", "frozen_bags"), default="provided_videos",
                   help="Predict: retain legacy provided-file references or explicitly assemble all authoritative diagnostic videos")
    p.add_argument("--validation-input-protocol", choices=("provided_videos", "raw_diagnostic", "frozen_bags"), default="provided_videos")
    p.add_argument("--split", choices=("train", "val", "test"), default="val")
    p.add_argument("--device", default="cuda:0")
    p.add_argument("--train-blocks", type=int, default=0)
    p.add_argument("--epochs", type=int, default=3)
    p.add_argument("--study-batch-size", type=int, default=2,
                   help="Maximum retained study graphs; total views also bound the batch")
    p.add_argument("--graph-view-budget", type=int, default=12)
    p.add_argument("--optimizer-studies", type=int, default=8,
                   help="Accumulate freed graph-batch gradients over this many equally weighted studies")
    p.add_argument("--activation-checkpointing", action=argparse.BooleanOptionalAction, default=True)
    p.add_argument("--severity-loss", choices=("mae_ccc", "mae", "mse"), default="mae_ccc")
    p.add_argument("--ccc-weight", type=float, default=.1)
    p.add_argument("--binary-weight", type=float, default=.3)
    p.add_argument("--smoke", action="store_true", help="Label engineering-only training; never qualify it as a target result")
    p.add_argument("--steps-per-epoch", type=int, default=0)
    p.add_argument("--lr", type=float, default=1e-4)
    p.add_argument("--encoder-lr", type=float)
    p.add_argument("--head-lr", type=float)
    p.add_argument("--decoder-lr", type=float)
    p.add_argument("--full-res", type=int, default=224)
    p.add_argument("--crop-res", type=int, default=224)
    p.add_argument("--max-views", type=int, default=12)
    p.add_argument("--crops-per-group", type=int, default=1)
    p.add_argument("--train-limit", type=int, default=0)
    p.add_argument("--val-limit", type=int, default=0)
    p.add_argument("--limit", type=int, default=0)
    p.add_argument("--bootstrap", type=int, default=0)
    p.add_argument("--workers", type=int, default=2)
    p.add_argument("--threads", type=int, default=4)
    p.add_argument("--memory-gb", type=float, default=16)
    p.add_argument("--seed", type=int, default=0)
    p.add_argument("--save-features", action="store_true")
    p.add_argument("--wandb", action="store_true")
    args = p.parse_args()
    if args.command == "train" and not args.smoke and (not args.cohort_manifest or not args.cohort_sha256):
        p.error("Non-smoke joint training requires --cohort-manifest and --cohort-sha256")
    if args.command == "train" and (args.study_batch_size < 1 or args.study_batch_size > 4):
        p.error("--study-batch-size must be between 1 and 4 for bounded graph accumulation")
    if args.command == "train" and (args.graph_view_budget < args.max_views or args.optimizer_studies < args.study_batch_size or args.mask_every < 1):
        p.error("Graph view budget must cover a complete capped study; optimizer_studies must cover a graph batch; mask_every must be positive")
    if args.command == "train" and args.severity_loss == "mae_ccc" and args.ccc_weight > 0 and args.study_batch_size < 2:
        p.error("CCC requires --study-batch-size at least 2; use --severity-loss mae or mse for single studies")
    if not args.checkpoint and not args.decoder:
        p.error("Provide --checkpoint or --decoder")
    torch.set_num_threads(args.threads)
    np.random.seed(args.seed)
    torch.manual_seed(args.seed)
    from integrated_cache import memory_limit
    memory_limit(args.device, args.memory_gb)
    cohort = None
    if args.cohort_manifest:
        from experiment_protocol import load_canonical_cohort
        cohort = load_canonical_cohort(args.cohort_manifest, args.cohort_sha256)
    model, lineage, physiology_config = load_model(args)
    prior_cohort_hash = lineage.get("canonical_cohort_sha256")
    if args.command == "predict" and prior_cohort_hash:
        if cohort is None or cohort.attrs["sha256"] != prior_cohort_hash:
            raise ValueError("Prediction must use the same explicitly frozen canonical cohort as joint training")
    if cohort is not None:
        lineage = {**lineage, "canonical_cohort_path": cohort.attrs["path"],
                   "canonical_cohort_sha256": cohort.attrs["sha256"],
                   "canonical_identity_policy_applied": True,
                   "ancestry_audit_complete": cohort.attrs["ancestry_audit_complete"],
                   "defensible_cohort_verified": bool(cohort.attrs["ancestry_audit_complete"])}
    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    run = None
    if args.wandb:
        import wandb
        run = wandb.init(entity="mhi_ai", project="deepcoro-segmentation", group="step3-seg-aware", name=out.name,
                         job_type=f"integrated-study-{args.command}", config=vars(args) | {"lineage": lineage})
        (out / "wandb_url.txt").write_text(run.url + "\n")
    if args.command == "export":
        save_model(model, out / "model.pt", lineage, physiology_config, args)
        (out / "manifest.json").write_text(json.dumps({"lineage": lineage, "groups": model.groups, "validated": False}, indent=2))
        print(f"Exported one complete model to {out / 'model.pt'}; unvalidated warm start", flush=True)
    elif args.command == "train":
        train(model, lineage, physiology_config, args, out, run, cohort=cohort)
    else:
        if (out/"manifest.json").exists() or (out/f"{args.split}_predictions.parquet").exists():
            raise ValueError("Inference outputs are immutable; use a new output directory/version")
        if args.bags_manifest:
            if args.input_protocol == "raw_diagnostic":
                raise ValueError("Literal frozen bags cannot be combined with fresh raw-file reassembly")
            from integrated_inputs import with_frozen_input_bags
            bag_payload = json.loads(Path(args.bags_manifest).read_text())
            videos = pd.DataFrame([{"StudyInstanceUID": case["StudyInstanceUID"], "FileName": f}
                                   for case in bag_payload["studies"] for f in case["FileNames"]])
            rows = study_rows(videos, args.split, model.groups, cohort=cohort)
            rows, _ = with_frozen_input_bags(rows, args.bags_manifest, args.max_views, cohort.attrs["sha256"] if cohort is not None else None)
            args.input_protocol = "frozen_bags"
        elif args.study_uids:
            if args.input_protocol != "raw_diagnostic":
                raise ValueError("--study-uids requires the explicit raw_diagnostic input protocol")
            from integrated_inputs import authoritative_diagnostic_videos
            requested = pd.read_parquet(args.study_uids).StudyInstanceUID
            videos, _ = authoritative_diagnostic_videos(requested)
            rows = study_rows(videos, args.split, model.groups, cohort=cohort)
        else:
            rows = study_rows(args.videos, args.split, model.groups, cohort=cohort)
        if args.input_protocol == "raw_diagnostic":
            from integrated_inputs import with_authoritative_diagnostic_inputs
            rows, _ = with_authoritative_diagnostic_inputs(rows)
        if args.limit:
            rows = rows.iloc[:args.limit]
        (out/"manifest.json").write_text(json.dumps({"state": "running", "input_protocol": args.input_protocol,
                                                       "checkpoint_sha256": sha256_file(args.checkpoint) if args.checkpoint else None,
                                                       "canonical_cohort_sha256": lineage.get("canonical_cohort_sha256")}, indent=2))
        pred, audit, features = predictions(model, rows, args, args.save_features)
        m = metrics(pred, rows, model.groups, args.bootstrap)
        pred.to_parquet(out / f"{args.split}_predictions.parquet")
        audit.to_csv(out / f"{args.split}_pass_audit.csv", index=False)
        m.to_csv(out / f"{args.split}_metrics.csv", index=False)
        if features is not None:
            np.savez_compressed(out / f"{args.split}_features.npz", features=features, uids=pred.index.to_numpy(str), groups=np.array(model.groups))
        manifest = {"lineage": lineage, "checkpoint_sha256": sha256_file(args.checkpoint) if args.checkpoint else None,
                    "args": vars(args), "n_studies": len(rows), "n_patients": rows.patient_id_anon.nunique(),
                    "max_encoder_passes": int(audit.encoder_passes.max()), "macro_auroc": float(m.auroc.mean()),
                    "peak_cuda_allocated_gb": torch.cuda.max_memory_allocated(args.device)/1024**3 if str(args.device).startswith("cuda") else 0,
                    "target_gate_claim_eligible": bool(lineage.get("validated")) and bool(lineage.get("defensible_cohort_verified")) and not bool(args.limit),
                    "sampling": "uniform16 across native videos; native FPS is recorded, 15 FPS normalization is NOT established",
                    "cohort_audit": rows.attrs.get("cohort_audit"),
                    "input_protocol": args.input_protocol, "raw_input_audit": rows.attrs.get("raw_input_audit"),
                    "frozen_input_bags": rows.attrs.get("frozen_input_bags"),
                    "video_provenance": [{"StudyInstanceUID": r.StudyInstanceUID, "videos": json.loads(r.video_provenance)} for r in audit.itertuples()]}
        write_complete_manifest(out/"manifest.json", out/f"{args.split}_predictions.parquet", audit, args.checkpoint,
                                 args.input_protocol, lineage=lineage, split=args.split, base=manifest)
        if run:
            run.log({"metrics": __import__("wandb").Table(dataframe=m), "macro_auroc": float(m.auroc.mean()),
                     "max_encoder_passes": int(audit.encoder_passes.max()),
                     "model_masks": __import__("wandb").Image(str(out / "study_model_masks.png"))})
        print(m.to_string(index=False), flush=True)
    if run:
        run.finish()


if __name__ == "__main__":
    main()
