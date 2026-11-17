package coretest

import (
	"context"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
)

// SeedMarkdownRendition is the CORE-36 Doc Tender rendition stand-in: it stores a
// markdown copy on a file version through the real files service without running
// converters or Doc Tender jobs, so a module suite can exercise listFileRenditions
// and downloadFileRendition against Postgres.
func (c *Core) SeedMarkdownRendition(who *User, fileID, versionID, body string) files.RenditionView {
	c.T.Helper()
	if c.app.Files == nil {
		c.T.Fatal("coretest: files service missing")
	}
	p := c.principal(who)
	view, err := c.app.Files.AddRendition(context.Background(), p, domain.ID(fileID), domain.ID(versionID), "markdown",
		strings.NewReader(body), int64(len(body)))
	if err != nil {
		c.T.Fatal(err)
	}
	return view
}

// OpenMarkdownRendition opens a markdown rendition as who.
func (c *Core) OpenMarkdownRendition(who *User, fileID, renditionID string) *files.Download {
	c.T.Helper()
	p := c.principal(who)
	dl, err := c.app.Files.OpenRendition(context.Background(), p, domain.ID(fileID), domain.ID(renditionID), false)
	if err != nil {
		c.T.Fatal(err)
	}
	return dl
}
