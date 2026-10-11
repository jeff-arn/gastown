package tmux

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestContainsQuestionUI(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name: "claude permission prompt",
			content: strings.Join([]string{
				"⏺ Bash(npm test)",
				"  ⎿  Running…",
				"",
				"Do you want to proceed?",
				"❯ 1. Yes",
				"  2. Yes, allow all edits during this session",
				"  3. No",
			}, "\n"),
			want: true,
		},
		{
			name: "ask user question dialog",
			content: strings.Join([]string{
				"Which approach should I take?",
				"❯ 1. Refactor the handler",
				"  2. Add a new flag",
				"  3. Leave as is",
				"Enter to select · Esc to cancel",
			}, "\n"),
			want: true,
		},
		{
			name: "paren-style options with enter esc hints",
			content: strings.Join([]string{
				"Allow this action?",
				"❯ 1) Allow once",
				"  2) Allow always",
				"Enter to confirm · Esc to reject",
			}, "\n"),
			want: true,
		},
		{
			name:    "styled selector still detected",
			content: "Do you want to proceed?\n\x1b[36m❯ 1. Yes\x1b[0m\n  2. No\n",
			want:    true,
		},
		{
			name:    "idle composer is not a question",
			content: "transcript output\n❯ draft message text\n",
			want:    false,
		},
		{
			name:    "busy agent without selector is not a question",
			content: "agent output\n· Thinking... (esc to interrupt)\n❯\n",
			want:    false,
		},
		{
			name: "numbered list in transcript without selector is not a question",
			content: strings.Join([]string{
				"Here is the plan:",
				"1. First step",
				"2. Second step",
				"❯",
			}, "\n"),
			want: false,
		},
		{
			name:    "question prose without options is not a question",
			content: "Do you want to continue with the refactor?\n❯ sure, go ahead\n",
			want:    false,
		},
		{
			name:    "rewind ui without numbered options is not a question",
			content: "Rewind to checkpoint\nEnter to accept · Esc to cancel\n",
			want:    false,
		},
		{
			name:    "empty pane is not a question",
			content: "",
			want:    false,
		},
		{
			name:    "shell prompt is not a question",
			content: "user@host ~/repo $ ",
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := containsQuestionUI(tt.content); got != tt.want {
				t.Errorf("containsQuestionUI() = %v, want %v\ncontent:\n%s", got, tt.want, tt.content)
			}
		})
	}
}

func TestErrQuestionPromptOpenWrapping(t *testing.T) {
	t.Parallel()
	wrapped := fmt.Errorf("nudge to session %q: %w", "gt-test", ErrQuestionPromptOpen)
	if !errors.Is(wrapped, ErrQuestionPromptOpen) {
		t.Fatal("errors.Is did not recognize wrapped ErrQuestionPromptOpen")
	}
}

// renderQuestionFixture prints a fake Claude Code permission prompt into a
// shell pane so integration tests can exercise the prompt-preserving fork
// against realistic Muse UI markers.
func renderQuestionFixture(t *testing.T, tm *Tmux, session string) {
	t.Helper()
	for _, line := range []string{
		"echo 'Do you want to proceed?'",
		`echo '❯ 1. Yes'`,
		"echo '  2. Yes, allow all edits during this session'",
		"echo '  3. No'",
	} {
		if err := tm.SendKeys(session, line); err != nil {
			t.Fatalf("SendKeys(%q): %v", line, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	content, err := tm.CapturePane(session, 15)
	if err != nil {
		t.Fatalf("CapturePane: %v", err)
	}
	if !containsQuestionUI(content) {
		t.Fatalf("fixture did not render a detectable question UI, pane:\n%s", content)
	}
}

func TestIsInQuestionPrompt_DetectsFixture(t *testing.T) {
	tm := newTestTmux(t)
	sessionName := "gt-test-qprompt-detect"
	_ = tm.KillSession(sessionName)
	if err := tm.NewSession(sessionName, ""); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer func() { _ = tm.KillSession(sessionName) }()

	if tm.isInQuestionPrompt(sessionName) {
		t.Fatal("fresh shell pane reported as question UI")
	}
	renderQuestionFixture(t, tm, sessionName)
	if !tm.isInQuestionPrompt(sessionName) {
		t.Fatal("question UI fixture not detected")
	}
}

// TestNudgeSessionWithOpts_QuestionPromptSkipsSubmit is the
// submit-verification test for the prompt-preserving fork: when a question
// UI is open, immediate delivery must send zero keys — the sentinel error
// is returned and the nudge text never reaches the pane (so the dialog is
// neither dismissed nor answered).
func TestNudgeSessionWithOpts_QuestionPromptSkipsSubmit(t *testing.T) {
	tm := newTestTmux(t)
	sessionName := "gt-test-qprompt-skip"
	_ = tm.KillSession(sessionName)
	if err := tm.NewSession(sessionName, ""); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer func() { _ = tm.KillSession(sessionName) }()

	renderQuestionFixture(t, tm, sessionName)

	marker := fmt.Sprintf("qt-nudge-probe-%d", time.Now().UnixNano())
	err := tm.NudgeSessionWithOpts(sessionName, marker, NudgeOpts{})
	if !errors.Is(err, ErrQuestionPromptOpen) {
		t.Fatalf("NudgeSessionWithOpts() = %v, want ErrQuestionPromptOpen", err)
	}

	content, capErr := tm.CapturePane(sessionName, 25)
	if capErr != nil {
		t.Fatalf("CapturePane: %v", capErr)
	}
	if strings.Contains(content, marker) {
		t.Errorf("nudge text reached the pane despite open question UI — dialog would be disturbed\npane:\n%s", content)
	}
	if !containsQuestionUI(content) {
		t.Errorf("question UI fixture no longer detectable after refused nudge\npane:\n%s", content)
	}
}

// TestNudgePane_QuestionPromptSkipsSubmit covers the pane-targeted delivery
// path with the same zero-keys-sent guarantee.
func TestNudgePane_QuestionPromptSkipsSubmit(t *testing.T) {
	tm := newTestTmux(t)
	sessionName := "gt-test-qprompt-pane"
	_ = tm.KillSession(sessionName)
	if err := tm.NewSession(sessionName, ""); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer func() { _ = tm.KillSession(sessionName) }()

	renderQuestionFixture(t, tm, sessionName)

	marker := fmt.Sprintf("qt-pane-probe-%d", time.Now().UnixNano())
	err := tm.NudgePane(sessionName+":0.0", marker)
	if !errors.Is(err, ErrQuestionPromptOpen) {
		t.Fatalf("NudgePane() = %v, want ErrQuestionPromptOpen", err)
	}

	content, capErr := tm.CapturePane(sessionName, 25)
	if capErr != nil {
		t.Fatalf("CapturePane: %v", capErr)
	}
	if strings.Contains(content, marker) {
		t.Errorf("nudge text reached the pane despite open question UI\npane:\n%s", content)
	}
}
