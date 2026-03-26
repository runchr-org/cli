package memorylooptui

import "github.com/charmbracelet/bubbles/key"

type globalKeys struct {
	TabNext key.Binding
	TabPrev key.Binding
	Tab1    key.Binding
	Tab2    key.Binding
	Tab3    key.Binding
	Tab4    key.Binding
	Help    key.Binding
	Quit    key.Binding
}

type memoriesKeys struct {
	Up         key.Binding
	Down       key.Binding
	Enter      key.Binding
	Activate   key.Binding
	Promote    key.Binding
	Suppress   key.Binding
	Unsuppress key.Binding
	Archive    key.Binding
	Prune      key.Binding
	Filter     key.Binding
	Search     key.Binding
	New        key.Binding
	Escape     key.Binding
}

type injectionKeys struct {
	Up     key.Binding
	Down   key.Binding
	Focus  key.Binding
	Enter  key.Binding
	Escape key.Binding
}

type historyKeys struct {
	Up      key.Binding
	Down    key.Binding
	Refresh key.Binding
}

type settingsKeys struct {
	Mode    key.Binding
	Policy  key.Binding
	MaxUp   key.Binding
	MaxDown key.Binding
}

var globalKeyMap = globalKeys{
	TabNext: key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next tab")),
	TabPrev: key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "prev tab")),
	Tab1:    key.NewBinding(key.WithKeys("1"), key.WithHelp("1", "memories")),
	Tab2:    key.NewBinding(key.WithKeys("2"), key.WithHelp("2", "injection")),
	Tab3:    key.NewBinding(key.WithKeys("3"), key.WithHelp("3", "history")),
	Tab4:    key.NewBinding(key.WithKeys("4"), key.WithHelp("4", "settings")),
	Help:    key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
}

var memoriesKeyMap = memoriesKeys{
	Up:         key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("up/k", "up")),
	Down:       key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("down/j", "down")),
	Enter:      key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "toggle detail")),
	Activate:   key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "activate")),
	Promote:    key.NewBinding(key.WithKeys("P"), key.WithHelp("P", "promote")),
	Suppress:   key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "suppress")),
	Unsuppress: key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "unsuppress")),
	Archive:    key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "archive")),
	Prune:      key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "prune")),
	Filter:     key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "cycle filter")),
	Search:     key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
	New:        key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new memory")),
	Escape:     key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
}

var injectionKeyMap = injectionKeys{
	Up:     key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("up/k", "up")),
	Down:   key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("down/j", "down")),
	Focus:  key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "focus input")),
	Enter:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "test prompt")),
	Escape: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "unfocus")),
}

var historyKeyMap = historyKeys{
	Up:      key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("up/k", "up")),
	Down:    key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("down/j", "down")),
	Refresh: key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "refresh")),
}

var settingsKeyMap = settingsKeys{
	Mode:    key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "cycle mode")),
	Policy:  key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "cycle policy")),
	MaxUp:   key.NewBinding(key.WithKeys("+", "="), key.WithHelp("+", "increase max")),
	MaxDown: key.NewBinding(key.WithKeys("-"), key.WithHelp("-", "decrease max")),
}
