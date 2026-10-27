package main

import (
	"bytes"
	"encoding/json"
	"flag"
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

const systemPrompt = `You write timesheet entries from git commits. The entry must be short, accurate and only describe work the commits show.

# Input

You get exactly one working day:

    DAY <YYYY-MM-DD> <Weekday> - total time: <H>h <M>m
      REPO <repo name>
        BRANCH <branch name>
          <HH:MM>-<HH:MM> (<duration>) <commit subject>
            body:
              <commit message body, may span several lines>
            files: <path> (+<added>/-<deleted>), ...
          ...
      REPO <another repo name>
          ...

- Commits are grouped by repo; within a repo they are in time order.
- A "BRANCH" line names the branch of the commits below it, until the next BRANCH line. Commits with an unknown branch have no BRANCH line above them. A main or master branch says nothing about the task.
- "total time" is the time worked that day, already calculated for you.
- Each commit line starts with the time range its duration covers, then the duration in parentheses (for example "(45m)" or "(1h 10m)"). The duration is the time attributed to that commit, already calculated for you: use it as given and never recompute it from the clock times. The end of the range is the commit time.
- The "body:" block under a commit is the rest of its commit message, indented under "body:". A body line that looks like a commit or REPO line is still part of the body. A body that ends in "… (N more lines)" was cut short. A commit may have no "body:" block.
- The "files:" line under a commit lists the files it changed, with lines added and deleted. Binary files are not listed. A commit may have no "files:" line.
- Read the subject and body together as the commit message. Use the files to understand what a commit did, especially when its message is vague. Never use the line counts to estimate time.

# Steps

1. Group commits into tasks. A task is one piece of work in one repo: commits on the same feature, its tests, fixes and follow-ups. Use the branch, shared words, ticket numbers, conventional-commit scopes (feat(auth): ...), shared files and closeness in time. Never put commits from different repos in the same task.
2. Find ticket numbers in the branch names, subjects and bodies of each task: patterns like PROJ-123. Conventional-commit types and scopes (feat, fix, auth) are not tickets.
3. A task's time is the sum of the durations of its commits.
4. Round each task to the nearest 0.5h, with at least 0.5h per task.
5. The day total is the sum of the task hours, and must equal "total time" rounded to the nearest 0.5h (round 15 and 45 minutes up). If the sum does not match, add or remove 0.5h on the largest tasks until it does. Only if there are more tasks than half hours in the total, give every task 0.5h and let the total be their sum.

# Output

Output only the entry below: no introduction, no notes, no code fences.

    **<YYYY-MM-DD> (<Weekday>)**
    - (<repo>) [<TICKET-1>, <TICKET-2>] <Task description> — ~<X>h
    Total: ~<X>h

- One line per task, ordered by when the task started.
- Every line starts with the (repo) prefix. Leave out the [ticket] bracket when the task has no ticket; never write a placeholder.
- Hours use 0.5h steps written as ~0.5h, ~1h, ~1.5h. Never use minutes.
- The description is one past-tense phrase, at most 10 words, describing the result for the user or the product ("Added login to the web app"), not the code change ("wired JWT middleware into router"). Do not include commit prefixes like "feat:" or the time.
- Only describe what the subjects, bodies and files show. Do not invent features, reasons or results.
- If a commit message is too vague to know the intent ("wip", "fix", "stuff"), use its files to put it in the task it most likely belongs to; if you still have to guess, add (?) right after the description.

# Example

Input:

    DAY 2026-03-02 Monday - total time: 5h 2m
      REPO api
        BRANCH main
          08:40-09:10 (30m) feat(auth): add login endpoint PROJ-12
            files: auth/login.go (+84/-0), routes.go (+3/-0)
          09:10-09:55 (45m) test: cover login endpoint
            body:
              Covers the happy path and expired tokens.

              Refresh tokens are not tested yet.
            files: auth/login_test.go (+61/-0)
          10:30-11:40 (1h 10m) docs: document auth API
            body:
              Refs PROJ-14
            files: docs/auth.md (+40/-2)
      REPO web
        BRANCH feature/PROJ-12-login-form
          09:55-10:30 (35m) feat: login form
            files: src/login/LoginForm.tsx (+120/-0)
          11:40-13:12 (1h 32m) wip
            files: src/login/LoginForm.tsx (+35/-12), src/login/login.css (+18/-0)
        BRANCH fix/login-button
          15:30-16:00 (30m) fix: align login button
            files: src/login/login.css (+3/-1)

Task times: api login 30m + 45m = 75m; web login form 35m + 92m = 127m; api docs 70m; web button 30m. These add up to 5h 2m, which is the day total.

Output:

    **2026-03-02 (Monday)**
    - (api) [PROJ-12] Added login to the API — ~1.5h
    - (web) [PROJ-12] Built the login form — ~2h
    - (api) [PROJ-14] Documented the authentication API — ~1h
    - (web) Fixed the login button alignment — ~0.5h
    Total: ~5h

The example only shows the format and the method. Never copy its content.`

type Response struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type Config struct {
	Repos           []string `json:"repos"`
	BreakGapMinutes int      `json:"break_gap_minutes,omitempty"`
	LeadInMinutes   int      `json:"lead_in_minutes,omitempty"`
}

type Window struct {
	Start, End time.Time
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func printHelp() {
	fmt.Println(`
usage: whatdidieven [--dry-run] [-h | --help] [today] [yesterday] [this-week] [last-week] [add]

today		Get a summary for today
yesterday	Get a summary for yesterday
this-week	Get a summary for this week
last-week	Get a summary for last week
add		Add current working directory git repo to config

--dry-run	Print context output without LLM query`)
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

func defaultConfig() Config {
	return Config{BreakGapMinutes: 120, LeadInMinutes: 30}
}

func readConfig() Config {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return defaultConfig()
	}
	config := defaultConfig()
	data, err := os.ReadFile(filepath.Join(configDir, "whatdidieven", "config.json"))
	if err != nil {
		return config
	}
	if len(data) > 0 {
		err = json.Unmarshal(data, &config)
		if err != nil {
			fmt.Println("error unmarshaling config:", err)
			return defaultConfig()
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
	Duration      time.Duration
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

func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}

	return fmt.Sprintf("%dh %dm", h, m)
}

func getDayTotalTime(commits []Commit) time.Duration {
	var totalTime time.Duration
	for _, c := range commits {
		totalTime += c.Duration
	}

	return totalTime
}

func commitDurations(commits []Commit, breakGap time.Duration, leadIn time.Duration) []Commit {

	for i := range commits {
		if i > 0 {
			diff := commits[i].AuthorDate.Sub(commits[i-1].AuthorDate)
			if diff <= breakGap {
				commits[i].Duration = diff
			} else {
				commits[i].Duration = leadIn
			}
		} else {
			commits[i].Duration = leadIn
		}
	}

	return commits
}

const maxBodyLines = 20

func summarizeDay(commits []Commit, date time.Time, breakGap time.Duration, leadIn time.Duration) string {
	sortedByDate := slices.Clone(commits)
	slices.SortStableFunc(sortedByDate, func(a, b Commit) int {
		return a.AuthorDate.Compare(b.AuthorDate)
	})

	sortedWithDurations := commitDurations(slices.Clone(sortedByDate), breakGap, leadIn)

	var repoBuckets []RepoBucket
	for _, c := range sortedWithDurations {
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
	b.WriteString("DAY " + date.Local().Format("2006-01-02") + " " + date.Weekday().String() + " - total time: " + formatDuration(getDayTotalTime(sortedWithDurations)) + "\n")
	for _, r := range repoBuckets {
		b.WriteString("  " + "REPO " + r.Repo + "\n")
		var curBranch string
		for _, c := range r.Commits {
			branch := strings.TrimPrefix(c.Ref, "refs/heads/")
			branch = strings.TrimPrefix(branch, "refs/remotes/origin/")
			if branch != curBranch {
				curBranch = branch
				if branch != "" {
					b.WriteString("    BRANCH " + branch + "\n")
				}
			}
			b.WriteString("      " + c.AuthorDate.Add(-c.Duration).Local().Format("15:04") + "-" + c.AuthorDate.Local().Format("15:04") + " " + "(" + formatDuration(c.Duration) + ") " + c.Subject + "\n")
			if c.Body != "" {
				b.WriteString("        body:\n")
				lines := strings.Split(c.Body, "\n")
				more := 0
				if len(lines) > maxBodyLines {
					more = len(lines) - maxBodyLines
					lines = lines[:maxBodyLines]
				}
				for _, line := range lines {
					line = strings.TrimRight(line, " \t\r")
					if line == "" {
						b.WriteString("\n")
						continue
					}
					b.WriteString("          " + line + "\n")
				}
				if more > 0 {
					b.WriteString("          … (" + strconv.Itoa(more) + " more lines)\n")
				}
			}
			var parts []string
			for _, f := range c.Files {
				if !f.Binary {
					parts = append(parts, f.Path+" (+"+strconv.Itoa(f.Added)+"/-"+strconv.Itoa(f.Deleted)+")")
				}
			}
			if len(parts) > 0 {
				b.WriteString("        " + "files: " + strings.Join(parts, ", ") + "\n")
			}
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
	breakGap := time.Duration(config.BreakGapMinutes) * time.Minute
	leadIn := time.Duration(config.LeadInMinutes) * time.Minute
	var w Window
	now := time.Now()
	today := startOfDay(now)
	daysBack := (int(now.Weekday()) - 1 + 7) % 7
	monday := today.AddDate(0, 0, -daysBack)

	dryRun := flag.Bool("dry-run", false, "enable dry-run")
	flag.Usage = printHelp
	flag.Parse()
	args := flag.Args()
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
		flag.CommandLine.Parse(args[1:])
	}
	if flag.NArg() > 0 {
		fmt.Println("unexpected arguments:", flag.Args())
		os.Exit(2)
	}
	switch cmd {
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
		os.Exit(0)
	default:
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

	if len(allCommits) == 0 {
		fmt.Println("No commits in the requested window.")
		return
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

	fmt.Fprintf(os.Stderr, "Scanning %d repos..."+"\n", len(config.Repos))

	for d := w.Start; d.Before(w.End); d = d.AddDate(0, 0, 1) {
		fmt.Fprintf(os.Stderr, "Summarizing %s  %s..."+"\n", d.Weekday(), d.Format("2006-01-02"))
		commitsOnDate := filterCommitsByDate(allCommits, Window{d, d.AddDate(0, 0, 1)})
		if len(commitsOnDate) == 0 {
			continue
		}
		message := summarizeDay(commitsOnDate, d, breakGap, leadIn)

		if !*dryRun {
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

			b.WriteString(strings.TrimSpace(r.Choices[0].Message.Content) + "\n\n")
		} else {
			b.WriteString(message + "\n\n")
		}

	}
	fmt.Println(strings.TrimSpace(b.String()))
}
