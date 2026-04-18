package opencode

import _ "embed"

//go:embed entire_plugin.ts
var pluginTemplate string

// entireCmdPlaceholder is replaced with the actual command during installation.
const entireCmdPlaceholder = "__ENTIRE_CMD__"

// entireMetaJSONPlaceholder is replaced at install time with the marshalled
// HookMeta JSON (e.g., `{"cli_version":"0.5.3"}`). The resulting `// entireMeta: {...}`
// comment line is read by drift.go to detect stale installs.
const entireMetaJSONPlaceholder = "__ENTIRE_META_JSON__"
