package main

import (
	"fmt"
	"os"
	"os/exec"
	"time"
)

func main() {
	var since string
	now := time.Now()

	switch os.Args[1] {
	case "today":
		since = "--since=" + now.Format("2006-01-21")
	default:
		since = "--since=" + now.Format("2006-01-21")
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
}
