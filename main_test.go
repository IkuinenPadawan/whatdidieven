package main

import (
	"reflect"
	"testing"
	"time"
)

// Same layout as `git log` with the format in buildGitCommand:
// \x1e starts a record, \x1f separates fields, \x1d ends the header,
// numstat lines follow, a blank line precedes the next record.
const sampleLog = "\x1e508d9a2cf44b168f005e606e45bac1845c74bbf2\x1f2026-09-29T19:01:03+03:00\x1f2026-09-29T20:59:42+03:00\x1fsome.email@somemail.com\x1fmaster\x1forigin/devel, devel\x1fdocs: update readme\x1f\x1d\n" +
	"22\t8\tREADME.md\n" +
	"\n" +
	"\x1efc75c9e253a7ebf35f568b07472db97530eb338c\x1f2026-09-29T18:48:04+03:00\x1f2026-09-29T19:00:33+03:00\x1fsome.email@somemail.com\x1fdevel\x1f\x1ffeat: add simple help printout\x1fFirst line of body\nSecond line of body\n\x1d\n" +
	"17\t2\tmain.go\n" +
	"-\t-\tlogo.png\n"

func mockCommits() []Commit {
	day := time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)

	return []Commit{
		{
			// afternoon, listed first on purpose to test sorting
			Repo:          "whatdidieven",
			Hash:          "c3d4e5f6a7b8",
			AuthorDate:    day.Add(15*time.Hour + 30*time.Minute),
			CommitterDate: day.Add(15*time.Hour + 30*time.Minute),
			Email:         "dev@example.com",
			Subject:       "fix: wrong repo name with empty config",
			Body:          "Fall back to the directory name when config.Repos is empty.",
			Decoration:    "HEAD -> betterer",
			Ref:           "refs/heads/betterer",
			Files: []FileStat{
				{Added: 4, Deleted: 1, Path: "main.go"},
			},
		},
		{
			// morning
			Repo:          "whatdidieven",
			Hash:          "a1b2c3d4e5f6",
			AuthorDate:    day.Add(9*time.Hour + 5*time.Minute),
			CommitterDate: day.Add(9*time.Hour + 5*time.Minute),
			Email:         "dev@example.com",
			Subject:       "feat: add git repo identifier to commits",
			Body:          "",
			Decoration:    "HEAD -> betterer",
			Ref:           "refs/heads/betterer",
			Files: []FileStat{
				{Added: 25, Deleted: 3, Path: "main.go"},
				{Added: 12, Deleted: 0, Path: "main_test.go"},
			},
		},
		{
			// midday, different repo, binary file
			Repo:          "dotfiles",
			Hash:          "b2c3d4e5f6a7",
			AuthorDate:    day.Add(12*time.Hour + 45*time.Minute),
			CommitterDate: day.Add(13 * time.Hour),
			Email:         "dev@example.com",
			Subject:       "chore: update wallpaper and fish config",
			Body:          "Swap the wallpaper.\n\nAlso tweak the prompt.",
			Files: []FileStat{
				{Added: 0, Deleted: 0, Path: "wall.png", Binary: true},
				{Added: 7, Deleted: 2, Path: "config.fish"},
			},
		},
		{
			// same timestamp as the morning commit, tests stable sort
			Repo:          "dotfiles",
			Hash:          "d4e5f6a7b8c9",
			AuthorDate:    day.Add(9*time.Hour + 5*time.Minute),
			CommitterDate: day.Add(9*time.Hour + 5*time.Minute),
			Email:         "dev@example.com",
			Subject:       "docs: update readme",
			Files: []FileStat{
				{Added: 12, Deleted: 2, Path: "readme.md"},
			},
		},
	}
}

func TestSummarizeDay(t *testing.T) {
	want := `DAY 2026-10-15 Thursday
  REPO whatdidieven
      09:05 feat: add git repo identifier to commits
      15:30 fix: wrong repo name with empty config
  REPO dotfiles
      09:05 docs: update readme
      12:45 chore: update wallpaper and fish config
`
	got := summarizeDay(mockCommits(), time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local))

	if got != want {
		t.Errorf("summarizeDay mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestFilterCommitsByDate(t *testing.T) {
	at := func(h, m int, day int) time.Time {
		return time.Date(2026, 10, day, h, m, 0, 0, time.UTC)
	}
	commits := []Commit{
		{Subject: "before window", AuthorDate: at(23, 59, 4)},
		{Subject: "exactly start", AuthorDate: at(0, 0, 5)},
		{Subject: "inside", AuthorDate: at(12, 30, 5)},
		{Subject: "just before end", AuthorDate: at(23, 59, 5)},
		{Subject: "exactly end", AuthorDate: at(0, 0, 6)},
	}

	got := filterCommitsByDate(commits, Window{
		Start: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC),
	})

	want := []string{"exactly start", "inside", "just before end"}
	if len(got) != len(want) {
		t.Fatalf("got %d commits, want %d", len(got), len(want))
	}
	for i, c := range got {
		if c.Subject != want[i] {
			t.Errorf("commit %d: got %q, want %q", i, c.Subject, want[i])
		}
	}
}

func TestParseFileStats(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []FileStat
		wantErr bool
	}{
		{"empty", "", nil, false},
		{"text file", "17\t2\tmain.go\n", []FileStat{{Added: 17, Deleted: 2, Path: "main.go"}}, false},
		{"binary file", "-\t-\tlogo.png\n", []FileStat{{Path: "logo.png", Binary: true}}, false},
		{"path with spaces", "1\t1\tmy file.go\n", []FileStat{{Added: 1, Deleted: 1, Path: "my file.go"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFileStats([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseCommits(t *testing.T) {
	commits, err := parseCommits([]byte(sampleLog), "/some/path/myrepo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("got %d commits, want 2", len(commits))
	}

	c := commits[1]
	if c.Hash != "fc75c9e253a7ebf35f568b07472db97530eb338c" {
		t.Errorf("Hash = %q", c.Hash)
	}
	if c.Repo != "myrepo" {
		t.Errorf("Repo = %q", c.Repo)
	}
	if c.Subject != "feat: add simple help printout" {
		t.Errorf("Subject = %q", c.Subject)
	}
	if c.Body != "First line of body\nSecond line of body" {
		t.Errorf("Body = %q", c.Body)
	}
	if c.Email != "some.email@somemail.com" {
		t.Errorf("Email = %q", c.Email)
	}
	wantAuthorDate := time.Date(2026, 9, 29, 18, 48, 4, 0, time.FixedZone("", 3*60*60))
	if !c.AuthorDate.Equal(wantAuthorDate) {
		t.Errorf("AuthorDate = %v, want %v", c.AuthorDate, wantAuthorDate)
	}

	wantCommitterDate := time.Date(2026, 9, 29, 19, 0, 33, 0, time.FixedZone("", 3*60*60))
	if !c.CommitterDate.Equal(wantCommitterDate) {
		t.Errorf("CommitterDate = %v, want %v", c.CommitterDate, wantCommitterDate)
	}
	if c.Ref != "devel" {
		t.Errorf("Ref = %q", c.Ref)
	}

	if commits[0].Decoration != "origin/devel, devel" {
		t.Errorf("commits[0].Decoration = %q", commits[0].Decoration)
	}
	if c.Decoration != "" {
		t.Errorf("Decoration = %q", c.Decoration)
	}
	if len(commits[0].Files) != 1 || commits[0].Files[0].Path != "README.md" {
		t.Errorf("commits[0].Files = %+v", commits[0].Files)
	}
	if len(c.Files) != 2 || !c.Files[1].Binary {
		t.Errorf("Files = %+v", c.Files)
	}

}

func TestParseCommitsEmpty(t *testing.T) {
	commits, err := parseCommits(nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(commits) != 0 {
		t.Fatalf("got %d commits, want 0", len(commits))
	}
}
