package summarytui

import "github.com/charmbracelet/bubbles/key"

type keyMap struct {
	cycleFilter key.Binding
	nextPage    key.Binding
	prevPage    key.Binding
}

var keys = keyMap{
	cycleFilter: key.NewBinding(
		key.WithKeys("f"),
		key.WithHelp("f", "filter"),
	),
	nextPage: key.NewBinding(
		key.WithKeys("right", "l"),
		key.WithHelp("→/l", "next page"),
	),
	prevPage: key.NewBinding(
		key.WithKeys("left", "h"),
		key.WithHelp("←/h", "prev page"),
	),
}
