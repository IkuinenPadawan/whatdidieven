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

	requestURL := "http://localhost:8080/v1/chat/completions"
	var jsonStr = []byte(`{ "messages": [
      {"role": "user", "content": "Who are you?"}
    ]}`)

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
