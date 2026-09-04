package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestViewScrollsSideways(t *testing.T) {
	m := New("tank/home", "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	// the marker sits at column 100: off screen until the text scrolls right
	m.showView("wide", "short\n"+strings.Repeat(" ", 100)+"MARK\nshort")
	if strings.Contains(m.View(), "MARK") {
		t.Fatalf("the marker should start off screen:\n%s", m.View())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if v := m.View(); !strings.Contains(v, "MARK") || !strings.Contains(v, "←/→") {
		t.Errorf("after right:\n%s", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if strings.Contains(m.View(), "MARK") {
		t.Errorf("after left:\n%s", m.View())
	}
	// a new text starts at the left edge again
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m.showView("other", strings.Repeat(" ", 100)+"MARK")
	if strings.Contains(m.View(), "MARK") {
		t.Errorf("new text:\n%s", m.View())
	}
}
