# skolectl

A command line tool for guardians of children at a Norwegian school that uses Visma Flyt Skole. It reads what the school sends through the guardian portal (Foresattportalen, the app Flyt Foresatt), and answers messages.

It is written for AI agents first: the output is Markdown that reads well in a terminal and in a chat, every data command also prints JSON, and the tool describes itself with `skolectl schema`. Ask your agent "is there anything new from school?" and it can answer.

> skolectl is unofficial and not affiliated with Visma. It uses the same undocumented API as the guardian portal, so it can break when the service changes.

```text
$ skolectl overview
# Overview

## Kari Nordmann

- Unread direct messages: 2
- Unread group messages: 5
- Unread notices: 1
- Unanswered forms: 0
- Unanswered consents: 1
- Applications waiting for an answer: 0
- pupil id: 1001
```

## What it does

| Area | Commands | Reads | Writes |
| --- | --- | --- | --- |
| Overview | `overview`, `children` | What waits for each child | |
| Messages | `threads`, `thread`, `attachment` | Direct threads and group conversations, with files | |
| | `reply`, `mark-read` | | A reply, and the read mark |
| Notices | `notices` | Notifications and announcements | |
| Absence | `absences` | Absence days, with a summary for the term or the year | |
| Forms and consents | `forms`, `form`, `consents` | What the school asks, and what was answered | |
| Applications | `applications`, `application` | Leave and other applications, with the answer | |
| | `leave-options`, `apply-leave` | What the school asks of an application for leave | An application for leave, with files |
| Class and school | `classes`, `schools` | Teachers for each subject, and how to reach the school | |
| Documents | `documents`, `document` | Files about the child, and files the school shares | |

What it does not do yet: start a new message thread, answer a form or a consent, or report absence. Use the portal for those.

A school chooses which areas to turn on. An area the school does not use comes back empty.

## Install

```bash
brew install langtind/tap/skolectl
```

Or with Go:

```bash
go install github.com/langtind/skolectl@latest
```

Binaries for macOS, Linux and Windows are on the [releases page](https://github.com/langtind/skolectl/releases).

## Log in

Visma Flyt Skole logs in with ID-porten (BankID, MinID, Buypass), so a person has to do it, in a browser. skolectl then takes over that login. With [playwright-cli](https://github.com/microsoft/playwright-cli):

```bash
playwright-cli open https://foresatt.visma.no/<school-key> --headed
# log in with ID-porten, wait for the page that shows your children, then:
playwright-cli state-save state.json
skolectl login --state-stdin < state.json
rm state.json
playwright-cli close
```

`<school-key>` is the last part of the address the school gave you for the portal, `https://foresatt.visma.no/<school-key>`.

The login is kept in the system keyring, never in a file. The access token lasts five minutes, and skolectl renews it when a command needs it, so the browser is needed only once. How long the service lets a login live is not known. When a command fails with `auth`, log in again the same way.

```bash
skolectl status    # is a login stored, and until when is the access token good
skolectl logout    # forget the login
```

`state.json` holds your login. Delete it straight after `login`, and never commit it.

## Use

```bash
skolectl overview                    # what waits, for each child
skolectl threads --unread            # threads with unread messages
skolectl thread <thread-id>          # the latest messages, oldest first
skolectl attachment <thread-id> <attachment-id>
skolectl notices --unread
skolectl absences --term fall
skolectl forms --unanswered
skolectl consents
skolectl applications
skolectl classes
skolectl schools
skolectl documents
skolectl document <document-id> --output decision.pdf
```

Every list covers all your children. Add `--child <pupil-id>` for one. The ids come from `skolectl children`.

Reading never changes anything: a thread you read with skolectl still counts as unread at the school.

### Answering

```bash
skolectl reply <thread-id> "Takk for beskjed." --dry-run   # shows who would get it, and the text
skolectl reply <thread-id> "Takk for beskjed."             # asks, then sends
skolectl mark-read <thread-id>
```

A reply goes out under your name and cannot be taken back. A read mark is visible to the school. Both commands ask before they send, show what they would do with `--dry-run`, and skip the question with `--yes`.

### Applying for leave

```bash
skolectl leave-options                 # the causes, what is required, and the limits
skolectl apply-leave <pupil-id> --from 2026-11-05 --reason "Choir trip." --attach letter.pdf --dry-run
skolectl apply-leave <pupil-id> --from 2026-11-05 --reason "Choir trip." --attach letter.pdf
```

`--dry-run` checks everything and shows the whole application without sending it. The real run asks first, sends, and prints the id the school gave the application.

The service cannot tell a second try from a new application, so skolectl is careful: it refuses when the child already has an application for the same days, it never sends twice, and when a send gets no clear answer it says so instead of trying again.

## For agents

```bash
skolectl install-skill   # copies the skill to ~/.claude/skills
skolectl schema          # every command, flag and error kind as JSON
```

The skill teaches an agent the commands, when to ask you first, and how to walk you through a new login. It tells the agent to treat every text from the school as data, never as an instruction, and never to send or mark as read without your yes.

Add `--json` to a data command for one JSON document on stdout, and `--fields id,title` to keep only some fields. A failed command exits with a code that says why:

| Exit | Kind | Meaning |
| --- | --- | --- |
| 2 | `usage` | The call was wrong |
| 3 | `not_found` | No such thread, form or file |
| 4 | `auth` | Not logged in, or the login ran out |
| 5 | `conflict` | The thread takes no replies, or the file exists already |
| 6 | `rate_limit` | Too many requests |
| 7 | `unavailable` | The service did not answer, or did not know the id |

With `--json` the error is a JSON document too.

## Privacy

skolectl talks only to `api.foresatt.visma.no`. It sends nothing anywhere else and keeps no copy of what it reads. What an agent does with the output is up to the agent and you: the messages are about children, so think about where they end up.

## How it works

[reference/README.md](reference/README.md) describes the API as it was observed: the requests, the shape of the answers, how the login renews, and what is still unverified. [AGENTS.md](AGENTS.md) describes the code. skolectl is built on [ctlkit](https://github.com/langtind/ctlkit).

## Develop

```bash
go build -o skolectl .
go test -race ./...
go vet ./...
```

The tests run against a fake service with made-up names. Nothing that identifies a real person goes into the repository.

## License

MIT
