package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

When multiple repositories are included, a line "Repo: <path>" appears before that repository's first ---- block and applies to every commit block that follows until the next "Repo:" line or the end of input. Treat it as the project identifier for all commits under it.

# Procedure

1. Group commits by calendar day (the Date field already shows the day).
2. Within each day, cluster related commits into logical tasks or themes (shared subsystem, sequential subjects, follow-up fixes). Never merge commits from different repos into the same task, even if they look related — a repo boundary always starts a new task.
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

If the input contains "Repo:" lines, prefix each task with the repo name in parentheses, before any ticket brackets: "- (repo-name) [TICKET-123] Task description — ~Xh". If the input has no "Repo:" lines, omit the prefix entirely.

End with a single line:

    **Grand total: ~Xh**

# Rules

- Use 0.5h granularity (e.g. ~0.5h, ~1h, ~1.5h, ~2h). Never use minutes.
- If commits are tagged with "Repo:", every task line must include the "(repo-name)" prefix, even when only one repo appears that day. Never combine tasks from two different repos onto a single line.
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

type Config struct {
	Repos []string `json:"repos"`
}

func getConfigFilepath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "whatdidieven", "config.json")

}

func createConfigFile() {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return
	}

	path := filepath.Join(configDir, "whatdidieven")
	_, err = os.Stat(path)

	if !os.IsNotExist(err) {
		return
	}

	err = os.MkdirAll(path, os.ModePerm)
	if err != nil {
		fmt.Printf("error in creating config file path: %v\n", err)
		return
	}

	_, err = os.Create(getConfigFilepath())
	if err != nil {
		fmt.Printf("error in creating config file: %v\n", err)
		return
	}
}

func readConfig() Config {
	configDir, err := os.UserConfigDir()
	var config Config
	data, err := os.ReadFile(filepath.Join(configDir, "whatdidieven", "config.json"))
	if err != nil {
		return Config{}
	}
	if len(data) > 0 {
		err = json.Unmarshal(data, &config)
		if err != nil {
			fmt.Println("error unmarshaling config:", err)
			return Config{}
		}
	}
	return config
}

func addRepoToConfig(config Config) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	repoAlreadyInConfig := false

	for _, repoStr := range config.Repos {
		if repoStr == wd {
			repoAlreadyInConfig = true
		}
	}

	if repoAlreadyInConfig {
		return fmt.Errorf("Repo has already been added to config")
	}

	if config.Repos == nil {
		fmt.Println("Config file does not exist, creating...")
	}

	config.Repos = append(config.Repos, wd)

	byteValue, err := json.Marshal(config)
	if err != nil {
		return err
	}

	err = os.WriteFile(getConfigFilepath(), byteValue, 0644)
	if err != nil {
		return err
	}

	return nil
}

func buildGitCommand(since string, until string, repo string) (*exec.Cmd, error) {
	var cmd *exec.Cmd
	gitAuthorCmd := exec.Command("git", "config", "user.email")
	gitAuthorCmd.Dir = repo
	out, err := gitAuthorCmd.Output()
	if err != nil {
		return nil, err
	}
	gitAuthor := strings.TrimSpace(string(out))
	if until != "" {
		cmd = exec.Command("git", "log", since, until, "--all", "--no-merges", "--decorate=full", "--stat",
			"--pretty=format:----%nDate: %ad%nHash: %h%nRefs: %D%nSubject: %s",
			"--date=format:%Y-%m-%d %H:%M",
			"--author="+gitAuthor)
	} else {
		cmd = exec.Command("git", "log", since, "--all", "--no-merges", "--decorate=full", "--stat",
			"--pretty=format:----%nDate: %ad%nHash: %h%nRefs: %D%nSubject: %s",
			"--date=format:%Y-%m-%d %H:%M",
			"--author="+gitAuthor)
	}
	cmd.Dir = repo

	return cmd, err
}

func main() {
	createConfigFile()
	config := readConfig()
	var since string
	var until string
	now := time.Now()

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "today":
			since = "--since=" + now.Format("2006-01-02") + " 00:00"
		case "yesterday":
			yesterday := now.AddDate(0, 0, -1)
			since = "--since=" + yesterday.Format("2006-01-02") + " 00:00"
			until = "--until=" + now.Format("2006-01-02") + " 00:00"
		case "this-week":
			daysBack := (int(now.Weekday()) - 1 + 7) % 7
			lastMonday := now.AddDate(0, 0, -daysBack)
			since = "--since=" + lastMonday.Format("2006-01-02") + " 00:00"
		case "last-week":
			daysBack := (int(now.Weekday()) - 1 + 7) % 7
			lastWeekMonday := now.AddDate(0, 0, -daysBack-7)
			since = "--since=" + lastWeekMonday.Format("2006-01-02") + " 00:00"
			until = "--until=" + now.AddDate(0, 0, -daysBack).Format("2006-01-02") + " 00:00"
		case "add":
			err := addRepoToConfig(config)
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			fmt.Println("Repo added to config")
			os.Exit(1)
		default:
			since = "--since=" + now.Format("2006-01-02") + " 00:00"
		}
	} else {
		since = "--since=" + now.Format("2006-01-02") + " 00:00"
	}

	var message []string

	if len(config.Repos) == 0 {
		cmd, err := buildGitCommand(since, until, "")
		if err != nil {
			fmt.Printf("error: %v\n", err)
			return
		}
		stdout, err := cmd.Output()
		if err != nil {
			fmt.Printf("error: %v\n", err)
			return
		}
		message = append(message, string(stdout))
	} else {
		for _, repo := range config.Repos {
			cmd, err := buildGitCommand(since, until, repo)
			if err != nil {
				fmt.Printf("error: %v\n", err)
				return
			}
			stdout, err := cmd.Output()
			if err != nil {
				fmt.Printf("error: %v\n", err)
				return
			}
			message = append(message, "Repo: "+repo)
			message = append(message, string(stdout))
		}
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
  }`, systemPrompt, strings.Join(message, "\n")))

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

	if res.StatusCode != 200 {
		fmt.Println("LLM response error")
		os.Exit(1)
	}

	if len(r.Choices) == 0 {
		fmt.Println("LLM response empty")
		os.Exit(1)
	}

	fmt.Println(r.Choices[0].Message.Content)
}
