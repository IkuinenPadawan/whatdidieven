package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
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
4. Estimate time per task. Primary signal is LOC churn from --stat but assume some time for orientating to a new task. Secondary signals are commit count and message scope:
   - Trivial fix, typo, or single-file tweak (<20 LOC): ~1h
   - Small feature or focused change (~20–150 LOC): 2-4h
   - Larger feature, multi-file work, or several related commits (>150 LOC): 3–6h
5. Gaps between commits on the same day indicate work duration.
6. Cap each day at 8h. If raw estimates exceed 8h, scale them down proportionally.
7. The day total equals the sum of its task times.

# Output format

Days appear in chronological order (oldest first). Each day heading includes the weekday name in parentheses:

    **YYYY-MM-DD**
    - [TICKET-123] Task description — ~Xh
    - Task description — ~Xh
    Total: ~Xh

For example: **2026-04-29**

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

type Window struct {
	Start, End time.Time
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func printHelp() {
	fmt.Println(`
usage: whatdidieven [-h | --help] [today] [yesterday] [this-week] [add]

today		Get a summary for today
yesterday	Get a summary for yesterday
this-week	Get a summary for this week
last-week	Get a summary for last week
add		Add current working directory git repo to config`)
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

type FileStat struct {
	Added   int
	Deleted int
	Path    string
	Binary  bool
}

type Commit struct {
	Repo          string
	Hash          string
	AuthorDate    time.Time
	CommitterDate time.Time
	Email         string
	Subject       string
	Body          string
	Decoration    string
	Ref           string
	Files         []FileStat
}

type RepoBucket struct {
	Repo    string
	Commits []Commit
}

func parseFileStats(data []byte) ([]FileStat, error) {
	var filestats []FileStat

	fileStatLines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	for i, line := range fileStatLines {
		if len(line) == 0 {
			continue
		}
		cols := bytes.SplitN(line, []byte("\t"), 3)
		if len(cols) != 3 {
			return nil, fmt.Errorf("filestat line %d: expected 3 columns, got %d", i+1, len(cols))
		}
		var added, deleted int

		binary := string(cols[0]) == "-" && string(cols[1]) == "-"

		if !binary {
			var err error
			added, err = strconv.Atoi(string(cols[0]))
			if err != nil {
				return nil, fmt.Errorf("filestat line %d: parse added count %q: %w", i+1, cols[0], err)
			}
			deleted, err = strconv.Atoi(string(cols[1]))
			if err != nil {
				return nil, fmt.Errorf("filestat line %d: parse deleted count %q: %w", i+1, cols[1], err)

			}
		}
		path := string(cols[2])
		filestats = append(filestats, FileStat{Added: added, Deleted: deleted, Path: path, Binary: binary})
	}
	return filestats, nil
}

func parseCommits(data []byte, repo string) ([]Commit, error) {
	var commits []Commit
	parts := bytes.Split(data, []byte("\x1e"))

	for i, part := range parts {
		commit := Commit{}
		part = bytes.TrimSpace(part)
		if len(part) == 0 {
			continue
		}
		header, filestat, found := bytes.Cut(part, []byte("\x1d"))
		if !found {
			return nil, fmt.Errorf("record %d: missing header end", i)
		}
		filestats, err := parseFileStats(filestat)
		if err != nil {
			return nil, fmt.Errorf("record %d: parse filestats: %w", i, err)
		}
		commit.Files = filestats
		fields := bytes.Split(header, []byte("\x1f"))
		if len(fields) != 8 {
			return nil, fmt.Errorf("record %d: expected 8 header fields, got %d", i, len(fields))
		}
		commit.Hash = string(fields[0])
		authorT, err := time.Parse(time.RFC3339, string(fields[1]))
		if err != nil {
			return nil, fmt.Errorf("record %d: author date: %w", i, err)
		}
		commit.AuthorDate = authorT.Local()
		committerT, err := time.Parse(time.RFC3339, string(fields[2]))
		if err != nil {
			return nil, fmt.Errorf("record %d: committer date: %w", i, err)
		}
		commit.CommitterDate = committerT.Local()
		commit.Email = strings.TrimSpace(string(fields[3]))
		commit.Ref = string(fields[4])
		commit.Decoration = string(fields[5])
		commit.Subject = strings.TrimSpace(string(fields[6]))
		commit.Body = strings.TrimSpace(string(fields[7]))
		commit.Repo = strings.TrimSpace(filepath.Base(repo))

		commits = append(commits, commit)
	}
	return commits, nil
}

func buildGitCommand(w Window, repo string) (*exec.Cmd, error) {
	gitAuthorCmd := exec.Command("git", "config", "user.email")
	gitAuthorCmd.Dir = repo
	out, err := gitAuthorCmd.Output()
	if err != nil {
		return nil, err
	}
	gitAuthor := strings.TrimSpace(string(out))
	since := "--since=" + w.Start.AddDate(0, 0, -2).Format(time.RFC3339)
	until := "--until=" + w.End.AddDate(0, 0, 1).Format(time.RFC3339)

	args := []string{"log", "--branches", "--remotes", "--no-merges", "--author-date-order",
		"--author=" + gitAuthor, "--fixed-strings", "--regexp-ignore-case", since, until}
	// Records: \x1e starts a record, fields split on \x1f, \x1d ends the header, numstat follows.
	// Fields: hash, author date, committer date, author email, source ref, decorations, subject, body.
	args = append(args, "-w", "--numstat", "--source",
		"--pretty=format:%x1e%H%x1f%aI%x1f%cI%x1f%ae%x1f%S%x1f%D%x1f%s%x1f%b%x1d")

	cmd := exec.Command("git", args...)
	cmd.Dir = repo

	return cmd, nil
}

func filterCommitsByDate(commits []Commit, w Window) []Commit {
	var keep []Commit
	for _, c := range commits {
		if !c.AuthorDate.Before(w.Start) && c.AuthorDate.Before(w.End) {
			keep = append(keep, c)
		}
	}
	return keep
}

func getDayTotalTime(commits []Commit) string {
	if len(commits) == 0 {
		return "0h 0m"
	}

	earliest := commits[0].AuthorDate
	latest := commits[len(commits)-1].AuthorDate

	d := latest.Sub(earliest)
	total := int((d + 30*time.Minute).Round(time.Minute).Minutes())
	h := total / 60
	m := total % 60

	return fmt.Sprintf("%dh %dm", h, m)
}

func summarizeDay(commits []Commit, date time.Time) string {
	sortedByDate := slices.Clone(commits)
	slices.SortStableFunc(sortedByDate, func(a, b Commit) int {
		return a.AuthorDate.Compare(b.AuthorDate)
	})

	var repoBuckets []RepoBucket
	for _, c := range sortedByDate {
		found := -1
		for i := range repoBuckets {
			if repoBuckets[i].Repo == c.Repo {
				found = i
				break
			}
		}
		if found == -1 {
			repoBuckets = append(repoBuckets, RepoBucket{Repo: c.Repo})
			found = len(repoBuckets) - 1
		}
		repoBuckets[found].Commits = append(repoBuckets[found].Commits, c)

	}

	var b strings.Builder
	b.WriteString("DAY " + date.Local().Format("2006-01-02") + " " + date.Weekday().String() + " - total time: " + getDayTotalTime(sortedByDate) + "\n")
	for _, r := range repoBuckets {
		b.WriteString("  " + "REPO " + r.Repo + "\n")
		for _, c := range r.Commits {
			b.WriteString("      " + c.AuthorDate.Local().Format("15:04") + " " + c.Subject + "\n")
		}
	}

	return b.String()

}

func fetchCommits(repo string, w Window) ([]Commit, error) {
	cmd, err := buildGitCommand(w, repo)
	if err != nil {
		return nil, fmt.Errorf("%s: build git command: %w", repo, err)
	}
	stdout, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: git log: %w", repo, err)
	}
	commits, err := parseCommits(stdout, repo)
	if err != nil {
		return nil, fmt.Errorf("%s: parse commits: %w", repo, err)
	}
	return filterCommitsByDate(commits, w), nil
}

func main() {
	createConfigFile()
	config := readConfig()
	var w Window
	now := time.Now()
	today := startOfDay(now)
	daysBack := (int(now.Weekday()) - 1 + 7) % 7
	monday := today.AddDate(0, 0, -daysBack)

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "today":
			w = Window{today, today.AddDate(0, 0, 1)}
		case "yesterday":
			w = Window{today.AddDate(0, 0, -1), today}
		case "this-week":
			w = Window{monday, today.AddDate(0, 0, 1)}
		case "last-week":
			w = Window{monday.AddDate(0, 0, -7), monday}

		case "add":
			err := addRepoToConfig(config)
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			fmt.Println("Repo added to config")
			os.Exit(1)
		case "--help", "-h":
			printHelp()
			os.Exit(1)
		default:
			w = Window{today, today.AddDate(0, 0, 1)}
		}
	} else {
		w = Window{today, today.AddDate(0, 0, 1)}

	}

	var allCommits []Commit

	if len(config.Repos) == 0 {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		allCommits, err = fetchCommits(wd, w)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		//		for _, p := range commits {
		//			fmt.Printf("%+v\n", p)
		//		}
		//
		//		os.Exit(1)
		summarizeDay(allCommits, today)
		return
		//		message = append(message, string(commits))
	} else {
		for _, repo := range config.Repos {
			commits, err := fetchCommits(repo, w)
			if err != nil {
				fmt.Fprintln(os.Stderr, "warning:", err)
				continue
			}
			allCommits = append(allCommits, commits...)
		}
	}

	requestURL := os.Getenv("WHATDIDIEVEN_API_URL")
	if requestURL == "" {
		requestURL = "http://localhost:8080/v1/chat/completions"
	}
	model := os.Getenv("WHATDIDIEVEN_MODEL")
	if model == "" {
		model = "local-model"
	}

	var b strings.Builder

	for d := w.Start; d.Before(w.End); d = d.AddDate(0, 0, 1) {
		commitsOnDate := filterCommitsByDate(allCommits, Window{d, d.AddDate(0, 0, 1)})
		if len(commitsOnDate) == 0 {
			continue
		}
		message := summarizeDay(commitsOnDate, d)

		jsonStr := []byte(fmt.Sprintf(`{
    "model": %q,
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
  }`, model, systemPrompt, message))

		req, err := http.NewRequest("POST", requestURL, bytes.NewBuffer(jsonStr))
		if err != nil {
			fmt.Printf("error: %v\n", err)
			os.Exit(1)
		}
		req.Header.Set("Content-Type", "application/json")
		if apiKey := os.Getenv("WHATDIDIEVEN_API_KEY"); apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Printf("error: %v\n", err)
			os.Exit(1)
		}

		responseBody, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			fmt.Printf("error reading LLM response: %v\n", err)
			os.Exit(1)
		}

		if res.StatusCode != http.StatusOK {
			fmt.Printf("LLM response error (%s): %s\n", res.Status, strings.TrimSpace(string(responseBody)))
			os.Exit(1)
		}

		var r Response
		if err := json.Unmarshal(responseBody, &r); err != nil {
			fmt.Printf("error decoding response: %v\n", err)
			os.Exit(1)
		}

		if len(r.Choices) == 0 {
			fmt.Println("LLM response empty")
			os.Exit(1)
		}

		b.WriteString(r.Choices[0].Message.Content)

	}
	fmt.Println(b.String())
}
