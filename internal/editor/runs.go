package editor

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
)

// RunRequest is POST /api/v1/files/{id}/editor-runs.
type RunRequest struct {
	Commands []Command `json:"commands"`
	SaveAs   string    `json:"save_as"`
	FileName string    `json:"file_name,omitempty"`
}

// RunResult is the file and version an editor run saved.
type RunResult struct {
	File    files.FileView    `json:"file"`
	Version files.VersionView `json:"version"`
}

// ConversionResult is the new file and its conversion_of link to the source.
type ConversionResult struct {
	File files.FileView `json:"file"`
	Link files.LinkView `json:"link"`
}

func commandIDs(cmds []Command) []string {
	ids := make([]string, 0, len(cmds))
	for _, c := range cmds {
		ids = append(ids, c.ID)
	}
	return ids
}

func runNote(cmds []Command) string {
	return "Editor run: " + strings.Join(commandIDs(cmds), ", ")
}

// auditRun writes one audit row for a run or conversion on fileID, whatever its outcome
// (PRD 8.20: every run goes to the audit log).
func (s *Service) auditRun(ctx context.Context, p domain.Principal, fileID domain.ID, field string, summary map[string]any) error {
	b, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	return s.record(ctx, p, "file", fileID, field, "", string(b))
}

// Run runs LawCraft commands headless on the file's current version and saves the result
// as its next version or as a new file beside it (PRD 8.20 Run). A command that reaches
// the file system or the network refuses the whole run before anything starts.
func (s *Service) Run(ctx context.Context, p domain.Principal, fileID domain.ID, req RunRequest) (RunResult, error) {
	if req.SaveAs != "version" && req.SaveAs != "new_file" {
		return RunResult{}, domain.Invalid("save_as", "save_as is version or new_file")
	}
	cur, err := s.loadCurrent(ctx, p, fileID)
	if err != nil {
		return RunResult{}, err
	}
	curExt := cur.ext()
	if !Editable(curExt) {
		return RunResult{}, domain.Invalid("file", "editor runs work on documents the editor opens (DOCX, DOC, ODT, RTF, HTML, Markdown, text)")
	}
	summary := map[string]any{"commands": commandIDs(req.Commands), "save_as": req.SaveAs, "from_version": cur.Version.ID}
	// fail audits a run that ends in an error, under "refused" (nothing ran) or "failed"
	// (with what was saved, if anything was, before the failure).
	fail := func(key string, err error) (RunResult, error) {
		summary[key] = err.Error()
		if aerr := s.auditRun(ctx, p, fileID, "editor_run", summary); aerr != nil {
			return RunResult{}, aerr
		}
		return RunResult{}, err
	}
	known, err := s.knownCommands(ctx)
	if err != nil {
		return fail("failed", err)
	}
	if err := checkCommands(known, req.Commands); err != nil {
		return fail("refused", err)
	}
	if req.SaveAs == "version" {
		// A new version under an open editor, the caller's own included, would be lost
		// on that editor's next save. This early check spares the run; writeUnderLock
		// checks again at the write, with the source version.
		if err := s.anyHolds(ctx, p, fileID); err != nil {
			return fail("refused", err)
		}
	}
	name := req.FileName
	if req.SaveAs == "new_file" {
		if name == "" {
			name = baseName(cur.File.Name) + " (edited).docx"
		} else if !sameFormat(extOf(name), "docx") {
			name = baseName(name) + ".docx"
		}
	}
	profiles, err := s.profileFiles(ctx)
	if err != nil {
		return fail("failed", err)
	}
	out, err := s.cli.Run(ctx, RunInput{Document: cur.Bytes, Ext: curExt, Commands: req.Commands, Profiles: profiles,
		Out: "docx", Author: s.userName(ctx, p.UserID)})
	if err == nil {
		err = CheckDOCX(out)
	}
	if err != nil {
		return fail("failed", err)
	}
	var res RunResult
	if req.SaveAs == "version" {
		opt := files.SaveOptions{How: "editor_run", MediaType: docxMedia, Note: runNote(req.Commands)}
		if !sameFormat(cur.File.Extension, "docx") {
			opt.Extension = "docx"
		}
		err = s.writeUnderLock(ctx, p, fileID, cur.Version.ID, func() error {
			r, n := reader(out)
			v, err := s.files.SaveVersion(ctx, p, fileID, r, n, opt)
			res.Version = v
			return err
		})
		if err == nil {
			summary["file"], summary["version"] = fileID, res.Version.ID
			res.File, err = s.files.Get(ctx, p, fileID)
		}
	} else {
		res.File, err = s.createBeside(ctx, p, cur.File, name, out, "editor_run", runNote(req.Commands))
		if err == nil {
			summary["file"] = res.File.ID
			res.Version, err = s.files.Version(ctx, p, res.File.ID, *res.File.CurrentVersionID)
			summary["version"] = res.Version.ID
		}
	}
	if err != nil {
		// A failure after the save still names what was saved.
		return fail("failed", err)
	}
	if err := s.auditRun(ctx, p, fileID, "editor_run", summary); err != nil {
		return RunResult{}, err
	}
	return res, nil
}

// createBeside makes a new file with src's matter, contact, folder and access flags, so
// a run or conversion of an attorney-only file is attorney-only too.
func (s *Service) createBeside(ctx context.Context, p domain.Principal, src files.FileView, name string, data []byte, how, note string) (files.FileView, error) {
	nf := files.NewFile{
		Name: name, AttorneyOnly: src.AttorneyOnly, SharedWithClient: src.SharedWithClient,
		MediaType: mediaTypes[extOf(name)], Source: "editor", How: how, Note: note,
	}
	if src.MatterID != nil {
		nf.MatterID = *src.MatterID
		unique, err := s.files.UniqueName(ctx, nf.MatterID, name)
		if err != nil {
			return files.FileView{}, err
		}
		nf.Name = unique
	}
	if src.ContactID != nil {
		nf.ContactID = *src.ContactID
	}
	if src.FolderID != nil {
		nf.FolderID = *src.FolderID
	}
	r, n := reader(data)
	return s.files.Create(ctx, p, nf, r, n)
}

// Convert makes a new file in another format, linked to its source with conversion_of
// (PRD 8.20 Convert). Only documents the editor opens are converted, never into the
// format they are already in.
func (s *Service) Convert(ctx context.Context, p domain.Principal, fileID domain.ID, format string) (ConversionResult, error) {
	target, ok := conversionTargets[format]
	if !ok {
		return ConversionResult{}, domain.Invalid("format", "format is pdf, docx, odt, rtf, html, markdown or text")
	}
	cur, err := s.loadCurrent(ctx, p, fileID)
	if err != nil {
		return ConversionResult{}, err
	}
	ext := cur.ext()
	if !Editable(ext) {
		return ConversionResult{}, domain.Invalid("file", "only documents the editor opens (DOCX, DOC, ODT, RTF, HTML, Markdown, text) can be converted")
	}
	if sameFormat(ext, target) {
		return ConversionResult{}, domain.Invalid("format", "the file is already in that format")
	}
	summary := map[string]any{"format": format, "from_version": cur.Version.ID}
	out, err := s.cli.Convert(ctx, cur.Bytes, ext, target)
	if err == nil {
		err = checkOutput(out, target)
	}
	if err != nil {
		summary["failed"] = err.Error()
		if aerr := s.auditRun(ctx, p, fileID, "conversion", summary); aerr != nil {
			return ConversionResult{}, aerr
		}
		return ConversionResult{}, err
	}
	f, err := s.createBeside(ctx, p, cur.File, baseName(cur.File.Name)+"."+target, out, "conversion", "Converted from "+cur.File.Name)
	if err != nil {
		summary["failed"] = err.Error()
		if aerr := s.auditRun(ctx, p, fileID, "conversion", summary); aerr != nil {
			return ConversionResult{}, aerr
		}
		return ConversionResult{}, err
	}
	summary["file"] = f.ID
	link, err := s.files.Link(ctx, p, f.ID, fileID, "conversion_of")
	if err != nil {
		// The new file exists but is not linked: the row names it so it can be found.
		summary["failed"] = err.Error()
	}
	if aerr := s.auditRun(ctx, p, fileID, "conversion", summary); aerr != nil {
		return ConversionResult{}, aerr
	}
	if err != nil {
		return ConversionResult{}, err
	}
	return ConversionResult{File: f, Link: link}, nil
}

// checkOutput is a sanity check of what wordcraft-cli wrote.
func checkOutput(b []byte, ext string) error {
	switch ext {
	case "docx":
		return CheckDOCX(b)
	case "pdf":
		if !bytes.HasPrefix(b, []byte("%PDF-")) || !bytes.HasSuffix(bytes.TrimSpace(b), []byte("%%EOF")) {
			return domain.Invalid("format", "the editor wrote a PDF without a header or trailer")
		}
	case "odt":
		if sniff(b) != "odt" {
			return domain.Invalid("format", "the editor wrote no OpenDocument text package")
		}
	case "rtf":
		if sniff(b) != "rtf" {
			return domain.Invalid("format", "the editor wrote no RTF document")
		}
	}
	if len(b) == 0 {
		return domain.Invalid("format", "the editor wrote an empty file")
	}
	return nil
}

// maxSaveBytes bounds one save from the browser editor.
const maxSaveBytes = 200 << 20

// Save stores what the browser editor handed back (wordcraft:save) as the file's next
// version (PRD 8.20 Editing). The caller must hold the live edit lock. DOCX is kept as
// DOCX; any other document format is converted to DOCX and the file's extension becomes
// .docx (files CONTRACT.md). Exports that are copies (PDF, PNG) are not versions.
func (s *Service) Save(ctx context.Context, p domain.Principal, fileID domain.ID, name string, data []byte) (files.VersionView, error) {
	if err := requireEditor(p); err != nil {
		return files.VersionView{}, err
	}
	f, err := s.files.Get(ctx, p, fileID)
	if err != nil {
		return files.VersionView{}, err
	}
	if err := s.holdsLock(ctx, p, fileID); err != nil {
		return files.VersionView{}, err
	}
	if len(data) == 0 {
		return files.VersionView{}, domain.Invalid("body", "the editor sent an empty document")
	}
	if len(data) > maxSaveBytes {
		return files.VersionView{}, domain.Invalid("body", "the document is too large to save")
	}
	format := sniff(data)
	ext := extOf(name)
	switch {
	case format == "docx":
	case format == "odt" || format == "rtf":
		ext = format
	case format == "" && (ext == "html" || ext == "htm" || ext == "md" || ext == "markdown" || ext == "txt"):
	default:
		return files.VersionView{}, domain.Invalid("body", "only document saves become versions; PDF and picture exports download instead")
	}
	if format != "docx" {
		if data, err = s.cli.Convert(ctx, data, ext, "docx"); err != nil {
			return files.VersionView{}, err
		}
	}
	if err := CheckDOCX(data); err != nil {
		return files.VersionView{}, domain.Invalid("body", err.Error())
	}
	opt := files.SaveOptions{How: "editor", MediaType: docxMedia, Note: "Saved in the editor"}
	if !sameFormat(f.Extension, "docx") {
		opt.Extension = "docx"
	}
	// The lock is checked again with its row held until the version is in, so it cannot
	// lapse to someone else between the check and the write.
	var v files.VersionView
	err = s.writeUnderLock(ctx, p, fileID, "", func() error {
		r, n := reader(data)
		var err error
		v, err = s.files.SaveVersion(ctx, p, fileID, r, n, opt)
		return err
	})
	if err != nil {
		return files.VersionView{}, err
	}
	return v, nil
}

// Document is the current version for the editor page to send in (wordcraft:open). Only
// the holder of the live lock gets it.
func (s *Service) Document(ctx context.Context, p domain.Principal, fileID domain.ID) (string, []byte, error) {
	// Role, wall, attorney-only and write access come first: a lock held by someone
	// else names them, and only a person who may edit the file may learn that.
	cur, err := s.loadCurrent(ctx, p, fileID)
	if err != nil {
		return "", nil, err
	}
	if err := s.holdsLock(ctx, p, fileID); err != nil {
		return "", nil, err
	}
	return cur.name(), cur.Bytes, nil
}

// Preview adds a PDF preview rendition for a version of a file in a format the editor
// reads (PRD 8.19 Preview), when the Document editor tab has PDF previews on.
func (s *Service) Preview(ctx context.Context, p domain.Principal, fileID, versionID domain.ID) (bool, error) {
	on, err := s.settings.Get(ctx, "editor.pdf_previews")
	if err != nil || on != "true" {
		return false, err
	}
	f, err := s.files.Get(ctx, p, fileID)
	if err != nil {
		return false, err
	}
	if _, err := s.files.Version(ctx, p, fileID, versionID); err != nil {
		return false, err
	}
	if !Editable(f.Extension) {
		return false, nil
	}
	d, err := s.files.OpenForProcessing(ctx, p, fileID, versionID)
	if err != nil {
		return false, err
	}
	var buf bytes.Buffer
	_, err = buf.ReadFrom(d.Body)
	_ = d.Close()
	if err != nil {
		return false, err
	}
	ext := formatOf(f.Extension, buf.Bytes())
	if !Editable(ext) {
		return false, nil
	}
	pdf, err := s.cli.Convert(ctx, buf.Bytes(), ext, "pdf")
	if err == nil {
		err = checkOutput(pdf, "pdf")
	}
	if err != nil {
		return false, err
	}
	r, n := reader(pdf)
	if _, err := s.files.AddRendition(ctx, p, fileID, versionID, "pdf_preview", r, n); err != nil {
		return false, err
	}
	return true, nil
}

// PreviewOnVersion is a files.Config.OnVersion subscriber: it makes the PDF preview of
// each new version as the system principal. Run it off the request path.
func (s *Service) PreviewOnVersion(ctx context.Context, ev files.VersionCreated) error {
	_, err := s.Preview(ctx, domain.Principal{Source: domain.SourceSystem, UserID: domain.SystemUserID}, ev.FileID, ev.VersionID)
	return err
}
