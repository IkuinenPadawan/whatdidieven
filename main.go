package main

import (
	"fmt"
	"os/exec"
	"time"
)

func main() {
	git := "git"
	log := "log"
	now := time.Now()
	since := "--since=" + now.Format("2006-01-21")

	cmd := exec.Command(git, log, since)
	stdout, err := cmd.Output()

	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	fmt.Printf(string(stdout))
}
