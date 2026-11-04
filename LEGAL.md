# Legal notice and terms of use

Personal Stack jira-cli ("jira-cli") is an **independent, unofficial,
open-source command-line client** from the
[Personal Stack](https://github.com/Personal-Stack) organization.
It connects to a Jira instance that you already have access to, through
Jira's public REST API. You can use it from a terminal, from scripts, and from
AI agents (for example in a Docker Sandbox), with the credentials kept
secure.

## Not affiliated with Atlassian

- jira-cli is **not** made, endorsed, sponsored, supported or approved by
  Atlassian. It is not an Atlassian product, and nothing in this project
  should be read as suggesting otherwise.
- It does **not** replace, reproduce or compete with any Atlassian product.
  It needs a Jira instance and works only through the interfaces Jira provides
  to every client.
- "Atlassian", "Jira", "Jira Software" and "Jira Data Center" are trademarks of
  Atlassian. They are used here only to say which product this tool works
  with. All other names (Docker, OpenCode, GitHub and others) are trademarks of
  their respective owners.

## Built from public information only

- The API commands are generated from the **Jira Software Data Center 10.3.26
  REST API specification that Atlassian publishes**, plus Atlassian's public
  documentation. No confidential or non-public Atlassian information, source
  code or binaries were used. Nothing was obtained by reverse engineering or
  by circumventing any protection.
- That specification (`specs/official/`) and the descriptions derived from it
  (in `generated/`, and compiled into the binary) remain Atlassian's material,
  subject to Atlassian's terms. They are **not** covered by this project's
  license (see [NOTICE](NOTICE)).

## Your responsibilities when you use it

These points describe how to use the tool appropriately. They do not limit the
rights the Apache License 2.0 grants you.

- **Authorized access only.** Use jira-cli only with Jira instances and
  accounts you are authorized to use. It acts with the permissions of the
  token you give it, exactly as Jira's web interface or any other API client
  would.
- **Your terms still apply.** Using jira-cli does not change your agreements
  with Atlassian, or your organization's policies for Jira (for example API
  usage, rate limits, data handling, and the use of automation or AI agents).
  Complying with them is your responsibility.
- **Agents act on your behalf.** When an AI agent runs jira-cli, you are
  responsible for what it does with your access. The defaults are
  conservative to help with that: sandboxes set up with `jiracli setup-sbx` are
  read-only, and destructive operations require confirmation. Keep them
  unless you have a reason to change them.

## What jira-cli does with your data

- It sends requests only to the Jira base URL you configure. It downloads
  from GitHub only when you ask it to install or set up something.
- It has **no telemetry and no analytics**, and it sends nothing to the
  project's authors or anyone else.
- Tokens are read from where you put them (the OS keyring, files, environment
  variables, or an sbx secret). They are never written to the configuration or
  to logs, and they are only sent to your Jira.

## No warranty, no liability

jira-cli is provided **"as is", without warranties or conditions of any
kind**, and its authors are **not liable** for damages arising from its use.
That includes changes made to a Jira instance through it, and service
interruptions or account restrictions applied by the Jira operator. This is a
summary; sections 7 and 8 of the [Apache License 2.0](LICENSE) are the binding
terms.

## Questions and requests

This notice is not legal advice. If you are Atlassian or another rights
holder and believe something here should change (a name, a file, a wording),
please open an issue on the project's repository. It will be addressed
promptly.
