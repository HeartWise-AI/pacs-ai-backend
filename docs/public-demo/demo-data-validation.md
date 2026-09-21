# Public demo DICOM de-identification validation

This procedure validates the DICOM objects currently stored in the public-demo
Orthanc instance. It does not ingest, replace, or modify studies.

The draft policy uses the
[DICOM PS3.15 Basic Application Confidentiality Profile](https://dicom.nema.org/medical/dicom/current/output/html/part15.html#chapter_E)
as its baseline. It is intentionally stricter in several places and must be
reviewed against the project's approved retention options before launch.

The validator checks every deployed DICOM instance for prohibited populated
attributes, project-approved pseudonymous Patient IDs, required de-identification
statements, suspicious free text, and private elements. Reports contain only
counts and one-way references; they intentionally omit DICOM values, UIDs,
filenames, URLs, and credentials.

## Before the first launch validation

1. Have the privacy/security owner review
   `dicom-deidentification-policy.json`. Update rules for explicitly approved
   retention options, then set `approval.status` to `approved`, provide the
   approver's name, set an ISO-8601 approval date, and replace the draft policy
   version.
2. Run the metadata scan once to obtain the dataset fingerprint. An unapproved
   policy and missing pixel review intentionally prevent `PASS`; a scan that
   also finds metadata violations reports `FAIL`.
3. Review representative frames from every series for burned-in patient,
   institution, and clinician information. Copy
   `pixel-review-attestation.example.json`, record the reviewer and exact dataset
   fingerprint, and store the completed attestation with the launch evidence.
4. Run the validator again with that attestation. Only an approved policy, a
   complete error-free scan, zero violations, and a matching passed pixel review
   can produce `PASS`.

`BurnedInAnnotation = NO` is not sufficient evidence on its own. The separate
pixel review is mandatory because the attribute can be incorrect or absent in
source data.

## Install

Python 3.10 or newer is required by the pinned `pydicom` release. Use an
isolated environment; do not install validation dependencies into the
application container:

```bash
python3 -m venv .venv-demo-validation
.venv-demo-validation/bin/pip install -r scripts/requirements_demo_data_validation.txt
```

## Validate the deployed Orthanc dataset

Run on the deployment host against Orthanc's private REST listener. Credentials
are read from environment variables and never accepted as command-line values:

```bash
export ORTHANC_USERNAME='operator-user'
export ORTHANC_PASSWORD='use-the-secret-store-value'

.venv-demo-validation/bin/python scripts/validate_demo_dicom.py \
  --orthanc-url http://127.0.0.1:8042 \
  --policy docs/public-demo/dicom-deidentification-policy.json \
  --pixel-review-attestation docs/public-demo/evidence/pixel-review-2026-08-19.json \
  --json-report docs/public-demo/evidence/dicom-validation-2026-08-19.json \
  --markdown-report docs/public-demo/evidence/dicom-validation-2026-08-19.md
```

If Orthanc is reachable only inside Docker, run through a temporary validation
container on the private network or create a short-lived loopback-only port
forward. Never expose the Orthanc REST API publicly.

For an approved local export, use `--path /path/to/export`. Local paths may
contain patient information, so the validator hashes paths and never includes
them in reports. The repository ignores DICOM files; never commit an export.

## Results and exit codes

- `0` / `PASS`: complete scan, approved policy, no violations, and current pixel review.
- `1` / `FAIL`: at least one prohibited or nonconforming value was detected,
  including when readiness blockers are also present.
- `2` / `INCOMPLETE`: no violations were found, but the scan has no instances,
  parse/download failures, pending policy approval, or missing/stale pixel review
  evidence.

Retain both detailed report formats and the matching pixel-review attestation in
approved private storage. They are ignored by Git and must never be committed to
the public repository. A report is valid only for its exact dataset fingerprint.
Adding, replacing, or modifying any instance requires a new scan and pixel review.

Only an aggregate summary may be committed publicly. It must omit dataset
fingerprints, instance references, DICOM values and UIDs, filenames, paths, URLs,
credentials, and patient, clinician, or institution identifiers.

## Remaining artifact review

This script validates DICOM objects. The launch owner must separately verify that
thumbnails, rendered previews, exported files, model-generated reports, logs,
caches, and backups do not contain identifying information. Record those checks
in the public-demo launch checklist before closing PACS-AI issue 399.
