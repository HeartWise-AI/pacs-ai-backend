# Public demo DICOM validation — aggregate summary

- Validation date: 2026-08-19
- Result: **FAIL (preliminary)**
- Policy: `pacs-ai-public-demo-dicom-deidentification` version `1.0-draft`
- Policy approval: `pending`
- Burned-in text review: `missing`
- Studies: 8
- Series: 88
- Instances: 172
- Metadata violations: 646
- Validation errors: 0

## Violation counts

| Code | Count |
|---|---:|
| `prohibited_tag_populated` | 183 |
| `required_tag_missing` | 275 |
| `required_value_mismatch` | 11 |
| `suspicious_text_pattern` | 5 |
| `value_outside_allowlist` | 172 |

## Blockers

- The de-identification policy has not been approved by a named reviewer.
- The required burned-in text review attestation is missing.

This summary is not launch approval. It intentionally omits dataset fingerprints,
instance references, DICOM values and UIDs, filenames, paths, URLs, credentials,
and patient, clinician, or institution identifiers. Detailed validation reports
and attestations are retained only in approved private storage.
