package editor

import "github.com/CharitonMedia/togavsu/internal/domain"

// Commit is the LawCraft commit this build compiles (PRD 8.20). Moving it is an
// ordinary reviewed change with its own tests; nothing moves it at run time.
const Commit = domain.LawCraftCommit

// Source is the LawCraft repository the commit is in.
const Source = "CharitonMedia/LawCraft"

// Info is GET /api/v1/editor: the pinned commit, read only.
type Info struct {
	Commit   string `json:"commit"`
	Source   string `json:"source"`
	ReadOnly bool   `json:"read_only"`
}

// PinnedInfo is the editor build's identity.
func PinnedInfo() Info { return Info{Commit: Commit, Source: Source, ReadOnly: true} }
