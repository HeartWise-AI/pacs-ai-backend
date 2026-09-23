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

Fill in `scripts/.env.demo-xa`. The seed directory must be an immutable copy of
exactly one XA study. The script never edits the seed files. Run the command on
the staging host from this repository so Docker Compose can inspect the live
study-service registry, workers, queues, and—only when explicitly authorized—
delete exact-study database history.

## Routine use

Always start with the read-only preflight:

```bash
make demo-xa-reingest-dry-run
```

The preflight checks API authentication, both Orthanc endpoints, study-service,
every registered XA model endpoint, exact model name/version routing, and a live
Celery consumer for each routed queue. It also validates the local seed. It does
not stop jobs, write DICOMs, upload, or delete anything.

For a clean cutover after reviewing the plan:

```bash
make demo-xa-reingest
```

This pauses only XA discovery jobs that were running, drains their queues,
creates new Study/Series/SOP UIDs, shifts DICOM dates and times together while
preserving relative ordering, replaces the demo PatientID, verifies that pixel
bytes are unchanged, uploads to the source PACS, restores the original discovery
states, and waits for a completed stored result from every originally-running XA
model/version. Only after all models pass does it remove the previous managed
study from both Orthancs and delete its exact-study pipeline history.

To keep the previous study and history for comparison:

```bash
make demo-xa-reingest KEEP_PREVIOUS=1
```

The first successful run uses the seed study UID as its cleanup target. Later
runs use only `.demo-xa-runs/latest-success.json`; the script never performs a
broad modality, patient, or date-range deletion. If processing fails, previous
data is retained. XA job states are restored from `finally`, including on Ctrl-C.

## Evidence and recovery

Each execution writes a mode-0600 JSON manifest below `.demo-xa-runs/<run-id>/`.
It contains the job/model/version snapshot, UID mapping, generated files, result
correlations, cleanup actions, and terminal state. Console output deliberately
does not print DICOM UIDs, PatientID values, passwords, or inference results.

If a run fails, inspect its manifest and service logs, fix the failing preflight
or model, then rerun the dry-run command. Failed newly-generated studies are left
in place for diagnosis and are never promoted to `latest-success.json`.
