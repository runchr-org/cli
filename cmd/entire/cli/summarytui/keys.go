package summarytui

import "github.com/charmbracelet/bubbles/key"

type keyMap struct {
	cursorUp          key.Binding
	cursorDown        key.Binding
	cycleTimeFilter   key.Binding
	cycleBranchFilter key.Binding
	nextPage          key.Binding
	prevPage          key.Binding
	generate          key.Binding
	quit              key.Binding
}

var keys = keyMap{
	cursorUp: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑/k", "up"),
	),
	cursorDown: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓/j", "down"),
	),
	cycleTimeFilter: key.NewBinding(
		key.WithKeys("1"),
		key.WithHelp("1", "time filter"),
	),
	cycleBranchFilter: key.NewBinding(
		key.WithKeys("2"),
		key.WithHelp("2", "branch filter"),
	),
	nextPage: key.NewBinding(
		key.WithKeys("right", "l"),
		key.WithHelp("→/l", "next page"),
	),
	prevPage: key.NewBinding(
		key.WithKeys("left", "h"),
		key.WithHelp("←/h", "prev page"),
	),
	generate: key.NewBinding(
		key.WithKeys("g"),
		key.WithHelp("g", "generate"),
	),
	quit: key.NewBinding(
		key.WithKeys("q"),
		key.WithHelp("q", "quit"),
	),
}
