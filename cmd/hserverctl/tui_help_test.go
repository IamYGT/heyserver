package main

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestTUIHelpDialogFitsSmallTerminalsAndShowsFirstLastContent(t *testing.T) {
	t.Parallel()
	for _, terminal := range []struct {
		name   string
		width  int
		height int
	}{
		{name: "small", width: 48, height: 18},
		{name: "standard", width: 80, height: 24},
	} {
		t.Run(terminal.name, func(t *testing.T) {
			model := tuiModel{
				width: terminal.width, height: terminal.height,
				dialog: tuiDialog{Mode: tuiDialogHelp, Title: "Keyboard help"},
			}
			top := model.renderDialog(terminal.width, terminal.height)
			if got := lipgloss.Width(top); got > terminal.width {
				t.Fatalf("top width = %d, terminal width = %d", got, terminal.width)
			}
			if got := lipgloss.Height(top); got > terminal.height {
				t.Fatalf("top height = %d, terminal height = %d", got, terminal.height)
			}
			if !strings.Contains(top, "switch sections") {
				t.Fatalf("first help content is not visible:\n%s", top)
			}

			model.dialog.HelpScroll = tuiHelpScrollLimit(terminal.height)
			bottom := model.renderDialog(terminal.width, terminal.height)
			if !strings.Contains(bottom, "q · Ctrl+C") || !strings.Contains(bottom, "Mutations never run") {
				t.Fatalf("last help content is not visible:\n%s", bottom)
			}
			if got := lipgloss.Height(bottom); got > terminal.height {
				t.Fatalf("bottom height = %d, terminal height = %d", got, terminal.height)
			}
		})
	}
}

func TestTUIHelpDialogKeyboardNavigation(t *testing.T) {
	t.Parallel()
	model := tuiModel{
		height: 18,
		dialog: tuiDialog{Mode: tuiDialogHelp, Title: "Keyboard help"},
	}
	limit := tuiHelpScrollLimit(model.height)
	if limit <= 0 {
		t.Fatalf("help content does not require scrolling: limit=%d", limit)
	}

	updated, command := model.updateDialogKey("j")
	model = updated.(tuiModel)
	if command != nil || model.dialog.HelpScroll != 1 {
		t.Fatalf("j scroll = %d, command=%v", model.dialog.HelpScroll, command != nil)
	}
	updated, _ = model.updateDialogKey("pgdown")
	model = updated.(tuiModel)
	page := tuiHelpPageSize(model.height)
	if model.dialog.HelpScroll != minInt(limit, 1+page) {
		t.Fatalf("PgDn scroll = %d, want %d", model.dialog.HelpScroll, minInt(limit, 1+page))
	}
	updated, _ = model.updateDialogKey("g")
	model = updated.(tuiModel)
	if model.dialog.HelpScroll != 0 {
		t.Fatalf("g scroll = %d, want 0", model.dialog.HelpScroll)
	}
	updated, _ = model.updateDialogKey("G")
	model = updated.(tuiModel)
	if model.dialog.HelpScroll != limit {
		t.Fatalf("G scroll = %d, want %d", model.dialog.HelpScroll, limit)
	}
	updated, _ = model.updateDialogKey("pgup")
	model = updated.(tuiModel)
	if model.dialog.HelpScroll != maxInt(0, limit-page) {
		t.Fatalf("PgUp scroll = %d, want %d", model.dialog.HelpScroll, maxInt(0, limit-page))
	}
	updated, _ = model.updateDialogKey("k")
	model = updated.(tuiModel)
	if model.dialog.HelpScroll != maxInt(0, limit-page-1) {
		t.Fatalf("k scroll = %d, want %d", model.dialog.HelpScroll, maxInt(0, limit-page-1))
	}

	updated, command = model.updateDialogKey("esc")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogNone {
		t.Fatalf("Esc did not close help: dialog=%v command=%v", model.dialog.Mode, command != nil)
	}
}
