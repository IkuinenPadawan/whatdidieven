package main

import (
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

func TestParseCommits(t *testing.T) {
	commits, err := parseCommits([]byte(sampleLog))
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

}

func TestParseCommitsEmpty(t *testing.T) {
	commits, err := parseCommits(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(commits) != 0 {
		t.Fatalf("got %d commits, want 0", len(commits))
	}
}
