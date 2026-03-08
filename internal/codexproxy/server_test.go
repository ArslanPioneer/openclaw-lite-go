package codexproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

type fakeExecutor struct {
	calls []execCall
	reply []byte
	err   error
}

type execCall struct {
	workdir string
	args    []string
}

func (f *fakeExecutor) Run(_ context.Context, workdir string, args []string) ([]byte, error) {
	call := execCall{
		workdir: workdir,
		args:    append([]string(nil), args...),
	}
	f.calls = append(f.calls, call)
	if f.err != nil {
		return nil, f.err
	}
	return append([]byte(nil), f.reply...), nil
}

func TestServerHandleChatFirstTurnRunsCodexExec(t *testing.T) {
	t.Parallel()

	workdir := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state")
	exec := &fakeExecutor{
		reply: []byte(`{"reply":"first reply"}`),
	}
	server := NewServer(Config{
		WorkDir:  workdir,
		StateDir: stateDir,
		Executor: exec,
	})

	body := bytes.NewBufferString(`{"chat_id":42,"message":"inspect the repo"}`)
	req := httptest.NewRequest(http.MethodPost, "/chat", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(exec.calls) != 1 {
		t.Fatalf("executor calls = %d, want 1", len(exec.calls))
	}
	if exec.calls[0].workdir != workdir {
		t.Fatalf("executor workdir = %q, want %q", exec.calls[0].workdir, workdir)
	}
	wantArgs := []string{"exec", "--skip-git-repo-check", "--full-auto", "--json", "inspect the repo"}
	if got := exec.calls[0].args; strings.Join(got, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("executor args = %#v, want %#v", got, wantArgs)
	}

	var resp struct {
		Reply string `json:"reply"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Reply != "first reply" {
		t.Fatalf("reply = %q, want %q", resp.Reply, "first reply")
	}
}

func TestServerHandleChatFollowUpIncludesHistory(t *testing.T) {
	t.Parallel()

	workdir := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "state")
	exec := &fakeExecutor{
		reply: []byte(`{"reply":"second reply"}`),
	}
	server := NewServer(Config{
		WorkDir:  workdir,
		StateDir: stateDir,
		Executor: exec,
	})

	firstReq := httptest.NewRequest(http.MethodPost, "/chat", bytes.NewBufferString(`{"chat_id":9,"message":"first task"}`))
	firstReq.Header.Set("Content-Type", "application/json")
	firstRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("first status = %d, want %d", firstRec.Code, http.StatusOK)
	}

	exec.reply = []byte(`{"reply":"follow up reply"}`)
	secondReq := httptest.NewRequest(http.MethodPost, "/chat", bytes.NewBufferString(`{"chat_id":9,"message":"what changed?"}`))
	secondReq.Header.Set("Content-Type", "application/json")
	secondRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(secondRec, secondReq)

	if secondRec.Code != http.StatusOK {
		t.Fatalf("second status = %d, want %d, body=%s", secondRec.Code, http.StatusOK, secondRec.Body.String())
	}
	if len(exec.calls) != 2 {
		t.Fatalf("executor calls = %d, want 2", len(exec.calls))
	}
	secondPrompt := exec.calls[1].args[len(exec.calls[1].args)-1]
	for _, fragment := range []string{"Conversation so far:", "User: first task", "Assistant: second reply", "New user message:\nwhat changed?"} {
		if !strings.Contains(secondPrompt, fragment) {
			t.Fatalf("second prompt missing %q:\n%s", fragment, secondPrompt)
		}
	}
}

func TestServerHandleChatReturnsBadGatewayOnExecutorFailure(t *testing.T) {
	t.Parallel()

	server := NewServer(Config{
		WorkDir:  t.TempDir(),
		StateDir: filepath.Join(t.TempDir(), "state"),
		Executor: &fakeExecutor{err: errors.New("codex failed")},
	})

	req := httptest.NewRequest(http.MethodPost, "/chat", bytes.NewBufferString(`{"chat_id":1,"message":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadGateway, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "codex failed") {
		t.Fatalf("body = %q, want executor error", rec.Body.String())
	}
}

func TestServerHandleChatRejectsInvalidToken(t *testing.T) {
	t.Parallel()

	server := NewServer(Config{
		WorkDir:   t.TempDir(),
		StateDir:  filepath.Join(t.TempDir(), "state"),
		AuthToken: "secret",
		Executor:  &fakeExecutor{reply: []byte(`{"reply":"ok"}`)},
	})

	req := httptest.NewRequest(http.MethodPost, "/chat", bytes.NewBufferString(`{"chat_id":1,"message":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestParseReplyPrefersAgentMessageFromCodexJSONStream(t *testing.T) {
	t.Parallel()

	stream := strings.Join([]string{
		`{"type":"thread.started","thread_id":"abc"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"OK"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":2}}`,
	}, "\n")

	reply := parseReply([]byte(stream))
	if reply != "OK" {
		t.Fatalf("reply = %q, want %q", reply, "OK")
	}
}

func TestBuildExecArgsIncludesDangerousBypassWhenEnabled(t *testing.T) {
	t.Parallel()

	args := buildExecArgs("gpt-5-codex", "check host status", true)
	want := []string{
		"exec",
		"--skip-git-repo-check",
		"--dangerously-bypass-approvals-and-sandbox",
		"--json",
		"-m",
		"gpt-5-codex",
		"check host status",
	}
	if got := strings.Join(args, "\x00"); got != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}
