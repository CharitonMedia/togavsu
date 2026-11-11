package doctender

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

const sheetMIME = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

// xlsxWith builds a one-sheet workbook named Budget around the given worksheet XML.
func xlsxWith(t *testing.T, worksheet string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	add("xl/workbook.xml", `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Budget" sheetId="1" r:id="rId1"/></sheets></workbook>`)
	add("xl/_rels/workbook.xml.rels", `<Relationships><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`)
	add("xl/worksheets/sheet1.xml", worksheet)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func inline(ref, text string) string {
	r := ""
	if ref != "" {
		r = ` r="` + ref + `"`
	}
	return `<c` + r + ` t="inlineStr"><is><t>` + text + `</t></is></c>`
}

func sheetUnit(t *testing.T, e *env, name, mime string, data []byte, published bool) (Unit, string) {
	t.Helper()
	f := e.upload(e.staff, e.open, name, mime, data)
	e.drain()
	if ex := e.openExceptions(f.ID); len(ex) != 0 && ex[0].Kind == "damaged" {
		t.Fatalf("%s was flagged damaged: %+v", name, ex)
	}
	units := e.unitsFor(e.jobFor(f.ID).ID)
	if len(units) == 0 {
		t.Fatalf("%s has no units", name)
	}
	if !published {
		return units[0], ""
	}
	_, md := e.markdown(e.attorney, f.ID)
	return units[0], md
}

// Opus checkpoint P2: an XLSX is checked against the sheet's own <dimension>, and rows
// the sheet skips keep their place.
func TestSparseXLSXRowsKeepTheirPlace(t *testing.T) {
	e := newEnv(t)
	ws := `<worksheet><dimension ref="A1:B7"/><sheetData>` +
		`<row r="1">` + inline("A1", "Item") + inline("B1", "Amount") + `</row>` +
		`<row r="3">` + inline("A3", "Rent") + `<c r="B3"><v>1250</v></c></row>` +
		`<row r="7">` + inline("A7", "Fees") + `<c r="B7"><v>90</v></c></row>` +
		`</sheetData></worksheet>`
	u, md := sheetUnit(t, e, "sparse.xlsx", sheetMIME, xlsxWith(t, ws), true)
	if u.CheckResult != "rows 7/7 columns 2/2" || u.Status != unitPassed {
		t.Fatalf("sparse check %q status %s", u.CheckResult, u.Status)
	}
	if !strings.Contains(md, "| Fees | 90 |") || strings.Count(md, "|  |  |") != 4 {
		t.Fatalf("rows 2, 4, 5 and 6 should be empty rows:\n%s", md)
	}
}

func TestXLSXThatDisagreesWithItsDimensionFailsItsCheck(t *testing.T) {
	e := newEnv(t)
	ws := `<worksheet><dimension ref="A1:C9"/><sheetData>` +
		`<row r="1">` + inline("A1", "Item") + inline("B1", "Amount") + `</row>` +
		`<row r="2">` + inline("A2", "Rent") + `<c r="B2"><v>1250</v></c></row>` +
		`</sheetData></worksheet>`
	u, _ := sheetUnit(t, e, "short.xlsx", sheetMIME, xlsxWith(t, ws), false)
	if u.CheckResult != "rows 2/9 columns 2/3" || u.Status == unitPassed {
		t.Fatalf("mismatched check %q status %s", u.CheckResult, u.Status)
	}
	// A sheet with no dimension is counted from a second, token-by-token read: a row
	// with no r follows the row before it, and a jump to r="4" still counts four rows.
	if rows, cols, err := sheetExtent([]byte(`<worksheet><sheetData><row><c/><c/></row><row r="4"><c r="C4"/></row></sheetData></worksheet>`), ""); err != nil || rows != 4 || cols != 3 {
		t.Fatalf("extent %d by %d, %v", rows, cols, err)
	}
	if rows, cols, _ := sheetExtent([]byte(`<worksheet><dimension ref="A1"/><sheetData/></worksheet>`), "A1"); rows != 0 || cols != 0 {
		t.Fatalf("an empty sheet reads as %d by %d", rows, cols)
	}
}

func TestXLSXCellsWithoutReferencesAreRead(t *testing.T) {
	e := newEnv(t)
	ws := `<worksheet><sheetData>` +
		`<row>` + inline("", "Item") + inline("", "Amount") + `</row>` +
		`<row>` + inline("", "Rent") + `<c><v>1250</v></c></row>` +
		`</sheetData></worksheet>`
	u, md := sheetUnit(t, e, "norefs.xlsx", sheetMIME, xlsxWith(t, ws), true)
	if u.CheckResult != "rows 2/2 columns 2/2" || u.Status != unitPassed || !strings.Contains(md, "| Rent | 1250 |") {
		t.Fatalf("no-ref check %q status %s:\n%s", u.CheckResult, u.Status, md)
	}
}

func TestRaggedCSVIsReadNotDamaged(t *testing.T) {
	e := newEnv(t)
	csv := "Date,Payee,Amount,Note\n2026-10-01,Landlord,1250\n2026-10-02,\"Utility, water\",80,\"paid\nlate\"\n\n2026-10-03\n"
	u, md := sheetUnit(t, e, "bank.csv", "text/csv", []byte(csv), true)
	if u.CheckResult != "rows 4/4 columns 4/4" || u.Status != unitPassed {
		t.Fatalf("ragged check %q status %s", u.CheckResult, u.Status)
	}
	if !strings.Contains(md, "| Utility, water | 80 | paid late |") || !strings.Contains(md, "| 2026-10-03 |") {
		t.Fatalf("ragged markdown:\n%s", md)
	}
	if rows, cols := csvExtent([]byte("a,\"b\"\"c,d\"\r\n\r\n,\n")); rows != 2 || cols != 2 {
		t.Fatalf("csv extent %d by %d", rows, cols)
	}
}
