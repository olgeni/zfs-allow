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

func TestTabbedLongLineKeepsTheLastLines(t *testing.T) {
	m := New("tank/home", "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	// a tab-indented line wider than the screen, followed by the last lines
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, "line")
	}
	lines = append(lines, "\tKEY: "+strings.Repeat("x", 300), "}", "THE END")
	m.showView("cfg", strings.Join(lines, "\n"))
	if rows := strings.Count(m.View(), "\n") + 1; rows != 10 {
		t.Errorf("the text screen uses %d rows of 10", rows)
	}
	m.vp.GotoBottom()
	v := m.View()
	if !strings.Contains(v, "THE END") || !strings.Contains(v, "}") || !strings.Contains(v, "        KEY: xxx") {
		t.Errorf("the end of the text is not shown:\n%s", v)
	}
	if expandTabs("a\tb\n\tc") != "a       b\n        c" {
		t.Errorf("expandTabs: %q", expandTabs("a\tb\n\tc"))
	}
}
