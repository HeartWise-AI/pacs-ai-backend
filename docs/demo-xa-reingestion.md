# Clean XA demo reingestion

`scripts/demo_xa_reingest.py` turns the XA demo refresh into a repeatable,
fail-closed operation. It discovers XA ingestion jobs dynamically; it does not
hard-code a model list.

## One-time setup

The staging host's system Python is too old for this runner. Create the local
Python 3.12 environment with `uv` and install the pinned dependency:

```bash
uv venv --python 3.12 .venv
uv pip install --python .venv/bin/python -r scripts/requirements_demo_xa_reingest.txt
cp scripts/.env.demo-xa.example scripts/.env.demo-xa
chmod 600 scripts/.env.demo-xa
```

Fill in `scripts/.env.demo-xa`. The source Orthanc is the canonical input because
it backs the demo website's `PACS_QUERY` worklist: the script discovers every
study that contains an XA series and snapshots the complete study, including any
associated DOC or other non-XA series. No local seed directory is needed.
Run the command on the staging host from this repository so Docker Compose can
inspect the live study-service registry, workers, queues, and—only when
explicitly authorized—delete exact-study database history.

## Routine use

Always start with the read-only preflight:

```bash
make demo-xa-reingest-dry-run
```

The preflight checks API authentication, both Orthanc endpoints, study-service,
every registered XA model endpoint, exact model name/version routing, and a live
Celery consumer for each routed queue. It inventories all XA studies in the
PACS_QUERY source Orthanc and reports the total expected model-study results. It
does not stop jobs, download or write DICOMs, upload, or delete anything. Mixed
studies are counted and later replayed in full so whole-study cleanup does not
discard their associated non-XA series.

For a clean cutover after reviewing the plan:

```bash
make demo-xa-reingest EXECUTE=1
```

Without `EXECUTE=1`, `make demo-xa-reingest` performs the same read-only
preflight as the dedicated dry-run target.

This pauses only XA discovery jobs that were running, drains their queues, and
rechecks that the PACS_QUERY inventory has not changed. It then snapshots each
current PACS_QUERY XA study, creates new Study/Series/SOP UIDs, shifts DICOM dates
and times together while preserving relative ordering, replaces each demo
PatientID, verifies that pixel bytes are unchanged, uploads every replay to the
source PACS, restores the original discovery states, and waits for a completed
stored result from every originally-running XA model/version for every study.
Only after the complete model × study matrix passes does it remove the
snapshotted originals from both Orthancs and delete their exact-study pipeline
history.

To keep the previous study and history for comparison:

```bash
make demo-xa-reingest EXECUTE=1 KEEP_PREVIOUS=1
```

Cleanup uses only the exact StudyInstanceUID values captured from the PACS_QUERY
source Orthanc during that run; the script never performs a broad
modality, patient, or date-range deletion. If any study/model result fails,
every snapshotted original is retained. XA job states are restored from
`finally`, including on Ctrl-C.
`KEEP_PREVIOUS=1` retains both originals and replays, so it should only be used
for a deliberate comparison run; repeating it increases the next run's study
count.

## Evidence and recovery

Each execution writes a mode-0600 JSON manifest below `.demo-xa-runs/<run-id>/`.
It contains the input-study inventory, job/model/version snapshot, per-study UID
mappings, generated files, result correlations, cleanup actions, and terminal
state. Console output deliberately does not print DICOM UIDs, PatientID values,
passwords, or inference results.

If a run fails, inspect its manifest and service logs, fix the failing preflight
or model, then rerun the dry-run command. Failed newly-generated studies are left
in place for diagnosis and are never promoted to `latest-success.json`.
