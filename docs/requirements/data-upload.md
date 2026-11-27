# Data upload on the public demo: requirements

Status: draft for review. Date: 2026-11-27. Owner decisions are marked **[decided]**; anything
else is a proposal. Code references are to `api-pacs/` at `origin/master` `57547a8c` and to the
viewer repo `HeartWise-AI/PACS-AI` at `e1e8fc8`.

## 1. Why this exists

The website Terms (version 2026-11-27, live on pacsai.co) now tell demo users they can upload their
own de-identified DICOM studies. The backend has to make every promise in those Terms true. Today
several of them are not (section 9).

## 2. What the Terms already promise [decided]

| Promise                                                                                     | Where in the Terms   |
| ------------------------------------------------------------------------------------------- | -------------------- |
| Only de-identified data; the user removes identifiers; we do not de-identify for them        | s. 5, s. 7           |
| We may delete identifiable uploads immediately and suspend the account                       | s. 7                 |
| 5 GB of uploaded data per account, DICOM studies                                             | s. 7                 |
| Stored on AWS S3, Canada (Central), Montreal, encrypted at rest and in transit               | s. 7                 |
| Automatic, permanent deletion 30 days after upload; user can delete sooner; closing the account deletes everything | s. 7 |
| Never used to train, fine-tune or improve any model; never sold or shared                    | s. 7                 |
| Re-acceptance of Terms 2026-11-27 at next login                                              | s. 14                |
| Service emails to all users; mailing list only with express, unticked opt-in; unsubscribe in every message; consent record kept | s. 13 |

## 3. How Harrison.ai does it (benchmark)

Harrison runs two separate things:

- **Research demo (chat.harrison.ai):** browser upload, X-ray only, social login (Google or
  LinkedIn), no institutional check, one user tier. De-identification is a user promise in the
  terms, not an automatic check. Uploaded images are deleted at the end of the session and in any
  event within 30 days; generated drafts are kept 90 days. Inputs and outputs are never used for
  training. Harrison can revoke access for identifiable uploads or long inactivity. Unstated rate
  limits; bots and scripts banned. No user-visible study list or admin tooling is documented.
- **Clinical platform (HOP):** no browser upload at all. Studies come through one integration with
  the hospital PACS/RIS; the customer manages its own users via SSO; Harrison staff do
  configuration.

Takeaway: their demo controls are thin and live mostly in the terms. We already have stronger
building blocks (roles, suspension, quotas, audit logs), so we can do better with modest work.
Sources: chat.harrison.ai/terms, harrison.ai/harrison-rad-2-one-model-reporting, harrison.ai/hop,
support.harrison.ai/s/slas, harrison.ai/data-privacy-faqs.

## 4. Roles and permissions

Reuse the existing role model (OWNER, ADMIN, USER plus per-tenant custom roles, `module/access/domain/entity/Access.go`).
Permission names are permanent once added.

| New permission        | Meaning                                                              | Default holders        |
| --------------------- | -------------------------------------------------------------------- | ---------------------- |
| `uploads.create`      | May upload studies                                                   | USER, ADMIN, OWNER     |
| `uploads.view_own`    | Sees own uploads only                                                | USER                   |
| `uploads.view_all`    | Sees every upload in the workspace, with uploader identity           | ADMIN, OWNER           |
| `uploads.manage`      | Delete any upload, block or unblock a user's uploading, edit quotas  | ADMIN, OWNER           |

Proposed role names in the UI: **Demo user** (USER), **Demo admin** (ADMIN). Open question Q1: do we
also want a **Model developer** role (can upload and see their own model's results across users)?

## 5. User experience

### 5.1 Upload (demo user)

1. Upload button in the study list, enabled by `dicomUploadEnabled` in `pacs-ai/config/pacs-ai.js`
   (OHIF's `DicomUpload` component is already in the repo).
2. Before the first upload of each session, a checkbox the user must tick: "These files are
   de-identified and I have the right to share them." Store it with the upload record.
3. Accept DICOM only (files or a folder). Show progress per study, and a clear error for: over
   quota, blocked, non-DICOM, failed header check (5.4).
4. After upload the study appears in the user's list with an **"Expires in N days"** badge.

### 5.2 My uploads (demo user)

Table: study date, modality, description, size, uploaded at, expires at, model runs, status
(uploading, ready, processing, done, failed, deleted). Actions: open in viewer, run model, delete
now. Header shows "X.X GB of 5 GB used".

### 5.3 Admin dashboard (Demo admin)

New page `/admin/uploads`, next to the existing `/admin/members`.

- **Overview cards:** uploads today and last 30 days, total stored GB, active uploaders, studies
  flagged by the header check, studies expiring in the next 24 h.
- **Uploads table:** uploader (name, email), uploaded at, size, modality, StudyInstanceUID, flags,
  expires at, link to open in the viewer and to the Orthanc study. Filter by user, date, modality,
  flagged. This is the "see them in addition to Orthanc" view: it reads the upload registry (6.1)
  and links each row to Orthanc, so admins never have to search Orthanc by hand.
- **Actions per upload:** delete now (with reason: identifiable data, abuse, user request, other).
- **Actions per user:** block uploads, unblock uploads, set a custom quota, suspend account
  (existing), view all uploads of that user.
- Every action writes an audit event (6.5).

### 5.4 Automatic header check at upload (proposed, recommended)

Reuse `docs/public-demo/dicom-deidentification-policy.json` and the logic in
`scripts/validate_demo_dicom.py`: reject or flag a study whose identifying tags (Patient's Name,
Patient ID, Birth Date, Other Patient IDs, Accession Number, institution and physician names) are
present and non-empty. Modalities with a high risk of burned-in text (US, XA, SC) are flagged for
admin review rather than blocked. Open question Q2: block or only flag on header failure?

## 6. Backend requirements

### 6.1 Upload registry (new)

Postgres table `uploads`: id, tenant_id, user_id, study_instance_uid, orthanc_study_id, s3 prefix,
bytes, modality, uploaded_at, expires_at (uploaded_at + 30 days), deleted_at, deleted_by,
delete_reason, header_check_result, attestation_at. One row per study per uploader.

### 6.2 Isolation (blocker)

Today visibility is per workspace and every demo user is registered into the same tenant
(`Register.tsx:37-38`, `UserCommandService.go:531`). **As built, every demo user would see every
other user's uploads.** Required: on the demo tenant, a user without `uploads.view_all` sees only
the curated demo studies plus their own uploads. Enforce in `OrthancProxy` (QIDO, WADO and the
viewer metadata calls), not only in the UI.

### 6.3 Quota and blocking

- Count bytes per user from the registry; reject the STOW request with a clear error when the
  upload would exceed 5 GB **[decided]**. The existing 6 GB per-request limit stays as a backstop.
- Upload rate limit per user (proposal: 20 studies per hour). Q3.
- `upload_blocked` flag per user (Firestore user document plus the Redis check used for
  suspension, `IAMMiddleware.go:220`), checked on the upload route only, so a blocked user can
  still view and run models on the demo studies.

### 6.4 Storage and deletion

- Orthanc storage for the demo moves to **AWS S3 ca-central-1** **[decided]** via the Orthanc S3
  plugin (already used in the legacy `pacs-ai-mhi` stack with MinIO), SSE encryption, bucket
  versioning off or lifecycle rule that expires non-current versions within the 30 days.
- Replace, for uploads, the current 24 h cache job (`OrthancCommandService.go:43-91`) with a
  registry-driven job: delete from Orthanc, S3, ownership rows (`orthanc_tenant_studies`) and model
  results in cardio Postgres when `expires_at` passes, and on user or admin delete.
- The deletion must also reach Elasticsearch documents that contain outputs and nginx logs.
- Backups: cardio Postgres backups are kept 14 days, so a backup taken on day 29 would hold upload
  results until day 43 and break the 30-day promise. Either exclude upload-sourced rows from
  backups, or state backup retention in the Privacy Policy (Q4).
- Closing an account triggers deletion of all its uploads.
- Service email 3 days before expiry (proposal, Q5).

### 6.5 Audit (Elasticsearch, existing pipeline)

New events: upload.created, upload.rejected (reason), upload.deleted (by user, admin or expiry),
upload.flagged, user.upload_blocked, user.upload_unblocked, quota.changed. Never log pixel data or
identifying tags.

### 6.6 Never used for training [decided]

Mark predictions on uploaded studies with `source = user_upload`, and exclude that source from
anything wise-training reads (`docs/integrations/wise-training-access.md`). Add a test that fails if
an upload-sourced row is visible to the training reader.

### 6.7 Terms and email

- Set `POLICY_TERMS_VERSION=2026-11-27` and `POLICY_PRIVACY_VERSION=2026-11-27`; the existing
  `PolicyAcceptanceGuard` forces re-acceptance.
- Registration: optional, unticked mailing-list checkbox separate from Terms acceptance. Store a
  consent record (date, source, exact wording, version) in Firestore; only then call Mailchimp.
  The public `/lead/subscribe` endpoint must also store the same record.
- Privacy policy names the email providers: Mailgun (service emails) and Mailchimp (mailing list),
  with their storage location. Website follow-up.

## 7. Acceptance criteria (each becomes a test)

1. A demo user cannot list, open or fetch any study uploaded by another demo user (API test against
   QIDO, WADO and metadata routes).
2. An upload that would take a user over 5 GB is rejected; nothing is stored.
3. A user with uploads blocked gets a clear error on upload and can still view demo studies.
4. A study whose `expires_at` has passed is gone from Orthanc, S3, ownership rows, model results and
   the admin table within one job cycle.
5. Admin delete with reason removes the study everywhere and writes an audit event.
6. The training reader sees zero upload-sourced rows.
7. A user who has not accepted 2026-11-27 is blocked until they do.
8. Registration without ticking the mailing-list box creates no Mailchimp contact.

## 8. Rollout

1. Isolation (6.2) and registry (6.1) first; nothing else ships without them.
2. Quota, blocking, admin dashboard, audit.
3. S3 storage and the 30-day deletion job, then viewer upload button behind a feature flag on
   staging.
4. Privacy impact assessment (EFVP) update, then enable on demo.pacsai.co.

## 9. Gap between live Terms and today's system

The Terms went live on 2026-11-27 describing the feature as available. Until rollout step 4:

- There is no upload button, so no user can upload yet: low practical risk.
- Storage is a local Docker volume, not S3, and existing studies are deleted after 24 h, not 30 days.
- Mailing-list sign-ups through the website keep no consent record.

Recommendation: ship 6.7 (terms version, consent record) this week, since it affects users today.

## 10. Open questions

- Q1. Model developer role: yes or no?
- Q2. Header check failure: block the upload, or accept and flag for admin review?
- Q3. Upload rate limit per user (proposal 20 studies per hour).
- Q4. Backups: exclude uploads from the cardio Postgres backup, or keep backup retention short
  enough that the 30-day promise holds?
- Q5. Expiry warning email 3 days before deletion: yes or no?
- Q6. Who are the demo admins (names), and should admins get an email when a study is flagged?
- Q7. Allowed modalities at launch: all, or start with echo, angiography and CT?
