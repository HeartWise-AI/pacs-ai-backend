# Angiogram procedure classifier 1.0.2

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
