package tmux

import (
	"errors"
	"regexp"
	"strings"
)

// ErrQuestionPromptOpen reports that a nudge was refused because the target
// pane is showing an interactive question/selection UI (Claude Code
// permission prompt, AskUserQuestion dialog, or equivalent). Immediate
// send-keys delivery (text + Escape + Enter) would dismiss the dialog and
// lose the pending selection, so callers must queue the nudge instead and
// let it drain on a later turn boundary.
var ErrQuestionPromptOpen = errors.New("question prompt open: nudge deferred to preserve selection UI")

var (
	// questionSelectedOption matches Claude Code's selected-option cursor in
	// selection UIs: ❯ (U+276F, the same prompt character as
	// DefaultReadyPromptPrefix) followed by a number, e.g. "❯ 1. Yes".
	// The idle composer also renders ❯ but followed by free text, never
	// "N." — the selector requirement is what separates a selection UI
	// from an idle prompt or busy status line.
	questionSelectedOption = regexp.MustCompile(`❯\s*\d+\s*[.)]`)

	// questionNumberedOption matches a non-selected numbered option line,
	// e.g. "  2. Yes, allow all edits during this session".
	questionNumberedOption = regexp.MustCompile(`^\s*\d+\s*[.)]\s+\S`)
)

// Validation basis (Muse UI): Claude Code renders permission prompts
// ("Do you want to proceed?") and AskUserQuestion dialogs as numbered
// option lists with a ❯ cursor on the selected item:
//
//	Do you want to proceed?
//	❯ 1. Yes
//	  2. Yes, allow all edits during this session
//	  3. No
//
// Detection requires the ❯ N. selector PLUS corroboration so that a bare
// numbered list in normal transcript output (or a user typing "1. foo"
// into an idle composer) is not mistaken for a selection UI.

// containsQuestionUI reports whether pane content shows an open interactive
// question/selection UI. Pure function of captured text — no tmux I/O —
// so it is cheaply unit-testable. Operates on ANSI-stripped text so SGR
// styling around the selector cannot hide a dialog.
func containsQuestionUI(content string) bool {
	plain, _ := stripAnsiTrackDim(content)
	lines := strings.Split(string(plain), "\n")

	hasSelected := false
	optionLines := 0
	for _, line := range lines {
		if questionSelectedOption.MatchString(line) {
			hasSelected = true
			continue
		}
		if questionNumberedOption.MatchString(line) {
			optionLines++
		}
	}
	if !hasSelected {
		return false
	}
	// Selector plus at least one more option: a genuine selection list
	// (permission prompts always offer Yes/No variants; AskUserQuestion
	// requires 2-4 options), not a stray "❯ 1." in prose.
	if optionLines >= 1 {
		return true
	}
	lower := strings.ToLower(string(plain))
	// Single-option edge: corroborate with question phrasing ...
	if strings.Contains(lower, "do you want to") {
		return true
	}
	// ... or with co-occurring Enter/Esc action hints. This reuses the
	// pair convention of the Rewind detector, but the ❯ N. selector
	// above already rules Rewind out (it has no numbered options).
	return hasQuestionEnterHint(lower) && hasQuestionEscHint(lower)
}

func hasQuestionEnterHint(lower string) bool {
	for _, s := range []string{"enter to select", "enter to confirm", "enter to accept"} {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

func hasQuestionEscHint(lower string) bool {
	for _, s := range []string{"esc to cancel", "esc to reject", "esc to go back", "esc to exit"} {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// isInQuestionPrompt checks if a tmux target is displaying an interactive
// question/selection UI. Returns false on any error (defensive — a failed
// capture must not block nudge delivery; the submit-verification path
// remains the backstop for misdelivery).
func (t *Tmux) isInQuestionPrompt(target string) bool {
	content, err := t.CapturePane(target, 15)
	if err != nil {
		return false
	}
	return containsQuestionUI(content)
}
