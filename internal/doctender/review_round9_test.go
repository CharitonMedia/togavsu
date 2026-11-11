package doctender

import (
	"bytes"
	"encoding/binary"
	"errors"
	"runtime"
	"strings"
	"testing"
)

// Opus checkpoint P1 (on 755454c): a compound file of a few KB cannot make cfbStream
// read gigabytes. The FAT lists each sector once, a chain may not revisit a sector, and
// a stream may not declare more bytes than the file holds.
func TestCompoundFileChainsReadEachSectorOnce(t *testing.T) {
	le := binary.LittleEndian
	stream := bytes.Repeat([]byte("x"), 5000)
	f := cfbNamed(t, "WordDocument", stream, false)

	// All 109 header DIFAT slots name the one FAT sector: still one FAT sector.
	repeated := append([]byte(nil), f...)
	for k := 0; k < 109; k++ {
		le.PutUint32(repeated[76+k*4:], 0)
	}
	if got, ok := cfbStream(repeated, "WordDocument"); !ok || !bytes.Equal(got, stream) {
		t.Fatalf("repeated DIFAT entries: found %v, %d bytes", ok, len(got))
	}

	// The stream's first sector (2) points to itself.
	loop := append([]byte(nil), repeated...)
	le.PutUint32(loop[512+2*4:], 2)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, ok := cfbStream(loop, "WordDocument")
	runtime.ReadMemStats(&after)
	if ok {
		t.Fatal("a self-looping chain was read as a stream")
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<20 {
		t.Fatalf("a %d-byte file made cfbStream allocate %d bytes", len(loop), grew)
	}

	// The stream's directory entry (sector 1, second entry) declares 2 GB.
	big := append([]byte(nil), f...)
	le.PutUint32(big[1024+128+120:], 0x7FFFFFF0)
	if _, ok := cfbStream(big, "WordDocument"); ok {
		t.Fatal("a stream larger than the file was read")
	}
}

// Opus checkpoint P1 (on 755454c): a page's contents and its fonts' CMaps share a
// budget, so a repeated Contents reference or an oversized bfrange is a decode-limit
// error rather than gigabytes of memory.
func TestPDFPageContentsAndCMapsShareABudget(t *testing.T) {
	lowCaps(t, 1<<20, 100)
	bomb := &pdfStream{Dict: pdfDict{"Filter": pdfName("FlateDecode")}, Raw: zlibBytes(make([]byte, 700<<10))}
	d := &pdfDoc{objs: map[int]any{5: bomb}}
	if _, err := d.contents(pdfDict{"Contents": pdfArray{pdfRef{5, 0}, pdfRef{5, 0}}}); !errors.Is(err, errDecodeLimit) {
		t.Fatalf("one stream named twice in Contents: %v", err)
	}
	if b, err := d.contents(pdfDict{"Contents": pdfArray{pdfRef{5, 0}}}); err != nil || len(b) != 700<<10+1 {
		t.Fatalf("the stream once: %d bytes, %v", len(b), err)
	}

	old := cmapEntryCap
	cmapEntryCap = func() int { return 1000 }
	t.Cleanup(func() { cmapEntryCap = old })
	cmap := func(hi string) *pdfStream {
		return &pdfStream{Raw: []byte("begincmap\n1 beginbfrange\n<0000> <" + hi + "> <0041>\nendbfrange\nendcmap\n" + strings.Repeat(" ", 70000))}
	}
	doc := &pdfDoc{objs: map[int]any{
		1:  &pdfStream{Raw: []byte("BT ET")},
		7:  cmap("0257"), // 600 entries
		9:  cmap("0257"),
		8:  pdfDict{"ToUnicode": pdfRef{7, 0}},
		10: pdfDict{"ToUnicode": pdfRef{9, 0}},
		11: pdfDict{"ToUnicode": pdfRef{7, 0}},
		12: cmap("FFFF"), // 65,536 entries
		13: pdfDict{"ToUnicode": pdfRef{12, 0}},
	}}
	page := func(fonts pdfDict) pdfDict {
		return pdfDict{"Contents": pdfRef{1, 0}, "Resources": pdfDict{"Font": fonts}}
	}
	doc.pages = []pdfDict{
		page(pdfDict{"F1": pdfRef{13, 0}}),
		page(pdfDict{"F1": pdfRef{8, 0}, "F2": pdfRef{10, 0}}),
		page(pdfDict{"F1": pdfRef{8, 0}, "F3": pdfRef{11, 0}}),
	}
	if _, err := doc.pageText(0); !errors.Is(err, errDecodeLimit) {
		t.Fatalf("a 65,536-entry bfrange under a cap of 1,000: %v", err)
	}
	if _, err := doc.pageText(1); !errors.Is(err, errDecodeLimit) {
		t.Fatalf("two 600-entry maps on one page under a cap of 1,000: %v", err)
	}
	if _, err := doc.pageText(2); err != nil {
		t.Fatalf("one 600-entry map named by two fonts: %v", err)
	}
}

// Found alongside: the objects parsed from object streams can keep those streams' bytes
// alive, so all of a file's object streams together stay under the decode cap.
func TestObjectStreamsShareTheDecodeCap(t *testing.T) {
	lowCaps(t, 1<<20, 100)
	objStm := func() *pdfStream {
		return &pdfStream{Dict: pdfDict{"Type": pdfName("ObjStm"), "N": 0, "First": 0, "Filter": pdfName("FlateDecode")}, Raw: zlibBytes(make([]byte, 700<<10))}
	}
	single := &pdfDoc{objs: map[int]any{1: objStm()}}
	if err := single.expandObjectStreams(); err != nil {
		t.Fatalf("one 700 KiB object stream under a 1 MiB cap: %v", err)
	}
	double := &pdfDoc{objs: map[int]any{1: objStm(), 2: objStm()}}
	if err := double.expandObjectStreams(); !errors.Is(err, errDecodeLimit) {
		t.Fatalf("two 700 KiB object streams under a 1 MiB cap: %v", err)
	}
}
