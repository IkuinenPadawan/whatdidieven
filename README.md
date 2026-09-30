# whatdidieven

> Turn your git log into a timesheet. Locally. In one command.

`whatdidieven` reads your recent commits, sends them to a local LLM, and prints a daily breakdown of what you worked on. Tasks get grouped, ticket numbers get pulled out, and each one gets a rough hour estimate.

No cloud APIs. No telemetry. No third-party Go dependencies. Your commit history stays on your machine.

## Quick start

Ensure Go is installed.

```sh
# 1. Start a local LLM server on :8080 (see "LLM server" below)
# 2. Install
go install github.com/IkuinenPadawan/whatdidieven@latest
# 3. Add binary to path
export PATH="$HOME/go/bin:$PATH"
# 4. Run from inside any git repo
cd ~/code/my-project && whatdidieven this-week
```

## Why

It's Friday at 16:47. Your timesheet is empty. Monday's a blur, Tuesday is a complete mystery, and Wednesday you're pretty sure you were in meetings. Or was that Thursday? You stare at `git log`, find a commit called `fix: actually fix it this time` from a day you have no memory of, and somehow that's supposed to become billable hours.

This tool does it for you. It groups related commits into tasks, pulls out ticket IDs, and estimates effort from diff size. Descriptions read like business value, not implementation detail. Pertsi-you can finally clock out.

## Example

```
$ whatdidieven this-week

**2026-04-27**
- [PROJ-412] Implemented session token rotation — ~2h
- Refactored config loader for multi-repo support — ~1.5h
Total: ~3.5h

**2026-04-29**
- [PROJ-418, PROJ-420] Added retry logic to payment webhook — ~3h
- Fixed flaky integration test (?) — ~0.5h
Total: ~3.5h

**Grand total: ~7h**
```

## Future Features
- Jira integration
- Better time estimations
- Computer active time calculation
- Including stats of work done by AI agents

## Current limitations
- Time estimations are aggressively shook from the sleeve of the LLM and are quite off many times

## Install

Requires Go 1.26+ and a running OpenAI-compatible local LLM server (e.g. [llama.cpp](https://github.com/ggerganov/llama.cpp), [LM Studio](https://lmstudio.ai/), [Ollama](https://ollama.com/) with the OpenAI shim).

```sh
go install github.com/IkuinenPadawan/whatdidieven@latest
```

Or, from a local clone of the repo:

```sh
go install ./...
```

The binary lands in `$(go env GOBIN)`, or `$(go env GOPATH)/bin` (usually `~/go/bin`). Make sure that directory is on your `PATH`:

```bash
export PATH="$HOME/go/bin:$PATH"
```

## Usage

Run from inside any git repository:

```sh
whatdidieven            # defaults to today
whatdidieven today      # commits from today
whatdidieven yesterday  # commits from yesterday
whatdidieven this-week  # commits since last Monday
whatdidieven last-week  # commits last week
whatdidieven add        # adds the current working directory git repo to config
```

That's it. Output goes to stdout, so you can pipe it anywhere.

```sh
whatdidieven this-week | glow (https://github.com/charmbracelet/glow)
whatdidieven today > timesheet.md
```

## Configuration
whatdidieven looks for a config file at:

┌──────────┬─────────────────────────────────────────────────────────────────────────────────────────┐
│ Platform │                                          Path                                           │
├──────────┼─────────────────────────────────────────────────────────────────────────────────────────┤
│ Linux    │ $XDG_CONFIG_HOME/whatdidieven/config.json (default: ~/.config/whatdidieven/config.json) │
├──────────┼─────────────────────────────────────────────────────────────────────────────────────────┤
│ macOS    │ ~/Library/Application Support/whatdidieven/config.json                                  │
├──────────┼─────────────────────────────────────────────────────────────────────────────────────────┤
│ Windows  │ %AppData%\whatdidieven\config.json                                                      │
└──────────┴─────────────────────────────────────────────────────────────────────────────────────────┘

The file is optional. If it doesn't exist, the tool creates an empty `config.json` at that path on first run and runs against the current working directory.

Add current working directory git repo to the config:
```sh
whatdidieven add
```

#### Multi-repo

To pull commits from multiple repositories in one summary, list their paths (or add them one by one by the add command):

```json
  {
    "repos": [
      "/home/you/code/backend",
      "/home/you/code/frontend"
    ]
  }
```

  All repos are queried for the same time window and the combined log is sent to the LLM as a single summary.

### Custom endpoint

Set `WHATDIDIEVEN_API_URL` to point at a server on a different host or port:

```sh
export WHATDIDIEVEN_API_URL=http://192.168.1.50:11434/v1/chat/completions
whatdidieven today
```

For endpoints that require authentication, set `WHATDIDIEVEN_API_KEY`. The key is sent as a Bearer token in the `Authorization` header:

```sh
export WHATDIDIEVEN_API_KEY=your-api-key
whatdidieven today
```

To choose the model sent in the API request, set `WHATDIDIEVEN_MODEL`:

```sh
export WHATDIDIEVEN_MODEL='your-model-name'
whatdidieven today
```

If unset, the request uses `local-model`.

## How it works

1. Shells out to `git log --no-merges --decorate=full --stat --author=` for the requested window, where the author is read from `git config user.email` (checked per-repo, so a repo-local override is respected). This filters the log to your own commits. `user.email` must be set, or the tool errors out.
2. Formats each commit with date, hash, subject, and per-file churn.
3. POSTs the log to `http://localhost:8080/v1/chat/completions` with a system prompt tuned for timesheet generation.
4. Prints the model's reply.

The system prompt enforces 0.5h granularity, an 8h daily cap, business-value descriptions, and a `(?)` marker on guesses. See [`main.go`](./main.go) for the full prompt.

## LLM server

Point any OpenAI-compatible server at port `8080` (or set `WHATDIDIEVEN_API_URL` to override the endpoint). A reasonable starting setup:

For example: https://llama.app/docs/serve

```sh
# From a Hugging Face repo (downloaded and cached automatically)
llama serve -hf ggml-org/gemma-4-e4b-it-GGUF:Q4_0

# From a local GGUF file
llama serve -m my-model.gguf
```
The request sends the model from `WHATDIDIEVEN_MODEL` (default: `local-model`) and `temperature: 0.2`. Most local servers ignore the model field and serve whatever you loaded.

## License

MIT
