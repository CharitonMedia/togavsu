package coretest

import (
	"context"
	"io"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Rendition is a public stand-in for a file rendition row a module suite can assert on
// without importing internal/files (CORE-36 Doc Tender markdown helper).
type Rendition struct {
	ID            string
	FileVersionID string
	Kind          string
	Number        int
	SizeBytes     int64
	SHA256        string
	Name          string
}

// Download is an open markdown rendition body a module suite can read and Close.
type Download struct {
	Name      string
	MediaType string
	Size      int64
	SHA256    string
	Body      io.ReadCloser
}

// Close releases the backend reader.
func (d *Download) Close() error {
	if d == nil || d.Body == nil {
		return nil
	}
	return d.Body.Close()
}

// SeedMarkdownRendition is the CORE-36 Doc Tender rendition stand-in: it stores a
// markdown copy on a file version through the real files service without running
// converters or Doc Tender jobs, so a module suite can exercise listFileRenditions
// and downloadFileRendition against Postgres.
func (c *Core) SeedMarkdownRendition(who *User, fileID, versionID, body string) Rendition {
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
	return Rendition{
		ID: string(view.ID), FileVersionID: string(view.FileVersionID), Kind: view.Kind,
		Number: view.Number, SizeBytes: view.SizeBytes, SHA256: view.SHA256, Name: view.Name,
	}
}

// OpenMarkdownRendition opens a markdown rendition as who.
func (c *Core) OpenMarkdownRendition(who *User, fileID, renditionID string) *Download {
	c.T.Helper()
	p := c.principal(who)
	dl, err := c.app.Files.OpenRendition(context.Background(), p, domain.ID(fileID), domain.ID(renditionID), false)
	if err != nil {
		c.T.Fatal(err)
	}
	return &Download{Name: dl.Name, MediaType: dl.MediaType, Size: dl.Size, SHA256: dl.SHA256, Body: dl.Body}
}
