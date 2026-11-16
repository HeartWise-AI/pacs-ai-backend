# Angiogram procedure classifier 1.0.5

**1.0.5:** same weights, class mapping, pixel conversion, thresholds and labels as 1.0.4. Only the chronology
used for PCI/POST_PCI status changes, and only for studies where several videos share one SeriesTime (exported
or converted runs, e.g. "DA Image Converted", where every video carries the export's SeriesTime):
- the videos sharing a SeriesTime are ordered by their AcquisitionTime when every one of them has a complete,
  distinct value, else by their ContentTime under the same condition; otherwise they keep the shared SeriesTime
  and tied PCI timing still fails closed. Those predictions carry `chronology_time` and `chronology_source`
  (`AcquisitionTime` or `ContentTime`); `series_time` stays the video's own SeriesTime. Predictions of studies
  without shared SeriesTimes are unchanged (no new field);
- a consumer may send `additionalMetadata.ambiguousTimingStatus = "undetermined"`: tied PCI timing then returns
  every video's labels with `status: "undetermined"` and `status_reason: "ambiguous_acquisition_order"` (no video
  is diagnostic) instead of an error. Without that key the request still fails.
`preprocessingContract` advertises both (`chronologyTime`, `ambiguousTimingStatus`).

**1.0.4:** same weights, class mapping, pixel conversion, thresholds and predictions as 1.0.3 (bit-identical on a
37-video study). Two operational changes for complete studies, which arrive as one request:
- videos are prepared (DICOM decoding, resampling) on 8 CPU threads instead of one: 11 s instead of 35 s for 37
  videos. GPU forwards still take one video (`preprocessingContract.forwardBatchSize` stays 1, checked against the
  configuration at start-up). `Config.forward_batch_size` can raise it, but measured batches of 2 to 8 were not
  faster, needed 1.8 to 7.6 GiB of GPU memory instead of 0.8, and moved the dominance probability by up to 0.0015;
- the internal proxy (`nginx.conf`) waits 300 s for a prediction instead of nginx's default 60 s, the gateway's own
  prediction timeout. With 1.0.3 a 37-video study was cut off twice on MHI (2026-11-15).

The unchanged VasoVision checkpoint and class mapping are verified by SHA-256
before the API serves its preprocessing contract. Set `PACS_AI_SOURCE_REVISION`
to the immutable source commit when building/running a local candidate. The
metadata exposes `preprocessingContract` with that revision, actual weight and
class-mapping hashes, explicit SOP/series identity fields, one-video GPU forwards
and full-request procedure-status scope.

Send the complete set of multi-frame XA videos before selecting/truncating model
inputs. Every prediction retains legacy `dicom_name` (series UID) and adds
`series_instance_uid` and `sop_instance_uid`. Multiple instances in a series remain
distinct. GPU forwards use a batch of one, then PCI/POST_PCI chronology is computed
over the complete request. Complete SeriesTime with or without fractional seconds
is supported; missing/malformed time or ambiguous tied PCI timing fails closed.
Invalid DICOMs, incomplete labels or failed video classification never produce a
partial successful classification. Trained sampling, normalization, weights and
binary thresholds are unchanged.

Each video also carries `dominance` (`right_dominant` or `left_dominant`, the
unchanged >= 0.5 call) and, since 1.0.3, `dominance_left_probability`, the
sigmoid of that head. A study-level dominance belongs to the consumer: DeepCORO-JEPA
uses the majority call of the diagnostic coronary videos it is given.

For diagnostic coronary selection, consumers must require `status=diagnostic`,
`main_structure` equal to `Left Coronary` or `Right Coronary`,
`contrast_agent=yes` and `stent_presence=absent`. Unknown labels, non-coronary,
PCI and post-PCI videos are excluded. A consumer must filter SOP instances rather
than accepting an entire series because one instance was eligible.

The recovery candidate overlays this source on a verified immutable legacy image,
preserving its packaged checkpoint, configuration and architecture cache. It is
not an official image publication. Register and run it through the managed
inference gateway on exactly one configured physical GPU UUID, after lifecycle,
resource and synthetic-input validation. Do not publish an existing version tag
or replace registrations/history to deploy a candidate.

`Dockerfile.local` records that recovery build against the immutable base digest
and verifies its checkpoint, class mapping and normalization configuration before
copying application code. `compose.local.yml` supplies the stable service name,
external PACS network, one physical GPU and bounded logs. Set
`CLASSIFIER_SOURCE_REVISION` to the full checked-out commit,
`CLASSIFIER_LOCAL_IMAGE` to a visibly local versioned tag, and
`CLASSIFIER_GPU_UUID` to the selected physical device; then run
`docker compose -f compose.local.yml build` from this directory. Never publish
this local artifact as an official semantic-version release.

The official `Dockerfile` follows the release contract (`scripts/release-model.py`):
`data/model_info.json` `provenance` pins `heartwise/VasoVision` at commit
`af6db088eae690c6eb64eabf4baf65ad7f7744b0` (the revision of the deployed checkpoint,
`config.json` and `class_mapping.json`), and `download_model.py` downloads it with the
`hf_token` BuildKit secret and verifies every file by SHA-256. The pytorchvideo hub
repository is checked out at commit `7da857b418b0e05fc4005907038695b247219b3a` and the
Kinetics `X3D_M.pyth` checkpoint read by `x3d_m(pretrained=True)` is verified by SHA-256,
so the build makes no unpinned fetch. `download_model.py --verify-only` rechecks all of
them inside a built image.

For a new deployment, start and register the container through the normal managed
model workflow. An existing registration identifies its container: do not run
Compose recreation over that registered container without a coordinated model
registration update. In cardio-agent, `study-service/docker-compose.jepa-diagnostic.yaml`
enables selection on the HTTP service and default worker using the verified image
ID and source commit. JEPA's model Dockerfile is already maintained separately in
`model-examples/DeepCORO-JEPA`; the classifier is a preceding managed model.
