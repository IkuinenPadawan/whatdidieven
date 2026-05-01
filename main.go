package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"
)

const systemPrompt = `You are a time-tracking assistant. You receive a git log and produce a daily work summary suitable for a timesheet.

# Input format

Commits are separated by a line of four dashes (----) and structured as:

    ----
    Date: YYYY-MM-DD HH:MM
    Hash: <short-sha>
    Refs: <branch/tag pointers, often empty>
    Subject: <commit message>
     <file> | <n> <+/->
     <N files changed, X insertions(+), Y deletions(-)>

The lines after "Subject:" are git --stat churn (LOC changed per file plus a totals line). "Refs:" is usually empty for historical commits — it only appears when a branch or tag points at that commit, so do not rely on it for ticket extraction.

# Procedure

1. Group commits by calendar day (the Date field already shows the day).
2. Within each day, cluster related commits into logical tasks or themes (shared subsystem, sequential subjects, follow-up fixes).
3. Extract ticket/issue numbers from subjects (e.g. PROJ-123, #42, fixes #99). Associate them with the cluster they belong to.
4. Estimate time per task. Primary signal is LOC churn from --stat; secondary signals are commit count and message scope:
   - Trivial fix, typo, or single-file tweak (<20 LOC): ~0.5h
   - Small feature or focused change (~20–150 LOC): 1–2h
   - Larger feature, multi-file work, or several related commits (>150 LOC): 2–4h
5. Cap each day at 8h. If raw estimates exceed 8h, scale them down proportionally.
6. The day total equals the sum of its task times.

# Output format

Days appear in chronological order (oldest first). Each day heading includes the weekday name in parentheses:

    **YYYY-MM-DD (Weekday)**
    - [TICKET-123] Task description — ~Xh
    - Task description — ~Xh
    Total: ~Xh

For example: **2026-04-29 (Wednesday)**

End with a single line:

    **Grand total: ~Xh**

# Rules

- Use 0.5h granularity (e.g. ~0.5h, ~1h, ~1.5h, ~2h). Never use minutes.
- Write descriptions from a business-value perspective, not implementation detail. Say "Added user authentication flow" not "wired JWT middleware into router".
- If multiple tickets appear in one cluster, list all: [TICKET-1, TICKET-2].
- If a cluster has no ticket, omit the bracket entirely. Do not write "(no ticket found)" or any placeholder.
- If a commit is terse or cryptic and you have to infer intent, append (?) immediately after the description: "- Refactored payment retry logic (?) — ~1h".
- Do not invent work. Only summarize what the commits indicate.
- If the input contains zero commits, output exactly: No commits in the requested window.`

type Response struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func main() {
	var since string
	now := time.Now()

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "today":
			since = "--since=" + now.Format("2006-01-02")
		case "week":
			daysBack := (int(now.Weekday()) - 1 + 7) % 7
			lastMonday := now.AddDate(0, 0, -daysBack)
			since = "--since=" + lastMonday.Format("2006-01-02")
		default:
			since = "--since=" + now.Format("2006-01-02")
		}
	} else {
		since = "--since=" + now.Format("2006-01-02")
	}

	cmd := exec.Command("git", "log", since, "--no-merges", "--decorate=short", "--stat",
		"--pretty=format:----%nDate: %ad%nHash: %h%nRefs: %D%nSubject: %s",
		"--date=format:%Y-%m-%d %H:%M")
	stdout, err := cmd.Output()

	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}

	requestURL := os.Getenv("WHATDIDIEVEN_API_URL")
	if requestURL == "" {
		requestURL = "http://localhost:8080/v1/chat/completions"
	}
	jsonStr := []byte(fmt.Sprintf(`{
    "model": "local-model",
    "messages": [
      {
        "role": "system",
        "content": %q 
      },
      {
        "role": "user",
        "content": %q
      }
    ],
    "temperature": 0.2
  }`, systemPrompt, string(stdout)))

	req, err := http.NewRequest("POST", requestURL, bytes.NewBuffer(jsonStr))
	if err != nil {
		fmt.Printf("error: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("error: %v\n", err)
		os.Exit(1)
	}

	defer res.Body.Close()

	var r Response
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		fmt.Printf("error decoding response: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(r.Choices[0].Message.Content)
}
