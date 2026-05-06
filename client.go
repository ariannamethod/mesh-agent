package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// cmdStatus — `mesh-agent status [PEER]`
func cmdStatus(args []string) {
	target := ""
	if len(args) > 0 {
		target = args[0]
	}
	addr := resolvePeer(target)
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get("http://" + addr + "/presence")
	if err != nil {
		fmt.Fprintf(os.Stderr, "GET %s: %v\n", addr, err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	io.Copy(os.Stdout, resp.Body)
	fmt.Println()
}

// cmdExec — `mesh-agent exec PEER SLOT [ARGS...]`
//
// POSTs an invoke and then streams the SSE log to stdout. Exits with the
// remote job's exit_code (best-effort: 0 on done, 1 on failure).
func cmdExec(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: mesh-agent exec PEER SLOT [ARGS...]")
		os.Exit(2)
	}
	addr := resolvePeer(args[0])
	slotID := args[1]
	jobArgs := args[2:]

	body, _ := json.Marshal(map[string]any{"args": jobArgs})
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Post("http://"+addr+"/slot/"+slotID+"/invoke",
		"application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "invoke: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		io.Copy(os.Stderr, resp.Body)
		fmt.Fprintln(os.Stderr)
		os.Exit(1)
	}
	var jr struct {
		JobID string `json:"job_id"`
		State string `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jr); err != nil {
		fmt.Fprintf(os.Stderr, "decode invoke: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "[mesh] job %s on %s slot %s — streaming...\n",
		jr.JobID, addr, slotID)

	// Stream SSE
	streamClient := &http.Client{Timeout: 0}
	sresp, err := streamClient.Get(fmt.Sprintf("http://%s/job/%s/stream", addr, jr.JobID))
	if err != nil {
		fmt.Fprintf(os.Stderr, "stream: %v\n", err)
		os.Exit(1)
	}
	defer sresp.Body.Close()
	br := bufio.NewReader(sresp.Body)
	finalState := ""
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			line = strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(line, "event: ") {
				event := strings.TrimPrefix(line, "event: ")
				// next line should be data:
				dataLine, _ := br.ReadString('\n')
				dataLine = strings.TrimRight(dataLine, "\r\n")
				dataLine = strings.TrimPrefix(dataLine, "data: ")
				switch event {
				case "log":
					fmt.Println(dataLine)
				case "state":
					finalState = dataLine
					fmt.Fprintf(os.Stderr, "[mesh] state: %s\n", dataLine)
				}
			}
		}
		if err != nil {
			break
		}
	}
	if finalState == string(JobFailed) || finalState == string(JobKilled) {
		os.Exit(1)
	}
}

// cmdSend — `mesh-agent send PEER TEXT...`
func cmdSend(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: mesh-agent send PEER TEXT...")
		os.Exit(2)
	}
	addr := resolvePeer(args[0])
	text := strings.Join(args[1:], " ")
	from, _ := os.Hostname()
	body, _ := json.Marshal(map[string]any{
		"from": from,
		"to":   args[0],
		"body": text,
	})
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Post("http://"+addr+"/msg",
		"application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "POST: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	io.Copy(os.Stdout, resp.Body)
	fmt.Println()
}
