package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		log.Fatal("usage: gitsrv <repo-root> <listen>")
	}
	root := os.Args[1]
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if err := serveGit(w, r, root); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
		}
	})
	log.Fatal(http.ListenAndServe(os.Args[2], mux))
}

func serveGit(w http.ResponseWriter, r *http.Request, root string) error {
	cmd := exec.Command("git", "http-backend")
	cmd.Env = append(os.Environ(),
		"GIT_PROJECT_ROOT="+root,
		"GIT_HTTP_EXPORT_ALL=1",
		"REQUEST_METHOD="+r.Method,
		"PATH_INFO="+r.URL.Path,
		"QUERY_STRING="+r.URL.RawQuery,
		"CONTENT_TYPE="+r.Header.Get("Content-Type"),
		"CONTENT_LENGTH="+strconv.FormatInt(r.ContentLength, 10),
	)
	cmd.Stdin = r.Body
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("git http-backend: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return err
	}
	head, body, ok := bytes.Cut(out, []byte("\r\n\r\n"))
	if !ok {
		return fmt.Errorf("git http-backend returned no header")
	}
	status := http.StatusOK
	for _, line := range strings.Split(string(head), "\r\n") {
		name, value, found := strings.Cut(line, ": ")
		if !found || name == "" {
			continue
		}
		if strings.EqualFold(name, "Status") {
			fmt.Sscanf(value, "%d", &status)
			continue
		}
		w.Header().Add(name, value)
	}
	w.WriteHeader(status)
	_, err = io.Copy(w, bytes.NewReader(body))
	return err
}
