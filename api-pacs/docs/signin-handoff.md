# Sign-in handoff from CardioAgent (CA-268)

CardioAgent's "Open in PACS-AI" opens PACS-AI already signed in. The accounts
must be linked first; email alone never decides who gets signed in.

## Flow

1. **Mint (internal).** CardioAgent's backend calls
   `POST /v1/internal/auth/handoff` with `Authorization: Bearer $PACS_AI_HANDOFF_TOKEN`:

   ```json
   {"tenantId":"<workspace>","externalUid":"<firebase uid>","email":"<verified email>",
    "emailVerified":true,"returnPath":"/viewer?StudyInstanceUIDs=<uid>"}
   ```

   * `200 {data:{url, expiresIn:60}}`: the user is linked. Open `url`
     (`<PACS_AI_PUBLIC_URL>/auth/handoff#code=…`) right away.
   * `409 {data:{error:"link_required", url, expiresIn:300}}`: not linked yet.
     Open `url` (`…/auth/link#code=…`).
   * `403 not_allowed`: unverified email, or the PACS-AI user is suspended or
     disabled. `400 invalid_request`: bad fields or `returnPath`.
     `401`: wrong token. `503`: handoff not configured.

   The route answers only callers on the internal network: a request that
   came through nginx (`X-Real-IP`, `X-Forwarded-For`, `Forwarded`) gets 404.
   `returnPath` must be a relative path under `/`, `/viewer` or `/worklist`.

2. **Exchange (viewer, public).** `/auth/handoff` reads the code from the
   fragment, removes it from the address bar and calls
   `POST /v1/iam/handoff/exchange {code}`. api-pacs re-checks the link and the
   user, then creates a normal 15-minute session. The viewer stores it as a
   password login does and continues through policy acceptance and consent.
   At most 30 exchanges per IP per minute.

3. **Link (viewer, signed in).** `/auth/link` keeps the code in
   `sessionStorage`, sends a signed-out user to the login page and back, then
   asks the user to confirm. `POST /v1/iam/handoff/link {code}` links the
   CardioAgent user to the signed-in PACS-AI user if the CardioAgent verified
   email equals the PACS-AI account's email. That way, someone else's link
   code opened by a signed-in user cannot take over their account.

## Codes and links

* Codes are 32 random bytes (43 base64url characters), stored in Redis only as
  their SHA-256, single use (atomic get-and-delete). A handoff code is valid
  for 60 s and a link code for 5 min.
* Links are in `iam_account_links` (migration 000016), keyed on the CardioAgent
  user ID. There is one active link per CardioAgent user and per PACS-AI user,
  and relinking revokes the old one. A later email change on either side does
  not move a link.
* Every mint, exchange, link and rejection is logged as
  `[security] event=handoff_*` with tenant and user IDs and a hash of the
  CardioAgent ID, never the code.

## Enabling

1. Apply migration 000016.
2. Set `PACS_AI_HANDOFF_TOKEN` (a long random secret, also given to
   CardioAgent's backend) and `PACS_AI_PUBLIC_URL` in api-pacs's `.env`.
3. Deploy api-pacs and the viewer.

## CardioAgent on another host

When CardioAgent's backend runs on another server, it mints through the
public nginx at `https://<PACS-AI host>/api/v1/internal/auth/handoff`. Two
gates must both let that host in, plus the token:

* **nginx:** `NGINX_HANDOFF_ALLOWED_IP` (one IPv4 address or CIDR, in
  `nginx/.env`) is the only client allowed on that exact path. The default,
  `127.0.0.1`, keeps it closed.
* **api-pacs:** `PACS_AI_HANDOFF_ALLOWED_CIDRS` (comma-separated CIDRs). A
  proxied request is accepted only when its socket peer is a trusted proxy
  (`LOGIN_TRUSTED_PROXY_CIDRS`) and the client address nginx reports is in
  this list. Otherwise it gets 404, as before.

The other host must trust the PACS-AI server certificate's CA.
