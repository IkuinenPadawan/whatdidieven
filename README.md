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

This tool does it for you. It groups related commits into tasks, pulls out ticket IDs, and calculates time from the gaps between your commits. Descriptions read like business value, not implementation detail. Pertsi-you can finally clock out.

## Example

```
$ whatdidieven this-week

**2026-04-27 (Monday)**
- (api) [PROJ-412] Implemented session token rotation — ~2h
- (cli) Refactored config loader for multi-repo support — ~1.5h
Total: ~3.5h

**2026-04-29 (Wednesday)**
- (api) [PROJ-418, PROJ-420] Added retry logic to payment webhook — ~3h
- (api) Fixed flaky integration test (?) — ~0.5h
Total: ~3.5h
```

## Future Features
- Jira integration
- Better time estimations
- Computer active time calculation
- Including stats of work done by AI agents

## Current limitations
- Time is calculated from gaps between commits, so it is only a proxy for real work time. The first commit of a day (and the first after a long break) gets a fixed lead-in guess, and gaps longer than the break gap count as breaks.
- Task grouping and descriptions still come from the LLM and can be off on vague commit messages.

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
whatdidieven today      # commits from today (local calendar day)
whatdidieven yesterday  # commits from yesterday
whatdidieven this-week  # commits since this week's Monday
whatdidieven last-week  # commits from last Monday to Sunday
whatdidieven add        # adds the current working directory git repo to config
whatdidieven --dry-run  # print context that would be sent to LLM without LLM call
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

The file is optional. If it doesn't exist, the tool creates an empty `config.json` at that path on first run and runs against the current working directory with default settings.

Available keys:

| Key | Default | Meaning |
|-----|---------|---------|
| `repos` | `[]` | Repositories to include. If empty, the current working directory is used. |
| `break_gap_minutes` | `120` | A gap between two commits longer than this counts as a break, not work time. |
| `lead_in_minutes` | `30` | Time credited to the first commit of a day, and to the first commit after a break. |
| `api_url` | `http://localhost:8080/v1/chat/completions` | Chat completions endpoint of your LLM server. `WHATDIDIEVEN_API_URL` overrides it. |
| `model` | `local-model` | Model name sent in the request. `WHATDIDIEVEN_MODEL` overrides it. |

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

All repos are queried for the same time window. Commits are merged and grouped per day, and each day is summarized in its own LLM request. A repo that fails (e.g. a missing path or no `user.email`) is skipped with a warning.

### Custom endpoint

Set `api_url` in `config.json` to point at a server on a different host or port:

```json
{ "api_url": "http://192.168.1.50:11434/v1/chat/completions", "model": "your-model-name" }
```

Environment variables take priority over the config file, which is handy for one-off runs:

```sh
export WHATDIDIEVEN_API_URL=http://192.168.1.50:11434/v1/chat/completions
whatdidieven today
```

For endpoints that require authentication, set `WHATDIDIEVEN_API_KEY`. It is environment-only and never read from the config file, so it can't end up in a file you commit or share. The key is sent as a Bearer token in the `Authorization` header:

```sh
export WHATDIDIEVEN_API_KEY=your-api-key
whatdidieven today
```

To choose the model sent in the API request, set `model` in the config or `WHATDIDIEVEN_MODEL`:

```sh
export WHATDIDIEVEN_MODEL='your-model-name'
whatdidieven today
```

If unset, the request uses `local-model`.

## How it works

1. Runs `git log --branches --remotes --no-merges --numstat --source` per repo for the requested window (padded by a couple of days, then filtered by author date). The author is read from `git config user.email` (checked per-repo, so a repo-local override is respected), which filters the log to your own commits. `user.email` must be set, or that repo errors out.
2. Sorts commits by time and calculates a duration for each: the gap to the previous commit, or the lead-in time for the first commit of a day or after a gap longer than the break gap. The durations sum to the day's total time.
3. Builds one input per day with the repo, branch, time range, duration, subject, body (capped at 20 lines) and changed files of every commit.
4. POSTs each day to the configured endpoint (default `http://localhost:8080/v1/chat/completions`) with a system prompt tuned for timesheet generation. If there are no commits in the window, it prints "No commits in the requested window." without calling the LLM.
5. Prints the model's reply for each day.

The LLM groups commits into tasks, extracts ticket numbers (also from branch names), writes business-value descriptions, and rounds task hours to 0.5h so they add up to the calculated day total. Guesses are marked with `(?)`. Line counts help it understand what a commit did, but are never used to estimate time. See [`main.go`](./main.go) for the full prompt.

## LLM server

Point any OpenAI-compatible server at port `8080` (or set `api_url` in the config or `WHATDIDIEVEN_API_URL` to override the endpoint). A reasonable starting setup:

For example: https://llama.app/docs/serve

```sh
# From a Hugging Face repo (downloaded and cached automatically)
llama serve -hf ggml-org/gemma-4-e4b-it-GGUF:Q4_0

# From a local GGUF file
llama serve -m my-model.gguf
```
The request sends the model from `WHATDIDIEVEN_MODEL` or the config's `model` (default: `local-model`) and `temperature: 0.2`. Most local servers ignore the model field and serve whatever you loaded.

## License

MIT
