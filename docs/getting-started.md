# Getting started: install, connect, use

This guide takes you from nothing to running read-only queries against your
Jira Data Center. Reference material lives in the other documents linked at
the end.

## 1. Choose how to run it

| Your machine | Recommended | Why |
|---|---|---|
| **macOS** | **Native binary** | Uses the Keychain for TLS trust (company CAs installed by IT just work) and for your token. No CA file needed |
| **Windows** | Native binary, installed from Git Bash with `install.sh` | Uses the Windows certificate store and Credential Manager |
| **Linux** | Native binary | Uses the system CA bundle; secret-service keyring when available |
| No native binary allowed, or you want isolation | **Docker wrapper** (Linux, macOS, WSL) | Only Docker needed. The container cannot see your OS keychain or certificate store, so you provide the company CA as a file (section 4) and the token is kept in a `0600` file |

## 2. Install

### Native, from a downloaded archive (no Go, no GitHub CLI)

1. Download from the release page (or the files you were sent):
   - `jira-cli_<version>_darwin_arm64.tar.gz` (Apple Silicon) or `_darwin_amd64` (Intel Mac), or `_linux_amd64` / `_linux_arm64`;
   - `SHA256SUMS`;
   - `install.sh`.
2. Put the three files in one folder, open a terminal there, and run:

```bash
sh install.sh --archive jira-cli_<version>_darwin_arm64.tar.gz
```

The installer verifies the SHA-256, installs to `~/.local/bin/jiracli`, adds that
directory to your `PATH` and, on macOS, removes the browser's quarantine flag
so Gatekeeper does not block the (not yet code-signed) binary. Open a new
terminal afterwards. To update, run the same command with the new archive.

Windows: download the `_windows_amd64.zip`, `SHA256SUMS` and `install.sh`, then
in **Git Bash** (comes with Git for Windows):

```bash
sh install.sh --archive jira-cli_<version>_windows_amd64.zip
```

The PATH entry applies to Git Bash; for cmd/PowerShell see
[installation.md](installation.md#windows-git-bash).

### Native, from a release

```bash
curl -fsSL https://github.com/Personal-Stack/jira-cli/releases/latest/download/install.sh | sh
```

Upgrade later with `jiracli upgrade`.

### Docker image from the release (no clone, no Go)

Every release publishes `ghcr.io/personal-stack/jira-cli:<version>`
(linux/amd64 + arm64, public, no login needed):

```bash
curl -fsSL https://github.com/Personal-Stack/jira-cli/releases/latest/download/install.sh | sh -s -- --docker
```

This installs the `jiracli` wrapper using the released image. To use plain
`docker run` instead, see [docker.md](docker.md#use-the-published-image-directly-no-wrapper).
Update by re-running the installer with the new `--version`.

### Docker wrapper from a clone

Needs Docker and a clone of the repository; Go runs inside the image build.

```bash
git clone https://github.com/Personal-Stack/jira-cli.git
```

```bash
sh jira-cli/scripts/install.sh --docker
```

**Updating:** the wrapper runs the image built from your clone, so CLI fixes
arrive only when you rebuild it:

```bash
cd jira-cli && git pull && sh scripts/install.sh --docker
```

Check: `jiracli --version` shows the new commit (e.g. `v0.1.0-6-g84dd1a9`).

## 3. Configure

```bash
jiracli config setup
```

It asks for the Jira URL, the name for the token, read-only, and a company CA
file if you need one (section 4). Each answer is checked when you give it: the
URL must be reachable, and a CA file must hold certificates. After writing the
file it offers to store the token (hidden input, verified against Jira, asked
again if Jira refuses it) and to run `jiracli doctor`, so one session ends with
a working CLI. Both are optional: answer no and run `jiracli auth login` and
`jiracli doctor` later. The file goes to
`~/.config/jira-cli/config.yaml` (Windows: `%APPDATA%\jira-cli\config.yaml`),
and the folder is created if needed. Run it again to change an instance or add
another one (`--instance staging`). Only the values it asks about change.
Flags (`--base-url`, ...) skip the
questions. In a terminal, any command run without a configuration offers to
create one, and any command run without a stored token offers to store it.

The resulting file contains no secrets:

```yaml
version: 1
default_instance: prod
instances:
  prod:
    deployment: datacenter
    base_url: https://jira.company.internal      # add the context path if Jira has one, e.g. /jira
    auth:
      type: pat
      credential_ref: jira-prod                    # a name for the token, not the token
    # tls:                                         # only if section 4 says you need it
    #   ca_bundle: /Users/<you>/.config/jira-cli/company-ca.pem
    read_only: true                                # recommended for production
```

```bash
jiracli config validate
```

## 4. Company certificate (only if needed)

**Do you need it?**

- **Native on macOS or Windows:** usually **no**. If your browser opens Jira without a warning, the CLI trusts it too. Leave `tls:` out of the config and continue with section 5.
- **Docker wrapper:** usually **yes**, if Jira uses a company (internal) CA. The container only trusts public CAs.
- **Native on Linux:** only if the company CA is not installed in the system bundle.

`jiracli doctor` tells you: a `tls` check failing with `tls_certificate` /
`unknown_certificate_authority` means you need it.

### Option A: let the CLI download it (simplest; works with Docker)

```bash
jiracli certs fetch                                   # uses base_url from your config
```

```bash
jiracli certs fetch https://jira.company.internal     # or any URL
```

The command:

- reads the server's certificate chain and, if the server omits the root (common), follows the "CA Issuers" links in the certificates, keeping a downloaded certificate only if it cryptographically signs the one below it;
- shows every certificate with its **SHA-256 fingerprint** and asks you to type `yes` (`--yes` for scripts; `--dry-run` only shows);
- saves the CA certificates (never the server's own certificate) to `~/.config/jira-cli/<host>-ca.pem`, proves the file works with a verified handshake, and prints the `ca_bundle` line to put in your config.

**Trust on first use:** the certificates come from the network you're about
to trust. Compare the root's fingerprint with the one your IT/PKI team
publishes, especially on networks you don't control. If the output says
`root_found: false`, the saved file ends at an intermediate CA. That works,
but the intermediate expires sooner than the root; get the root from IT or the
browser when you can.

### Option B: export the CA certificate from your browser

Open Jira in the browser first. You want the **top** certificate of the chain
(the root CA, the one whose name usually contains "Root"), not the Jira
server's own certificate. Menu names vary slightly between browser versions.

**Chrome / Edge (macOS, Windows, Linux)**

1. Click the icon left of the address (padlock or "tune" icon), then **Connection is secure**, then **Certificate is valid**.
2. In the certificate viewer, open the **Details** tab.
3. Under **Certificate Hierarchy**, select the **top** entry.
4. Click **Export…** and save it as **Base64-encoded ASCII, single certificate**, e.g. `company-ca.pem`.

**Firefox**

1. Click the padlock, then **Connection secure**, then **More information**, then **View Certificate**.
2. A page opens with one tab per certificate in the chain. Select the **last tab** (the root).
3. Under **Miscellaneous**, choose **Download: PEM (cert)**.

**Safari (macOS)**

1. Click the padlock in the address bar, then **Show Certificate**.
2. Select the **top** certificate in the chain shown at the top of the sheet.
3. Drag the large certificate icon to your Desktop. This creates a `.cer` file (binary DER), which the CLI accepts as-is.

Any of these formats works: PEM, or binary DER (`.cer`, `.cert`, `.crt`). If
the file name has spaces, rename it (`mv "Company Root CA.cer" company-ca.cer`)
or always put it in quotes.

### Check it and wire it up

```bash
openssl x509 -in company-ca.pem -noout -subject -issuer -enddate -fingerprint -sha256
```

(For a binary `.cer`, add `-inform der`.) For a root CA, **subject and issuer
are the same**. If you can, compare the SHA-256 fingerprint with what your IT
or PKI team publishes: the browser shows you what *your network* presented.

Put the file in the CLI's config directory. For the Docker wrapper it **must**
be there, because only that directory is shared with the container:

```bash
mkdir -p ~/.config/jira-cli && cp company-ca.pem ~/.config/jira-cli/company-ca.pem
```

In `config.yaml` (absolute path; `~` is not expanded):

```yaml
    tls:
      ca_bundle: /Users/<you>/.config/jira-cli/company-ca.pem
```

### If it still fails

- The server may send an **incomplete chain**. Browsers fetch the missing intermediate silently; the CLI does not. Export the **intermediate** too (the entry just below the root) and combine both: `cat company-root.pem company-intermediate.pem > ~/.config/jira-cli/company-ca.pem`.
- A corporate proxy may **inspect TLS** (Zscaler, Netskope, Palo Alto...). Then the top of the chain is the proxy's CA, which is the one you need.
- `jiracli certs fetch --dry-run` shows the chain the CLI sees, including through your proxy.
- Diagnose precisely, from a clone: `scripts/tls-diagnose.sh jira.company.internal ~/.config/jira-cli/company-ca.pem`. It shows the server's chain and says for each certificate whether it works as `ca_bundle`.

## 5. Token (Personal Access Token)

1. In Jira: click your avatar, then **Profile**, then **Personal Access Tokens**, then **Create token**, and set an expiry. For production, use a low-privilege account: the token can do whatever that user can.
2. Store it:

```bash
jiracli auth login
```

Paste the token at the hidden prompt. It is verified against Jira and stored in
the Keychain (macOS), Credential Manager (Windows), Secret Service (Linux
desktop), or `~/.config/jira-cli/credentials/<ref>` with mode `0600` (Docker
wrapper). It is never put in the config, on the command line or in logs.
Other options (mounted files, environment variables for CI) are in
[credentials.md](credentials.md).

## 6. Check the connection

```bash
jiracli doctor
```

Expect `"ok": true`. The checks, in order:

| Check | Meaning |
|---|---|
| `config` | config file valid, CA file readable |
| `dns`, `tcp` | host resolves, port reachable |
| `tls` | certificate trusted; shows issuer and expiry |
| `http` | Jira answers. A **401 from Jira** here is normal: production Jira usually disables anonymous access, and `doctor` says so |
| `credentials` | token found (shows where, never the value) |
| `authentication` | token accepted (`/rest/api/2/myself`) |
| `version` | your Jira version (checked with the token when anonymous access is off); warns if it is not 10.3.x |

Docker wrapper and a Jira or proxy reachable only via `localhost`/VPN DNS:
`export JIRA_DOCKER_NETWORK=host` (Linux).

## 7. Use it

```bash
jiracli issue search --jql 'project = ABC ORDER BY updated DESC' --max-results 5 --fields summary,status -o table
```

```bash
jiracli issue get ABC-123 --fields summary,status,assignee
```

```bash
jiracli commands list --group issue -o table          # what exists
```

```bash
jiracli commands describe issue create                 # parameters, body, risk
```

- Output: JSON by default; `-o yaml`, `-o table`.
- Pagination: `--all` follows pages; on production add `--max-pages 3` and keep `--max-results` small.
- Writes: preview with `--dry-run`. Destructive and administrative commands need `--yes`. With `read_only: true` (or `JIRA_READ_ONLY=1`) every non-read command is refused before anything is sent.
- Bodies: `--file issue.json` or `--stdin`. With the Docker wrapper, files must be in or below the current directory.
- Tracing: `--log-level debug --log-format json --log-file trace.jsonl`. Tokens are always redacted; hostnames and JQL are not.
- Stopping: Ctrl+C stops the command cleanly (exit code 130): requests are cancelled and prompts end at once. If it is still busy, it says so; press Ctrl+C again to exit immediately.

## 8. Production checklist

1. The token is from a low-privilege account.
2. `read_only: true` in the config, and `export JIRA_READ_ONLY=1`.
3. Guard test before any real request: `jiracli issue delete ABC-1 --yes` must exit `9` (`read_only_mode`).
4. Small queries: `--max-results` of 50 or less, `--max-pages 3` with `--all`.
5. Tell the Jira admins: requests carry `User-Agent: jira-cli/<version>`, and `--log-file` traces include Jira's request IDs.

## 9. Verify everything at once (read-only)

From a clone, with `jiracli` on your PATH (native or Docker wrapper):

```bash
scripts/phase4-readonly.sh --project <KEY>
```

It first proves that writes are refused, then runs about 25 read commands
(search, pagination, issue details, boards, users, version) and checks from
the trace that nothing but reads was sent. Send back `summary.txt` and
`trace.jsonl` from the `phase4-<timestamp>/` directory it creates; review the
trace first, as it contains hostnames, JQL and issue keys (never tokens).

## More

[installation.md](installation.md) (all installer options) ·
[docker.md](docker.md) · [credentials.md](credentials.md) ·
[secure-transport.md](secure-transport.md) · [logging.md](logging.md) ·
[troubleshooting](../skills/jira/references/troubleshooting.md)
