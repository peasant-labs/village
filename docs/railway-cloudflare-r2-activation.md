# Set up Railway PostgreSQL and Cloudflare R2

## Scope

Use this procedure to set up encrypted transcript storage for Village.

Deploy Village and PostgreSQL in Railway. Store transcript objects in Cloudflare R2.

You do not need an AWS account. Village uses the S3-compatible API to connect to Cloudflare R2.

This procedure has separate checklists for Cloudflare, your workstation, and Railway.

Read [`transcript-storage-security.md`](transcript-storage-security.md) for the design.
Read [`transcript-encryption-cutover.md`](transcript-encryption-cutover.md) before the maintenance window.

## Execution order

1. Create the private deployment record.
2. Complete **2.1 Install tools and create the operator directory**.
3. Complete section 1, **Cloudflare checklist**.
4. Complete **2.2 Authenticate and link Railway**.
5. Complete **2.3 Create and verify the KEK**.
6. Complete section 3, **Railway setup before the merge**.
7. Complete section 4, **Pre-merge checklist**.
8. Stop before section 5 until the pull request has approval to merge.
9. Complete sections 5 through 7 during the maintenance window.

## Terms

The **old Railway environment** is the current production environment.

The **new Railway environment** is the replacement environment in this procedure.

**Production ingress** is user traffic through the production domains.

A **validation domain** is a temporary Railway domain for the validation operator.

The **old database credential** is the `DATABASE_URL` used by the old Railway backend service.

The **rollback boundary** is the time of the first successful encrypted transcript write.

An **object-cleanup event** has the name `transcript_blob_reconciliation_required`.
It means that encrypted bytes can remain after a cleanup attempt.

## Secret placement

| Secret | Create or retrieve it in | Use it in | Store it in | Remove or retain it |
|---|---|---|---|---|
| Transcript KEK | trusted workstation | new Railway backend `TRANSCRIPT_KEK_KEYRING` | separate KEK secret store | remove local file after readiness proof; retain recovery copy |
| R2 access keys | Cloudflare R2 dashboard | new Railway backend `S3_ACCESS_KEY` and `S3_SECRET_KEY` | credential secret store | retain until approved rotation or revocation |
| New PostgreSQL credential | Railway PostgreSQL service | new Railway backend `DATABASE_URL` reference | Railway and approved database recovery system | retain while the new stack serves |
| JWT signing secret | approved secret-generation procedure | new Railway backend `JWT_SECRET` | credential secret store | retain and rotate under the auth runbook |
| Identity-provider credentials | each identity-provider dashboard | new Railway backend provider variables | credential secret store | retain and rotate under the provider runbook |
| Old object-store credential | existing object-provider secret store | old stack and temporary denial probe only | existing credential secret store | revoke only after stabilization approval |
| Replacement old-database credential | approved old-database credential procedure | old backend during a pre-boundary abort only | credential secret store | retain or revoke under the abort record |
| External recovery credentials | approved database or object recovery system | only the named recovery runbook | recovery system's secret store | retain and rotate under that recovery runbook |

Never put these secrets in the frontend, migration service, Git, chat, logs, or deployment record.

## Credential lifecycle

| Credential | Provision | Validation or denial proof | Runtime owner | Rollback handling | Retirement or revocation |
|---|---|---|---|---|---|
| Transcript KEK | generate on the trusted workstation; copy to the separate recovery store | compare canonical 32-byte fingerprints, complete the local decrypt proof, then require backend readiness | new backend only | retain the recovery copy; never install it in an old revision | remove both workstation files after readiness; rotate only under the encryption operations runbook |
| New R2 token | create in Cloudflare for only the new bucket | require new-token write/read/delete and private empty-bucket checks | new backend only | retain for investigation while production ingress is closed | rotate or revoke only after stabilization approval |
| Old object-store credential | retrieve from its existing secret store for the denial probe | require list/write/read/delete authentication denials against the new bucket | old stack only | retain while a pre-boundary abort can restore the old stack | revoke after stabilization approval and record the credential ID |
| New PostgreSQL credential | create through the new Railway PostgreSQL service | require fresh-database inspection, migration proof, and product validation | migration service during migration; new backend during serving | retain with the new database; never expose it to an old revision after the boundary | rotate under the database recovery runbook |
| Old PostgreSQL credential or replacement | retain the existing credential; pre-authorize its replacement procedure | require an authenticated old-stack connection only when a pre-boundary abort needs it | old backend only | replace it before restarting the old backend when the abort procedure requires rotation | retire with the old environment after stabilization approval |
| JWT signing secret | retrieve or generate through the approved auth procedure | require authenticated sign-in through the validation and production domains | new backend only | keep the old value with the old environment for a pre-boundary abort | rotate under the authentication runbook |
| Identity-provider credentials | retrieve from each provider's approved secret store | require one successful flow for every enabled provider | new backend only | keep old provider configuration with the old environment for a pre-boundary abort | rotate or revoke under each provider runbook |

## Deployment record

Create one private deployment record. Include these values:

- Cloudflare account ID
- authorized Cloudflare operator, Super Administrator role, and verification evidence
- R2 bucket name, data location, endpoint, token ID, and token scope
- Railway project name and ID
- authorized Railway operator, workspace, and administrator role
- old and new Railway environment names
- trusted workstation Railway operator directory
- PostgreSQL service name and database name
- validation and production domains
- enabled identity providers
- database and object recovery owners
- approved database and object recovery runbook titles, revisions, and URLs
- recovery tools, authorized actors, sources, targets, commands, pass criteria, and evidence locations
- backup-copy identifiers and restore-test evidence
- credential secret store name
- KEK recovery store, procedure revision, operator, non-secret fingerprint, and recovery-test evidence
- durable backend log sink, retention period, and ingestion delay
- reconciliation settle window and log-sink verification evidence
- Cloudflare DNS CNAME and TXT names, old values, proxy status, and TTL values
- actual DNS-only production hostnames and Railway certificate-verification evidence
- old database and object-store identities
- old object-store credential ID and denial evidence against the new R2 bucket
- authorized GitHub merger and pull request URL
- Git revisions, Railway deployment IDs, immutable image digests, OCI revision labels, and validation results
- approval evidence and required-check results

Do not put a secret value in the deployment record.

## Shared recovery prerequisite

Apply this evidence template to both the R2 and PostgreSQL recovery tests:

1. Name the approved runbook title, revision, and URL.
2. Name the authorized operator and backup tool.
3. Name the production source and separate recovery target.
4. Record the exact backup and restore actions.
5. Create known disposable source data.
6. Create a recovery point or backup copy.
7. Restore to a separate test location.
8. Compare the restored data with the known source data.
9. Remove the disposable source and restored data.
10. Record the expected result, actual result, time, and private evidence location.

Stop when any field, comparison, cleanup result, or evidence is missing.
The storage-specific checklists below supply the commands and dashboard actions.
This repository does not select or create the external backup systems.

## Stop conditions

Stop if one of these conditions occurs:

- The old Railway environment can deploy `develop` automatically.
- The new PostgreSQL database contains a transcript row.
- The new R2 bucket contains an unexpected object before product validation.
- The new R2 bucket has public access.
- The R2 token scope includes more than the new bucket.
- A required backup has no successful restore test.
- The backend log sink cannot retain complete events for longer than the reconciliation settle window.
- A secret appears in Git, chat, logs, or the deployment record.
- A deployed revision does not match the approved Git revision.
- An authenticated product test fails.

Do not delete data to make a check pass.

After the rollback boundary, never connect the new database or bucket to an old Village revision.

## 1. Cloudflare checklist

### Cloudflare dashboard

#### Create the bucket

1. Open **R2 object storage**.
2. Select the Cloudflare account in the deployment record.
3. Select **Create bucket**.
4. Enter the bucket name from the deployment record.
5. Select the data location from the deployment record.
6. Use **Automatic** when you have no data residency requirement.
7. Create the bucket.
8. Open the bucket.
9. Open **Settings**.
10. Confirm that **Public Development URL** shows **Disabled**.
11. Confirm that **Custom Domains** is empty.
12. Confirm that the object list is empty.

Do not enable an `r2.dev` URL. Do not add a custom domain.

#### Create the runtime token

You need a Cloudflare Super Administrator for an account-owned token.

1. Open the Cloudflare account member settings.
2. Confirm that the signed-in operator is the authorized Cloudflare operator.
3. Confirm that the operator has the **Super Administrator** role.
4. Record the account, operator, role, and verification evidence.
5. Stop when the identity or role does not match the deployment record.
6. Return to the **R2 object storage** overview.
7. Find **API Tokens** in **Account Details**.
8. Select **Manage**.
9. Select **Create Account API token**.
10. Select **Object Read & Write**.
11. Select only the new Village bucket.
12. Create the token.
13. Record the token ID and token scope.
14. Store the **Access Key ID** in the credential secret store from the deployment record.
15. Store the **Secret Access Key** in the credential secret store from the deployment record.

Cloudflare shows the **Secret Access Key** one time. Do not put this key in the deployment record.

#### Record the endpoint

Use the endpoint that matches the bucket location:

| Bucket location | Endpoint |
|---|---|
| Automatic or location hint | `https://<ACCOUNT_ID>.r2.cloudflarestorage.com` |
| European Union jurisdiction | `https://<ACCOUNT_ID>.eu.r2.cloudflarestorage.com` |
| FedRAMP jurisdiction | `https://<ACCOUNT_ID>.fedramp.r2.cloudflarestorage.com` |

Record the endpoint in the deployment record. Do not record the access keys.

### Trusted workstation

#### Complete the object recovery prerequisite

Apply the **Shared recovery prerequisite** to R2 with these storage-specific steps.
Complete **2.1 Install tools and create the operator directory** before this checklist.
Use one continuous shell session for all command blocks in this checklist.
Keep the shell open until both local test files are removed.

1. Enter the name of the object recovery owner in the deployment record.
2. Enter the URL of the approved object recovery runbook.
3. On the trusted workstation, create a disposable test object:

```sh
set -e
recovery_test_file="$(mktemp)"
printf 'Village R2 recovery test %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$recovery_test_file"
recovery_test_hash="$(sha256sum "$recovery_test_file" | cut -d ' ' -f 1)"
printf 'Test file: %s\nSHA-256: %s\n' "$recovery_test_file" "$recovery_test_hash"
```

4. Open the new R2 bucket in the Cloudflare dashboard.
5. Upload the disposable file with the object key `recovery-test/source.txt`.
6. Use the approved recovery runbook to copy the object to a separate backup location.
7. Record the backup-copy identifier and creation time.
8. Delete `recovery-test/source.txt` from the new R2 bucket.
9. Use the approved recovery runbook to restore the backup to a separate test location.
10. Download the restored object to the trusted workstation.
11. Set `restored_test_file` to the downloaded local file path.
12. Compare the restored bytes with the source bytes:

```sh
set -e
restored_test_file='<RESTORED_TEST_FILE>'
restored_test_hash="$(sha256sum "$restored_test_file" | cut -d ' ' -f 1)"
test "$restored_test_hash" = "$recovery_test_hash"
printf '%s\n' 'PASS: the restored R2 test object matches the source object.'
```

13. Remove the restored object from the separate test location.
14. Remove both local test files:

```sh
rm -f "$recovery_test_file" "$restored_test_file"
unset recovery_test_file recovery_test_hash restored_test_file restored_test_hash
```

15. Confirm that the new R2 bucket is empty.
16. Record the restore-test time and evidence URL.
17. Stop when the owner, runbook URL, backup-copy evidence, or restore-test evidence is missing.

Do not use the production R2 bucket as its own backup.

#### Test the new token and deny the old credential

Complete **2.1 Install tools and create the operator directory** before this checklist.
Use one continuous shell session.

1. Create a protected temporary rclone configuration:

```sh
set -e
set +x
umask 077
rclone_config="$HOME/.local/state/village-railway-activation/rclone-r2-probe.conf"
test ! -e "$rclone_config"
: >"$rclone_config"
chmod 600 "$rclone_config"
printf 'Temporary rclone configuration: %s\n' "$rclone_config"
```

2. Open the file in a trusted local editor.
3. Add these two remotes.
4. Retrieve each credential only through its approved secret-store procedure.
5. Do not put a credential in shell history or the deployment record.

```toml
[village-new-r2]
type = s3
provider = Cloudflare
access_key_id = <NEW_R2_ACCESS_KEY_ID>
secret_access_key = <NEW_R2_SECRET_ACCESS_KEY>
endpoint = <NEW_R2_ENDPOINT>
acl = private
no_check_bucket = true

[village-old-credential]
type = s3
provider = Cloudflare
access_key_id = <OLD_OBJECT_STORE_ACCESS_KEY_ID>
secret_access_key = <OLD_OBJECT_STORE_SECRET_ACCESS_KEY>
endpoint = <NEW_R2_ENDPOINT>
acl = private
no_check_bucket = true
```

6. Save and close the editor.
7. Run the authenticated capability and denial proof:

```sh
set -e
set +x
umask 077

rclone_config="$HOME/.local/state/village-railway-activation/rclone-r2-probe.conf"
probe_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"
probe_key="credential-probe/$probe_id.bin"
old_write_probe_key="credential-probe/$probe_id-old-write.bin"
delete_probe_key="credential-probe/$probe_id-old-delete.bin"
probe_source="$HOME/.local/state/village-railway-activation/r2-probe-source.bin"
probe_returned="$HOME/.local/state/village-railway-activation/r2-probe-returned.bin"
old_read_probe="$HOME/.local/state/village-railway-activation/r2-old-read.bin"
old_list_denial="$HOME/.local/state/village-railway-activation/r2-old-list-denial.log"
old_write_denial="$HOME/.local/state/village-railway-activation/r2-old-write-denial.log"
old_read_denial="$HOME/.local/state/village-railway-activation/r2-old-read-denial.log"
old_delete_denial="$HOME/.local/state/village-railway-activation/r2-old-delete-denial.log"
probe_listing="$HOME/.local/state/village-railway-activation/r2-probe-listing.txt"

cleanup_r2_probe() {
  rclone --config "$rclone_config" deletefile \
    "village-new-r2:<R2_BUCKET>/$probe_key" > /dev/null 2>&1 || true
  rclone --config "$rclone_config" deletefile \
    "village-new-r2:<R2_BUCKET>/$old_write_probe_key" > /dev/null 2>&1 || true
  rclone --config "$rclone_config" deletefile \
    "village-new-r2:<R2_BUCKET>/$delete_probe_key" > /dev/null 2>&1 || true
  rm -f "$rclone_config" "$probe_source" "$probe_returned" "$old_read_probe"
  rm -f "$old_list_denial" "$old_write_denial" "$old_read_denial" "$old_delete_denial" "$probe_listing"
}
trap cleanup_r2_probe EXIT HUP INT TERM

record_denial() {
  operation="$1"
  log_file="$2"
  if ! denial_class="$(grep -Eom1 'AccessDenied|InvalidAccessKeyId|SignatureDoesNotMatch|Unauthorized|StatusCode: (401|403)' "$log_file")"; then
    printf 'STOP: old credential %s failed without a recognized authentication denial.\n' "$operation" >&2
    exit 1
  fi
  printf 'Old credential %s denial: %s\n' "$operation" "$denial_class"
}

openssl rand 64 >"$probe_source"

if rclone --config "$rclone_config" lsf \
  "village-old-credential:<R2_BUCKET>" \
  > /dev/null 2>"$old_list_denial"; then
  printf '%s\n' 'STOP: the old object-store credential can list the new R2 bucket.' >&2
  exit 1
fi
record_denial list "$old_list_denial"

if rclone --config "$rclone_config" copyto \
  "$probe_source" \
  "village-old-credential:<R2_BUCKET>/$old_write_probe_key" \
  > /dev/null 2>"$old_write_denial"; then
  rclone --config "$rclone_config" deletefile \
    "village-new-r2:<R2_BUCKET>/$old_write_probe_key" > /dev/null 2>&1 || true
  printf '%s\n' 'STOP: the old object-store credential can write to the new R2 bucket.' >&2
  exit 1
fi
record_denial write "$old_write_denial"

rclone --config "$rclone_config" copyto \
  "$probe_source" \
  "village-new-r2:<R2_BUCKET>/$probe_key"

if rclone --config "$rclone_config" copyto \
  "village-old-credential:<R2_BUCKET>/$probe_key" \
  "$old_read_probe" \
  > /dev/null 2>"$old_read_denial"; then
  printf '%s\n' 'STOP: the old object-store credential can read from the new R2 bucket.' >&2
  exit 1
fi
record_denial read "$old_read_denial"

rclone --config "$rclone_config" copyto \
  "village-new-r2:<R2_BUCKET>/$probe_key" \
  "$probe_returned"
cmp -s "$probe_source" "$probe_returned"

rclone --config "$rclone_config" copyto \
  "$probe_source" \
  "village-new-r2:<R2_BUCKET>/$delete_probe_key"
if rclone --config "$rclone_config" deletefile \
  "village-old-credential:<R2_BUCKET>/$delete_probe_key" \
  > /dev/null 2>"$old_delete_denial"; then
  printf '%s\n' 'STOP: the old object-store credential can delete from the new R2 bucket.' >&2
  exit 1
fi
record_denial delete "$old_delete_denial"

rclone --config "$rclone_config" deletefile \
  "village-new-r2:<R2_BUCKET>/$probe_key"
rclone --config "$rclone_config" deletefile \
  "village-new-r2:<R2_BUCKET>/$delete_probe_key"

rclone --config "$rclone_config" lsf \
  "village-new-r2:<R2_BUCKET>" \
  --files-only --recursive >"$probe_listing"
if grep -Fxq -- "$probe_key" "$probe_listing"; then
  printf '%s\n' 'STOP: the R2 credential probe object still exists.' >&2
  exit 1
fi

printf 'Probe object: %s\n' "$probe_key"
printf '%s\n' 'PASS: the new R2 token completed write, read, and delete.'
printf '%s\n' 'PASS: old-credential list, write, read, and delete all received authentication denials.'

cleanup_r2_probe
trap - EXIT HUP INT TERM
unset rclone_config probe_id probe_key old_write_probe_key delete_probe_key
unset probe_source probe_returned old_read_probe probe_listing
unset old_list_denial old_write_denial old_read_denial old_delete_denial
unset operation log_file denial_class
```

8. Record the probe object keys, successful new-token operations, denial class for each old-credential operation, operator, and time.
9. Do not record credential values or unreviewed error output.
10. Confirm that `rclone-r2-probe.conf` and all local probe files are absent.
11. Confirm in Cloudflare that the new R2 bucket is empty.

## 2. Workstation checklist

### Trusted workstation

#### 2.1 Install tools and create the operator directory

Use vendor-supported tool releases that receive security updates.
The capability check below is the compatibility gate for this procedure.
Install the non-Railway tools with the workstation operating system's supported package manager.

1. Install the Railway CLI with the
   [official Railway installer](https://docs.railway.com/guides/cli#installing-the-cli).
2. Install a current `psql` client. It must have the same or a newer major version than the new PostgreSQL server.
3. Install OpenSSL 3 or later.
4. Install Docker Engine 24 or later.
5. Install `jq` 1.6 or later.
6. Install GNU Coreutils 9 or later for `sha256sum`.
7. Install `curl` 8 or later.
8. Install `rclone` 1.59 or later.
9. Run the capability check:

```sh
set -e

for tool in railway psql openssl docker jq sha256sum curl rclone; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    printf 'STOP: required command is missing: %s\n' "$tool" >&2
    exit 1
  fi
done

railway --version
railway ssh --help >/dev/null
connect_help="$(railway connect --help)"
case "$connect_help" in
  *--ssh*) ;;
  *) printf '%s\n' 'STOP: Railway connect does not support --ssh.' >&2; exit 1 ;;
esac
psql --version
openssl version
docker --version
docker info >/dev/null
jq --version
sha256sum --version
curl --version
rclone version

printf '%s\n' 'PASS: all required command capabilities are available.'
```

10. Compare the reported versions with the requirements above.
11. Stop when a version requirement or capability check fails.
12. Create a private Railway operator directory outside the Git checkout:

```sh
set -e
railway_operator_dir="$HOME/.local/state/village-railway-activation"
mkdir -p "$railway_operator_dir"
chmod 700 "$railway_operator_dir"
cd "$railway_operator_dir"
printf 'Railway operator directory: %s\n' "$railway_operator_dir"
```

13. Record the operator directory in the deployment record.

#### 2.2 Authenticate and link Railway

1. Change to the Railway operator directory.
2. Run `railway login`.
3. Run `railway whoami`.
4. Confirm that the account belongs to the authorized Railway operator.
5. Open the Railway workspace member settings.
6. Confirm that the operator has the administrator role for the target project.
7. Record the account, workspace, role, and verification evidence.
8. Link the operator directory to the new Railway environment:

```sh
railway link --project '<RAILWAY_PROJECT>' --environment '<NEW_ENVIRONMENT>'
```

9. Run `railway status`.
10. Confirm that the project and environment match the deployment record.
11. Run `railway ssh keys`.
12. Register a trusted workstation SSH key when Railway lists no usable key.

#### 2.3 Create and verify the KEK

The key-encryption key is the KEK. Village uses the KEK to encrypt each transcript data key.

1. Open a shell on the trusted workstation.
2. Turn off shell tracing.
3. Generate the KEK in the private Railway operator directory:

```sh
set -e
set +x
umask 077
kek_file="$HOME/.local/state/village-railway-activation/transcript-kek-v1.base64"
test ! -e "$kek_file"
openssl rand 32 | openssl base64 -A >"$kek_file"
printf '\n' >>"$kek_file"
chmod 600 "$kek_file"
printf 'Temporary KEK file: %s\n' "$kek_file"
```

4. Open the temporary file in a trusted local editor.
5. Store one KEK recovery copy in the secret store identified in the deployment record.
6. Create an empty recovery-test file:

```sh
set -e
set +x
umask 077
recovered_kek_file="$HOME/.local/state/village-railway-activation/transcript-kek-v1.recovered.base64"
test ! -e "$recovered_kek_file"
: >"$recovered_kek_file"
chmod 600 "$recovered_kek_file"
```

7. Use the approved KEK recovery procedure to retrieve the recovery copy.
8. Put only the one-line base64 value in `transcript-kek-v1.recovered.base64`.
9. Do not put the value in a command argument, shell history, log, or deployment record.
10. Validate the recovery copy and run a disposable local decrypt test:

```sh
set -e
set +x
umask 077

kek_file="$HOME/.local/state/village-railway-activation/transcript-kek-v1.base64"
recovered_kek_file="$HOME/.local/state/village-railway-activation/transcript-kek-v1.recovered.base64"

generated_length="$(openssl base64 -d -A <"$kek_file" | wc -c)"
recovered_length="$(openssl base64 -d -A <"$recovered_kek_file" | wc -c)"
test "$generated_length" = '32'
test "$recovered_length" = '32'

generated_text="$(tr -d '\n' <"$kek_file")"
recovered_text="$(tr -d '\n' <"$recovered_kek_file")"
generated_canonical="$(openssl base64 -d -A <"$kek_file" | openssl base64 -A)"
recovered_canonical="$(openssl base64 -d -A <"$recovered_kek_file" | openssl base64 -A)"
test "$generated_text" = "$generated_canonical"
test "$recovered_text" = "$recovered_canonical"

generated_fingerprint="$(openssl base64 -d -A <"$kek_file" | sha256sum | cut -d ' ' -f 1)"
recovered_fingerprint="$(openssl base64 -d -A <"$recovered_kek_file" | sha256sum | cut -d ' ' -f 1)"
test "$generated_fingerprint" = "$recovered_fingerprint"

probe_plaintext="$(mktemp)"
probe_ciphertext="$(mktemp)"
probe_recovered="$(mktemp)"
chmod 600 "$probe_plaintext" "$probe_ciphertext" "$probe_recovered"
openssl rand 64 >"$probe_plaintext"
openssl enc -aes-256-cbc -pbkdf2 -salt \
  -pass "file:$kek_file" \
  -in "$probe_plaintext" \
  -out "$probe_ciphertext"
openssl enc -d -aes-256-cbc -pbkdf2 \
  -pass "file:$recovered_kek_file" \
  -in "$probe_ciphertext" \
  -out "$probe_recovered"
cmp -s "$probe_plaintext" "$probe_recovered"

printf 'KEK fingerprint: %s\n' "$generated_fingerprint"
printf '%s\n' 'PASS: the recovered KEK is canonical 32-byte material and completed the local decrypt test.'

rm -f "$probe_plaintext" "$probe_ciphertext" "$probe_recovered" "$recovered_kek_file"
unset recovered_kek_file generated_length recovered_length
unset generated_text recovered_text generated_canonical recovered_canonical
unset generated_fingerprint recovered_fingerprint
unset probe_plaintext probe_ciphertext probe_recovered
```

The local decrypt test does not use Village's transcript format.
It proves that the retrieved recovery copy is usable and equals the generated 32-byte KEK.
The backend startup evidence later proves that Village accepts the same KEK.

11. Record the non-secret fingerprint, recovery procedure revision, operator, and test evidence.
12. Confirm that `transcript-kek-v1.recovered.base64` is absent.
13. Keep the generated temporary file until the new Railway backend proves that it loaded the KEK.

Do not print the KEK. Do not put the KEK in shell history.

Do not store the KEK with the database password or the R2 token.

## 3. Railway setup before the merge

You need administrator access to the Railway project.

### Railway dashboard

#### Stop automatic deployment in the old environment

1. Open the old Railway backend service.
2. Open the source deployment settings.
3. Disable the GitHub deployment trigger for `develop`.
4. Disconnect the GitHub source when Railway has no disable control.
5. Open the old Railway frontend service.
6. Open the source deployment settings.
7. Disable the GitHub deployment trigger for `develop`.
8. Disconnect the GitHub source when Railway has no disable control.
9. Confirm that a push to `develop` cannot deploy either service.
10. Keep the old services running.

Do not merge the encryption change until this checklist is complete.

#### Create the new environment and service definitions

1. Open the Railway environment menu.
2. Select **New Environment**.
3. Select **Empty Environment**.
4. Create a persistent environment.
5. Do not create a pull-request environment.
6. Open the new environment.
7. Select **Sync**.
8. Select the old Railway environment as the source.
9. Import the backend and frontend service definitions.
10. Review the staged changes.
11. Do not deploy the staged services.
12. Remove each inherited database, object, and KEK variable.
13. Keep both GitHub sources disconnected.
14. Confirm that the new backend service exists.
15. Confirm that the new frontend service exists.
16. Confirm that both services use the production Dockerfiles.
17. Do not attach the production domains.

The backend must use `backend/Dockerfile`. The frontend must use `frontend/Dockerfile`.

#### Create PostgreSQL

1. Select **Create** on the project canvas.
2. Select **Database**.
3. Select **PostgreSQL**.
4. Create PostgreSQL in the new environment.
5. Wait for PostgreSQL to become healthy.
6. Record the PostgreSQL service name.
7. Record the PostgreSQL database name.

#### Confirm that PostgreSQL is fresh

1. Change to the Railway operator directory on the trusted workstation.
2. Run this command:

```sh
railway connect '<POSTGRES_SERVICE>' --environment '<NEW_ENVIRONMENT>' --ssh
```

3. Run these commands in `psql`:

```sql
\conninfo
SHOW server_version;
SELECT current_database(), current_user, inet_server_addr(), inet_server_port();
SELECT to_regclass('public.transcripts') AS transcripts_table;
```

4. Compare `SHOW server_version` with the recorded `psql --version` output.
5. Stop when the `psql` major version is older than the server major version.
6. Confirm that the database name matches the deployment record.
7. Continue when `transcripts_table` is null.
8. Run this query when `transcripts_table` is not null:

```sql
SELECT count(*) AS transcript_rows FROM public.transcripts;
```

9. Continue only when `transcript_rows` is zero.
10. Stop when `transcript_rows` is not zero.

Village must use Railway's private `DATABASE_URL` connection at runtime.

#### Complete the database recovery prerequisite

Apply the **Shared recovery prerequisite** to PostgreSQL with these storage-specific steps.

1. Enter the name of the database recovery owner in the deployment record.
2. Enter the URL of the approved database recovery runbook.
3. Use Railway Backups when your plan supplies the required controls.
4. Use the approved external procedure when Railway does not supply the required controls.
5. From the trusted workstation, connect to the new PostgreSQL service through Railway.
6. Create a disposable recovery marker in `psql`:

```sql
CREATE SCHEMA recovery_test;
CREATE TABLE recovery_test.marker (
    value TEXT PRIMARY KEY
);
INSERT INTO recovery_test.marker (value)
VALUES ('village-postgresql-recovery-test');
```

7. Create one recovery point with the approved recovery procedure.
8. Record the recovery-point identifier and creation time.
9. Restore that recovery point to a separate test database.
10. Connect to the separate test database.
11. Run this query:

```sql
SELECT value
FROM recovery_test.marker;
```

12. Require one row with the value `village-postgresql-recovery-test`.
13. Drop the disposable schema from the production and restored test databases:

```sql
DROP SCHEMA recovery_test CASCADE;
```

14. Confirm that `to_regnamespace('recovery_test')` is null in both databases.
15. Record the restore-test time and evidence URL.
16. Stop when the owner, runbook URL, recovery-point evidence, or restore-test evidence is missing.

#### Create validation domains

1. Open the new Railway backend service.
2. Open **Settings**.
3. Open **Networking**.
4. Select **Generate Domain**.
5. Record the backend validation origin.
6. Open the new Railway frontend service.
7. Open **Settings**.
8. Open **Networking**.
9. Select **Generate Domain**.
10. Record the frontend validation origin.

The validation domains are internet addresses. Village authentication protects the validation paths.

Do not share the validation domains with general users. Do not attach the production domains yet.

## 4. Pre-merge checklist

Complete this checklist before you merge:

1. Confirm that the old environment cannot deploy `develop` automatically.
2. Confirm that both new Railway services exist.
3. Confirm that both new GitHub sources are disconnected.
4. Confirm that the new PostgreSQL database is fresh.
5. Confirm that the new R2 bucket is private and empty.
6. Confirm that the R2 token scope lists only the new bucket.
7. Confirm that the KEK has a separate recovery copy.
8. Confirm that each required restore test passed.
9. Confirm that the deployment record names the backend log sink and reconciliation settle window.
10. Confirm that the new services have no production domains.
11. Confirm that all required repository build and test checks passed.
12. Confirm that the deployment record contains no secret values.

Stop when any numbered check fails. Record the failed check and its result.

## 5. Maintenance and deployment after the merge

### Verify the production revision

1. Sign in to GitHub as the authorized merger from the deployment record.
2. Open the approved pull request URL from the deployment record.
3. Confirm that the pull request is open and targets `develop`.
4. Confirm that its head commit is the approved pre-merge Git revision.
5. Confirm that every required reviewer has approved the pull request.
6. Confirm that every branch-protection-required check has passed.
7. Confirm that GitHub permits **Create a merge commit**.
8. Record the actor, approval state, required-check results, and head commit.
9. Stop when one of the approval checks fails.
10. Merge the pull request with **Create a merge commit**.
11. Record the full merge commit SHA.
12. Open the `develop` branch in GitHub.
13. Confirm that the branch head is the full merge commit SHA.
14. Stop other merges to `develop` until production ingress is open.
15. Update a clean local `develop` worktree.
16. Change to the Village repository root.
17. Set the validation frontend API URL:

```sh
export NEXT_PUBLIC_API_URL='https://<BACKEND_VALIDATION_DOMAIN>/api/v1'
```

18. Run the production artifact check:

```sh
scripts/verify-production-artifacts.sh '<FULL_MERGE_COMMIT_SHA>'
```

19. Stop when the artifact check fails.

### Stop all old writers

Do these steps in the old Railway environment.

1. Open the old backend service.
2. Open **Settings**.
3. Open **Networking**.
4. Record each production domain mapping.
5. Remove each production domain.
6. Repeat steps 1 through 5 for the old frontend service.
7. Confirm that a new request through a production domain cannot reach the old environment.
8. Inventory every old backend, worker, queue consumer, scheduled job, and maintenance service.
9. Record the inventory in the deployment record.
10. Drain all old requests and jobs.
11. Stop every old backend process.
12. Stop every old worker and queue consumer.
13. Disable every old scheduled job and maintenance service.
14. Verify in the Railway control plane that every inventoried writer is stopped.
15. Confirm that no old database session or transaction can write.
16. Disable each old database credential used by an inventoried writer.
17. Confirm that a new connection with each old credential fails.

Do not add the R2 token or KEK to Railway before this checklist is complete.

### Run migration 031 with database authority only

Do these steps in the new Railway environment.

1. Duplicate the new Railway backend service definition.
2. Name the temporary service `migration-031`.
3. Remove public networking from `migration-031`.
4. Remove all runtime variables from `migration-031`.
5. Add only this runtime variable:

```text
DATABASE_URL=${{<POSTGRES_SERVICE>.DATABASE_URL}}
```

6. Set the start command to `/server -migrate-only`.
7. Connect the `migration-031` GitHub source to `develop`.
8. Do not create a custom `VCS_REF` variable; Railway supplies `RAILWAY_GIT_COMMIT_SHA` to GitHub-triggered builds.
9. Open the `migration-031` service.
10. Open the Railway command palette with `CMD + K` or `Ctrl + K` when no deployment starts.
11. Select **Deploy Latest Commit** when no deployment starts.
12. Open the new deployment details.
13. Compare the source commit with the full merge commit SHA.
14. Cancel or stop the deployment immediately when the values differ.
15. Stop the procedure when the values differ.
16. Require the build log to print the same full merge commit SHA as `Building Village revision`.
17. Require a successful process exit.
18. Stop when the migration fails.

From the Railway operator directory on the trusted workstation, connect to the new PostgreSQL service through Railway.
Run these queries in `psql`:

```sql
SELECT version, applied_at
FROM public.schema_migrations
WHERE version = 31;

SELECT tgname
FROM pg_trigger
WHERE tgname = 'trg_transcript_writer_version';
```

19. Require one migration 031 record.
20. Require one `trg_transcript_writer_version` trigger.
21. Stop the procedure when either result is absent.
22. Delete the temporary `migration-031` service only after both results are present.

Do not put JWT, R2, OAuth, or KEK variables in `migration-031`.

### Configure the new backend service

**Location:** Railway dashboard.

Add these runtime variables to the new Railway backend service:

```text
DATABASE_URL=${{<POSTGRES_SERVICE>.DATABASE_URL}}
S3_ENDPOINT=<Cloudflare R2 endpoint>
S3_BUCKET=<R2 bucket name>
S3_ACCESS_KEY=<R2 Access Key ID>
S3_SECRET_KEY=<R2 Secret Access Key>
S3_USE_PATH_STYLE=true
TRANSCRIPT_KEK_ACTIVE_VERSION=1
TRANSCRIPT_KEK_KEYRING={"1":"<base64 KEK>"}
JWT_SECRET=<strong JWT secret>
FRONTEND_URL=https://<FRONTEND_VALIDATION_DOMAIN>
BASE_URL=https://<BACKEND_VALIDATION_DOMAIN>
```

1. Open the new Railway environment in the Railway dashboard.
2. Open the new backend service.
3. Open **Variables**.
4. Open `$HOME/.local/state/village-railway-activation/transcript-kek-v1.base64`
   in a trusted local editor.
5. Copy only the one-line base64 value with a secure clipboard timeout.
6. Set `TRANSCRIPT_KEK_ACTIVE_VERSION` to `1`.
7. Set `TRANSCRIPT_KEK_KEYRING` to `{"1":"<PASTE_BASE64_VALUE_HERE>"}`.
8. Do not use a CLI argument or shell command to set the KEK.
9. Clear the clipboard after Railway stores the value.
10. Add the remaining runtime variables from the block above.
11. Add credentials for each identity provider in the deployment record.
12. Register each validation callback URL with its identity provider.
13. Seal each secret when Railway supplies that control.
14. Confirm that the frontend has none of the `DATABASE_URL`, `S3_*`, or `TRANSCRIPT_KEK_*` variables.
15. Confirm that the old environment has none of the new `DATABASE_URL`, `S3_*`, or `TRANSCRIPT_KEK_*` variables.
16. Clear the backend start command so Railway uses Docker's `CMD ["/server"]`.
17. Remove any backend pre-deploy command.
18. Remove any custom `VCS_REF` variable.
19. Confirm that the GitHub source will supply `RAILWAY_GIT_COMMIT_SHA` when deployment starts.

Do not add `S3_REGION`. Cloudflare R2 accepts Village's `us-east-1` setting as `auto`.

### Configure the new frontend service

**Location:** Railway dashboard.

1. Remove any custom `VCS_REF` variable.
2. Confirm that the GitHub source will supply `RAILWAY_GIT_COMMIT_SHA` when deployment starts.
3. Add `NEXT_PUBLIC_API_URL` as a Railway build variable.
4. Set `NEXT_PUBLIC_API_URL` to `https://<BACKEND_VALIDATION_DOMAIN>/api/v1`.
5. Confirm that the frontend uses `frontend/Dockerfile`.
6. Clear the frontend start command so Railway uses Docker's `CMD ["node","frontend/server.js"]`.

Read [`../frontend/README.md`](../frontend/README.md) for all frontend settings.

### Shared deploy and immutable-revision proof

Use this checklist every time this procedure deploys or replaces a serving service.
Set `SERVICE`, `ROLE`, `NEW_ENVIRONMENT`, and `EXPECTED_REVISION` before you start.
Use the Railway dashboard for deployment metadata and the trusted workstation for the runtime command.

1. Confirm that `develop` points to `EXPECTED_REVISION`.
2. Open `SERVICE` in the new Railway environment.
3. Open the Railway command palette with `CMD + K` or `Ctrl + K` when no deployment starts.
4. Select **Deploy Latest Commit** when no deployment starts.
5. Open the new deployment details.
6. Require the source commit to equal `EXPECTED_REVISION`.
7. Cancel or stop the deployment immediately when the source differs.
8. Require a successful deployment.
9. Open the deployment artifact metadata.
10. Record the deployment ID and immutable image digest.
11. Require `org.opencontainers.image.revision` to equal `EXPECTED_REVISION`.
12. Stop when Railway cannot provide the digest or revision label.
13. From the Railway operator directory, read the live runtime revision:

```sh
set -e
SERVICE='<SERVICE>'
ROLE='<backend-or-frontend>'
NEW_ENVIRONMENT='<NEW_ENVIRONMENT>'
EXPECTED_REVISION='<FULL_MERGE_COMMIT_SHA>'
: "${SERVICE:?}" "${ROLE:?}" "${NEW_ENVIRONMENT:?}" "${EXPECTED_REVISION:?}"
runtime_revision="$(railway ssh --service "$SERVICE" --environment "$NEW_ENVIRONMENT" -- printenv VILLAGE_BUILD_REVISION)"
test "$runtime_revision" = "$EXPECTED_REVISION"
printf 'Runtime revision: %s\n' "$runtime_revision"
```

14. Stop when the live runtime revision differs.
15. For the backend role, find exactly one `transcript_encryption_authority_ready` event in the new deployment logs.
16. For the backend role, require `stage=pre_listener`, `active_key_version=1`, and `revision=EXPECTED_REVISION`.
17. For the backend role, require the event to belong to the recorded deployment digest.
18. Stop when required backend readiness evidence is absent, duplicated, or incomplete.
19. Record the service, role, source revision, deployment ID, digest, label, runtime revision, and readiness evidence.
20. Mark the recorded digest as the approved artifact for this deployment.

### Deploy the new serving stack

1. Connect the new Railway backend source to `develop`.
2. Connect the new Railway frontend source to `develop`.
3. Apply **Shared deploy and immutable-revision proof** to the backend service with the full merge commit SHA.
4. Apply **Shared deploy and immutable-revision proof** to the frontend service with the full merge commit SHA.
5. Confirm that no process in the old Railway environment uses the new database or bucket.
6. Stop when the backend logs contain `transcript KEK configuration failed` or `authority loading failed`.
7. From the trusted workstation, request the backend health endpoint:

```sh
set -e
health_status="$(curl -sS -o /dev/null -w '%{http_code}' 'https://<BACKEND_VALIDATION_DOMAIN>/health')"
test "$health_status" = '200'
printf '%s\n' 'PASS: the backend health endpoint returned HTTP 200.'
```

8. Stop when the health command fails.
9. Record the health evidence.
10. Remove the temporary KEK file from the trusted workstation:

```sh
set -e
kek_file="$HOME/.local/state/village-railway-activation/transcript-kek-v1.base64"
rm -f "$kek_file"
test ! -e "$kek_file"
unset kek_file
printf '%s\n' 'PASS: the temporary KEK file is absent.'
```

The new backend runs the migration check again. The check does not change migration 031.

### Verify durable backend log retention

Complete this checklist before the first transcript write.

1. Read
   [`transcript-encryption-operations.md`](transcript-encryption-operations.md#required-log-retention).
2. Record each retention bound in the deployment record.
3. Calculate and record the reconciliation settle window.
4. Record the Railway plan's log-retention period.
5. Use Railway Log Explorer only when its retention period is longer than the settle window.
6. Configure an approved external log-forwarding procedure when Railway retention is not long enough.
7. Record the selected sink, retention period, and expected ingestion delay.
8. Tell the monitoring owner that you will emit one synthetic reconciliation event.
9. Change to the Railway operator directory on the trusted workstation.
10. Emit the event from the new backend container:

```sh
set -e
log_probe_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"
probe_transcript_id='00000000-0000-4000-8000-000000000000'
probe_object_key="reconciliation-log-probe/$log_probe_id.bin"

railway ssh \
  --service '<BACKEND_SERVICE>' \
  --environment '<NEW_ENVIRONMENT>' \
  -- /bin/sh -c \
  "printf '%s\\n' 'level=ERROR msg=transcript_blob_reconciliation_required probe=true operation=log_probe transcript_id=$probe_transcript_id object_key=$probe_object_key completion=probe meaning=synthetic-durable-log-probe remediation=archive-probe-without-storage-action' > /proc/1/fd/1"

printf 'Log probe ID: %s\nObject key: %s\n' "$log_probe_id" "$probe_object_key"
```

11. Search the selected sink for the exact `probe_object_key` value.
12. Require one `transcript_blob_reconciliation_required` event.
13. Require `probe=true`.
14. Require `operation`, `transcript_id`, `object_key`, `completion`, `meaning`, and `remediation`.
15. Require the event timestamp and backend service identity.
16. Apply **Shared deploy and immutable-revision proof** to replace the backend with the full merge commit SHA.
17. Search the selected sink for the exact `probe_object_key` value again.
18. Require the event to remain available after the deployment replacement.
19. Record the log-sink verification evidence.
20. Mark the event as an intentional probe in the monitoring system.
21. Stop when the event is missing, incomplete, or retained for less than the settle window.

The sink must retain the complete `transcript_blob_reconciliation_required` event.
It must retain `operation`, `transcript_id`, `object_key`, `completion`, `meaning`, and `remediation`.

This synthetic event tests only the sink and its retention.
The repository gates `TestBlobCleanupBoundaryEmitsSecretSafeReconciliationEvidence` and
`TestMountedBlobCleanupFailuresEmitReconciliationEvidence` verify the production cleanup boundary's
event fields, exact completion value, mounted call paths, and secret exclusions.
When validation produces a real non-probe event, require all fields above before reconciliation.
Stop when a real application event is incomplete.

## 6. Validation checklist

Use a disposable operator account. Use a unique text marker in each disposable transcript.
Use the
[`conservative reconciliation procedure`](transcript-encryption-operations.md#conservative-reconciliation)
for each durable object-cleanup event.

The private transcript test covers pull, republish, and conditional-read storage behavior.
All visibility tiers use the same encrypted object store.
The public and shared tests repeat publish, ciphertext inspection, and authenticated read to verify their access paths.
The cleanup checklist deletes the transcripts from all three visibility tiers.

### Test encryption through the product

Before publishing each visibility tier, prepare one disposable transcript source file.
Put its unique text marker in the body.
Record the source file path, SHA-256 hash, and byte size:

```sh
set -e
source_file='<DISPOSABLE_TRANSCRIPT_SOURCE_FILE>'
test_marker='<UNIQUE_TEST_TEXT>'
grep -Fq -- "$test_marker" "$source_file"
source_hash="$(sha256sum "$source_file" | cut -d ' ' -f 1)"
source_size="$(wc -c <"$source_file")"
printf 'Source SHA-256: %s\nSource bytes: %s\n' "$source_hash" "$source_size"
```

1. Open the frontend validation domain.
2. Sign in with the disposable operator account.
3. Publish the prepared private transcript source file through the product.
4. Record the transcript ID.
5. Record the time of this first encrypted write.
6. Open the new R2 bucket in the Cloudflare dashboard.
7. Confirm that the bucket contains a new `.bin` object.
8. Record the object key.
9. Download the new object from the Cloudflare dashboard.
10. Record the downloaded local file path.
11. Run this command block on the trusted workstation:

```sh
set -e
ciphertext_file='<DOWNLOADED_FILE>'
test_marker='<UNIQUE_TEST_TEXT>'

if grep -aFq -- "$test_marker" "$ciphertext_file"; then
  printf '%s\n' 'STOP: the R2 object contains the plaintext test marker.' >&2
  exit 1
fi

if jq -e . "$ciphertext_file" >/dev/null 2>&1; then
  printf '%s\n' 'STOP: the R2 object is valid JSON.' >&2
  exit 1
fi

printf '%s\n' 'PASS: the R2 object does not contain the marker and is not JSON.'
```

12. Confirm that the R2 object has type `application/octet-stream`.
13. Delete the downloaded local file.
14. Read the transcript through the authenticated frontend validation domain.
15. Require the rendered transcript body to contain the exact private text marker.
16. Send an authenticated request to
    `https://<BACKEND_VALIDATION_DOMAIN>/api/v1/pull/transcripts/<TRANSCRIPT_ID>`.
17. Require the response to identify the private transcript.
18. Send an authenticated request to
    `https://<BACKEND_VALIDATION_DOMAIN>/api/v1/pull/transcripts/<TRANSCRIPT_ID>/content`.
19. Save the authorized response body to a local file.
20. Require the body to contain the exact marker and match the recorded source hash and byte size.

```sh
set -e
response_file='<AUTHORIZED_CONTENT_RESPONSE_FILE>'
test_marker='<UNIQUE_TEST_TEXT>'
expected_source_hash='<RECORDED_SOURCE_SHA256>'
expected_source_size='<RECORDED_SOURCE_BYTES>'

grep -Fq -- "$test_marker" "$response_file"
response_hash="$(sha256sum "$response_file" | cut -d ' ' -f 1)"
response_size="$(wc -c <"$response_file")"
test "$response_hash" = "$expected_source_hash"
test "$response_size" = "$expected_source_size"
printf '%s\n' 'PASS: authorized content matches the marker, SHA-256 hash, and byte size.'
rm -f "$response_file"
unset response_file test_marker expected_source_hash expected_source_size response_hash response_size
```

21. Republish the disposable transcript.
22. Confirm that Village creates a new `.bin` object.
23. Record the new object key.
24. Check whether the old object key still exists.
25. Record successful cleanup when the old object key is absent.
26. Search the durable log sink for a matching `transcript_blob_reconciliation_required` event when the old object remains.
27. Stop when the old object remains and no matching event exists.
28. Run the conservative reconciliation procedure for each matching event.
29. Publish the prepared public transcript source file with `public` visibility.
30. Record its transcript ID and current object key.
31. Repeat steps 6 through 20 for the public transcript, source hash, byte size, and unique marker.
32. Publish the prepared shared transcript source file with `shared` visibility.
33. Record its transcript ID and current object key.
34. Repeat steps 6 through 20 for the shared transcript, source hash, byte size, and unique marker.

Do not use a health response as the encryption test. Do not insert test rows with SQL.

### Test the conditional response

Use the private transcript ID recorded in step 4 of the encryption test.
Use the transcript after the successful republish.

Use the authenticated content route:

```text
GET /api/v1/pull/transcripts/<TRANSCRIPT_ID>/content
```

1. Send the first authenticated request.
2. Record the `ETag` response header.
3. Create a second authenticated request.
4. Add the recorded value as the `If-None-Match` request header.
5. Send the second request.
6. Require HTTP status `304`.
7. Require an empty response body.

### Remove the disposable data

1. Open the new PostgreSQL service in Railway.
2. Open the **Data** tab.
3. Run this query for each recorded transcript ID:

```sql
SELECT id, blob_key
FROM public.transcripts
WHERE id = '<TRANSCRIPT_ID>';
```

4. Record the current `blob_key` as the R2 object key.
5. Delete each disposable transcript through the product.
6. Run this query for each recorded transcript ID:

```sql
SELECT count(*) AS transcript_rows
FROM public.transcripts
WHERE id = '<TRANSCRIPT_ID>';
```

7. Require `transcript_rows` to be zero.
8. Open the new R2 bucket in Cloudflare.
9. Search for each recorded object key.
10. Record successful cleanup for each absent object key.
11. Search the durable log sink for a matching `transcript_blob_reconciliation_required` event when an object remains.
12. Stop when an object remains and no matching event exists.
13. Run the conservative reconciliation procedure for each matching event.
14. Confirm that no object-cleanup event remains unresolved.

### Change to the production domains

1. Keep production ingress closed.
2. In the new Railway backend service, set `FRONTEND_URL` to the production frontend origin.
3. In the new Railway backend service, set `BASE_URL` to the production backend origin.
4. In the new Railway frontend service, set `NEXT_PUBLIC_API_URL` to the production backend API URL.
5. Register each production callback URL with its identity provider.
6. Set the production frontend API URL on the trusted workstation.

```sh
export NEXT_PUBLIC_API_URL='https://<PRODUCTION_BACKEND_DOMAIN>/api/v1'
scripts/verify-production-artifacts.sh '<FULL_MERGE_COMMIT_SHA>'
```

7. Require a successful production artifact check.
8. Confirm that the GitHub `develop` branch still points to the full merge commit SHA.
9. Apply **Shared deploy and immutable-revision proof** to the backend service with the full merge commit SHA.
10. Apply **Shared deploy and immutable-revision proof** to the frontend service with the full merge commit SHA.
11. Record both approved digests as the production-domain deployment artifacts.

### Route the production domains to the new services

These steps use Cloudflare as the DNS provider.
They keep the production CNAME records **DNS Only**.
Clients connect directly to Railway and validate Railway's custom-domain certificate.
This is a narrow exception to Railway's normal proxied Cloudflare guidance.
It applies only to the actual production hostnames in the deployment record.

**Locations:** Railway dashboard for custom domains, Cloudflare dashboard for DNS, and the trusted workstation for certificate and route probes.

1. Open the new Railway backend service.
2. Open **Settings**.
3. Open **Networking**.
4. Select **Custom Domain**.
5. Enter the production backend domain.
6. Select the backend service port.
7. Record the CNAME name and target that Railway supplies.
8. Record the TXT name and value that Railway supplies.
9. Repeat steps 1 through 8 for the new frontend service and production frontend domain.
10. Open the production domain in the Cloudflare dashboard.
11. Open **DNS** and then **Records**.
12. Add or edit the CNAME record for the production backend domain.
13. Set **Target** to the backend CNAME target from Railway.
14. Set **Proxy status** to **DNS Only**.
15. Set **TTL** to **Auto**.
16. Save the backend CNAME record.
17. Add or update the backend TXT record with the exact name and value from Railway.
18. Repeat steps 12 through 17 for the production frontend domain.
19. Wait for a green verification mark for both custom domains in Railway.
20. Wait for Railway to issue a valid certificate for both custom domains.
21. Confirm in Cloudflare that both CNAME records remain **DNS Only**.
22. Confirm that the verified hostnames exactly match the DNS-only exception in the deployment record.
23. Verify each actual hostname from the trusted workstation:

```sh
set -e
for hostname in '<PRODUCTION_BACKEND_DOMAIN>' '<PRODUCTION_FRONTEND_DOMAIN>'; do
  openssl s_client \
    -connect "$hostname:443" \
    -servername "$hostname" \
    -verify_hostname "$hostname" \
    -verify_return_error \
    < /dev/null
done
```

24. Require `Verify return code: 0 (ok)` for both hostnames.
25. Record the Railway certificate subject, validity result, and verification time for each actual hostname.
26. Stop when a custom domain does not verify, differs from the recorded exception, or has no valid Railway certificate.

The verified CNAME records open production ingress.

27. Send a unique request through the production backend domain:

```sh
set -e
route_test_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"
backend_status="$(curl -sS -o /dev/null -w '%{http_code}' "https://<PRODUCTION_BACKEND_DOMAIN>/route-test-$route_test_id")"
frontend_status="$(curl -sS -o /dev/null -w '%{http_code}' 'https://<PRODUCTION_FRONTEND_DOMAIN>/')"

test "$backend_status" = '404'
test "$frontend_status" = '200'

printf 'Backend status: %s\nFrontend status: %s\n' "$backend_status" "$frontend_status"
printf 'Route test ID: %s\n' "$route_test_id"
```

28. Stop when the backend does not return `404` or the frontend does not return `200`.
29. Open the new backend deployment's HTTP logs in Railway.
30. Find the request for `/route-test-<ROUTE_TEST_ID>`.
31. Confirm that its host is the production backend domain.
32. Confirm that the deployment source is the full merge commit SHA.
33. Open the new frontend deployment's HTTP logs in Railway.
34. Confirm that a request reached the production frontend domain.
35. Confirm that the deployment source is the full merge commit SHA.
36. Complete one authenticated sign-in and read test through the production domains.
37. Remove the validation domains from the new Railway services.
38. Allow merges to `develop` again.

## 7. Stabilization and rollback

### Monitor the new environment

**Locations:** Railway dashboard and the durable backend log sink.

1. Review publish, read, pull, republish, and delete logs during stabilization.
2. Review key, authentication, and object errors during stabilization.
3. Review errors that contain `transcript storage mutation rejected`.
4. Treat this error as proof that an old or unsupported process attempted a storage write.
5. Confirm that new R2 objects remain binary ciphertext.
6. Review logs for `transcript_blob_reconciliation_required`.
7. Use the conservative procedure in
   [`transcript-encryption-operations.md`](transcript-encryption-operations.md#conservative-reconciliation)
   for each object-cleanup event.
8. Confirm that no unresolved object-cleanup event remains.
9. Keep the old resources during the approved stabilization period.

### Close production ingress after a failure

**Locations:** Railway and Cloudflare dashboards.

Use this checklist when a stop condition requires closed production ingress.

1. Open **DNS** and then **Records** in Cloudflare.
2. Delete the CNAME records for the production backend and frontend domains.
3. Open **Settings** and then **Networking** for each new Railway service.
4. Remove each production custom domain.
5. Stop the new Railway backend and frontend services.
6. Confirm that neither production domain reaches Village.
7. Preserve the deployment, log, database, and R2 evidence.

Close production ingress immediately if the application writes plaintext.

Close production ingress immediately if the application reports an integrity-check failure.

Close production ingress immediately if the logs contain `transcript storage mutation rejected`.

Close production ingress immediately if storage behavior does not match the expected database or R2 result.

### Abort before the rollback boundary

**Locations:** Railway and Cloudflare dashboards, with validation commands on the trusted workstation.

Use this checklist only when Village has accepted no encrypted transcript write.

1. Stop the new Railway backend and frontend services.
2. Confirm that the new PostgreSQL database has no transcript row.
3. Open the new R2 bucket in the Cloudflare dashboard.
4. Inventory the complete bucket object list.
5. Require the object count to be zero.
6. Search the durable log sink for `transcript_blob_reconciliation_required`.
7. Require zero unresolved object-cleanup events for the new bucket.
8. Stop the abort when an object or unresolved event remains.
9. Open the old Railway environment.
10. Issue a replacement credential for the old PostgreSQL database.
11. Set the old backend `DATABASE_URL` to the old PostgreSQL database.
12. Confirm that the old backend has no new R2 or KEK variable.
13. Confirm that the old backend still identifies the old object store.
14. Start the old Railway backend and frontend services.
15. Start only the old workers and jobs that appear in the recorded pre-window inventory.
16. Confirm that all old deployments use the pre-window Git revision from the deployment record.
17. Add each recorded production custom domain to its old Railway service.
18. Record the CNAME and TXT values that Railway supplies for each restored custom domain.
19. Open **DNS** and then **Records** in Cloudflare.
20. Restore each production CNAME and TXT record with the values from step 18.
21. Restore the recorded proxy status and TTL value.
22. Wait for a green verification mark for each restored custom domain in Railway.
23. Complete one authenticated sign-in and read test against the old database and old object store.
24. Confirm that no old service uses the new PostgreSQL database or R2 bucket.
25. Record the abort result and evidence.

### Use the correct rollback rule

**Location:** private deployment record and the approved recovery systems named there.

Before the rollback boundary, complete the abort checklist when any validation step fails.

After the rollback boundary, use only a Village revision that supports encrypted transcript storage.

Do not deploy an older Village revision after the rollback boundary.

Do not delete an old database, bucket, or credential during this setup procedure.

Use the approval and deletion steps in
[`transcript-encryption-cutover.md`](transcript-encryption-cutover.md#credential-revocation-and-old-bucket-deletion).

## Reference material

This section is not part of the execution path.

Last verified: **2026-08-01**.

The approved recovery runbook revisions in the deployment record are authoritative
for external backup and restore tools.

- [Railway CLI installation](https://docs.railway.com/guides/cli#installing-the-cli)
- [Railway environments](https://docs.railway.com/environments)
- [Railway GitHub autodeploy controls](https://docs.railway.com/guides/github-autodeploys)
- [Railway PostgreSQL](https://docs.railway.com/databases/postgresql)
- [Railway connect](https://docs.railway.com/cli/connect)
- [Railway SSH](https://docs.railway.com/cli/ssh)
- [Railway variables and Git-provided revisions](https://docs.railway.com/reference/variables#git-variables)
- [Railway public networking](https://docs.railway.com/networking/public-networking)
- [Railway custom domains](https://docs.railway.com/networking/domains/working-with-domains)
- [Railway logs and retention](https://docs.railway.com/observability/logs)
- [Railway third-party observability](https://docs.railway.com/guides/third-party-observability)
- [Cloudflare R2 bucket creation](https://developers.cloudflare.com/r2/buckets/create-buckets/)
- [Cloudflare R2 public access](https://developers.cloudflare.com/r2/buckets/public-buckets/)
- [Cloudflare R2 token authentication](https://developers.cloudflare.com/r2/api/tokens/)
- [Cloudflare R2 data location](https://developers.cloudflare.com/r2/reference/data-location/)
- [Cloudflare R2 rclone configuration](https://developers.cloudflare.com/r2/examples/rclone/)
- [Cloudflare DNS records](https://developers.cloudflare.com/dns/manage-dns-records/how-to/create-dns-records/)
- [PostgreSQL downloads](https://www.postgresql.org/download/)
- [OpenSSL downloads](https://www.openssl.org/source/)
- [Docker Engine installation](https://docs.docker.com/engine/install/)
- [`jq` installation](https://jqlang.org/download/)
- [GNU Coreutils](https://www.gnu.org/software/coreutils/)
- [`curl` downloads](https://curl.se/download.html)

## Change log

| Date | Change |
|---|---|
| 2026-07-31 | Added the first Railway PostgreSQL and Cloudflare R2 procedure. |
| 2026-08-01 | Replaced the audit narrative and AWS CLI commands with step-by-step executable checklists grouped by system. |
