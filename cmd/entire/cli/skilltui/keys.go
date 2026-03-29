package skilltui

import "github.com/charmbracelet/bubbles/key"

type globalKeys struct {
	TabNext key.Binding
	TabPrev key.Binding
	Tab1    key.Binding
	Tab2    key.Binding
	Tab3    key.Binding
	Help    key.Binding
	Quit    key.Binding
}

type pickerKeys struct {
	Up     key.Binding
	Down   key.Binding
	Enter  key.Binding
	Filter key.Binding
}

type dashboardKeys struct {
	Back    key.Binding
	Refresh key.Binding
}

type improveKeys struct {
	Generate key.Binding
	Apply    key.Binding
	Dismiss  key.Binding
}

var globalKeyMap = globalKeys{
	TabNext: key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next tab")),
	TabPrev: key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "prev tab")),
	Tab1:    key.NewBinding(key.WithKeys("1"), key.WithHelp("1", "stats")),
	Tab2:    key.NewBinding(key.WithKeys("2"), key.WithHelp("2", "friction")),
	Tab3:    key.NewBinding(key.WithKeys("3"), key.WithHelp("3", "improve")),
	Help:    key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
}

var pickerKeyMap = pickerKeys{
	Up:     key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("up/k", "up")),
	Down:   key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("down/j", "down")),
	Enter:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "select")),
	Filter: key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "filter")),
}

var dashboardKeyMap = dashboardKeys{
	Back:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
}

var improveKeyMap = improveKeys{
	Generate: key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "generate")),
	Apply:    key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "apply")),
	Dismiss:  key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "dismiss")),
}
