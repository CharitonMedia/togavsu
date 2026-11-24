package domain

import "regexp"

// ModuleIDPattern is the manifest module-id rule: 2 to 42 ASCII slug characters.
const ModuleIDPattern = `^[a-z][a-z0-9-]{0,40}[a-z0-9]$`

var moduleID = regexp.MustCompile(ModuleIDPattern)

// ValidModuleID checks the syntax of a module id without requiring installation.
func ValidModuleID(id string) bool { return moduleID.MatchString(id) }
