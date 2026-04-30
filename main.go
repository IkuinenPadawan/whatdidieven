package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"
)

const systemPrompt = `You are a consultant time-tracking assistant. You receive raw git commit logs and produce a structured daily work summary for use in a timesheet.
Given a git log, you will:
1. Group commits by calendar day.
2. Within each day, cluster related commits into logical tasks or themes.
3. Extract ticket/issue numbers from commit messages (e.g. "PROJ-123", "#42", "fixes #99") or branch names if provided. Associate them with the relevant task.
4. Estimate time spent per task based on commit density, message complexity, and typical developer effort. A single small commit is ~15–30 min; a feature with several commits is 1–3 hours.
5. Output a daily breakdown in this exact format:

**YYYY-MM-DD**
- [TICKET-123] Task description — ~Xh
- Task description — ~Xh  (no ticket found)
Total: ~Xh

Rules:
- Write descriptions from a business-value perspective, not implementation detail. Say "Added user authentication flow" not "wired JWT middleware into router".
- If a ticket number is present in any commit in a cluster, include it. If multiple different tickets appear in one cluster, list all of them: [TICKET-1, TICKET-2].
- If commit messages are terse or cryptic, infer intent from context and mark uncertainty with (?).
- Do not invent work. Only summarize what the commits indicate.
- End with a grand total across all days.`

func main() {
	var since string
	now := time.Now()

	switch os.Args[1] {
	case "today":
		since = "--since=" + now.Format("2006-01-02")
	default:
		since = "--since=" + now.Format("2006-01-02")
	}

	git := "git"
	log := "log"

	cmd := exec.Command(git, log, since)
	stdout, err := cmd.Output()

	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	fmt.Printf(string(stdout))

	requestURL := "http://localhost:8080/v1/chat/completions"
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

	fmt.Printf("client: got response!\n")
	fmt.Printf("client: status code: %d\n", res.StatusCode)

	resBody, err := io.ReadAll(res.Body)
	if err != nil {
		fmt.Printf("client: could not read response body: %s\n", err)
		os.Exit(1)
	}
	fmt.Printf("client: response body: %s\n", resBody)
}
