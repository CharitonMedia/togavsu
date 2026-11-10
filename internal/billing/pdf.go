package billing

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"image/color"
	"image/jpeg"
	"strings"
	"sync"
	"unicode"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// pdfLayout is the invoice page design in PDF points on US Letter: margins, columns, line
// spacing and font sizes. It is a fixed page design shipped with the code, like billing.css,
// not a firm setting; the firm's name, contact lines, logo and terms come from the registry.
type pdfLayout struct {
	Top           float64 `json:"top"`
	Margin        float64 `json:"margin"`
	Right         float64 `json:"right"`
	LogoTop       float64 `json:"logo_top"`
	LogoMaxWidth  float64 `json:"logo_max_width"`
	LogoMaxHeight float64 `json:"logo_max_height"`
	HeaderX       float64 `json:"header_x"`
	AmountX       float64 `json:"amount_x"`
	TotalsX       float64 `json:"totals_x"`
	LinesBottom   float64 `json:"lines_bottom"`
	TotalsBottom  float64 `json:"totals_bottom"`
	FooterY       float64 `json:"footer_y"`
	Line          float64 `json:"line"`
	TitleGap      float64 `json:"title_gap"`
	Gap           float64 `json:"gap"`
	Section       float64 `json:"section"`
	RuleGap       float64 `json:"rule_gap"`
	TitleSize     float64 `json:"title_size"`
	HeadingSize   float64 `json:"heading_size"`
	BodySize      float64 `json:"body_size"`
	SmallSize     float64 `json:"small_size"`
	WrapLines     int     `json:"wrap_lines"`
	WrapMessage   int     `json:"wrap_message"`
}

//go:embed pdf_layout.json
var pdfLayoutJSON []byte

var loadLayout = sync.OnceValues(func() (pdfLayout, error) {
	var l pdfLayout
	dec := json.NewDecoder(bytes.NewReader(pdfLayoutJSON))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		return pdfLayout{}, fmt.Errorf("billing: pdf layout: %w", err)
	}
	return l, nil
})

// renderPDF draws the invoice on US Letter pages with the firm's name, address, logo and terms.
// ponytail: ceiling: a built-in text PDF writer with the standard Helvetica font; characters
// outside Latin-1 print as "?", and only a JPEG logo is drawn. Upgrade path: a PDF
// library with embedded fonts and PNG logos. Trigger: a firm name or client name outside
// Latin-1, or a PNG logo upload.
func (s *Service) renderPDF(ctx context.Context, inv Invoice) ([]byte, error) {
	L, err := loadLayout()
	if err != nil {
		return nil, err
	}
	firm := s.setting(ctx, "firm.name")
	if firm == "" {
		firm = "Invoice"
	}
	var logo *jpegInfo
	if id := domain.ID(s.setting(ctx, "firm.logo_file_id")); !id.IsZero() && s.d.Logo != nil {
		if raw, err := s.d.Logo(ctx, id); err == nil {
			if info, err := parseJPEG(raw); err == nil {
				logo = &info
			}
		}
	}
	doc := &pdfDoc{}
	page := doc.page()
	y := L.Top
	if logo != nil {
		h := L.LogoMaxHeight
		w := h * float64(logo.width) / float64(logo.height)
		if w > L.LogoMaxWidth {
			w, h = L.LogoMaxWidth, L.LogoMaxWidth*float64(logo.height)/float64(logo.width)
		}
		page.imageAt(w, h, L.Margin, L.LogoTop-h)
		y = L.Top - h
	}
	page.text(L.Margin, y, L.TitleSize, firm)
	y -= L.TitleSize
	for _, line := range []string{s.setting(ctx, "firm.address"), s.setting(ctx, "firm.phone"), s.setting(ctx, "firm.email"), s.setting(ctx, "firm.website")} {
		if strings.TrimSpace(line) != "" {
			page.text(L.Margin, y, L.BodySize, line)
			y -= L.Line
		}
	}
	hy := L.Top
	page.text(L.HeaderX, hy, L.TitleSize, "INVOICE")
	hy -= L.TitleGap
	for _, line := range []string{"Number: " + inv.Number, "Date: " + inv.IssuedOn, "Due: " + inv.DueOn, "Status: " + inv.Status} {
		page.text(L.HeaderX, hy, L.BodySize, line)
		hy -= L.Line
	}
	y -= L.Gap
	page.text(L.Margin, y, L.HeadingSize, "Matter "+inv.MatterNum+" "+inv.MatterName)
	for _, c := range s.clientParties(inv.MatterID) {
		if contact, err := s.d.Practice.Contact(ctx, c); err == nil {
			y -= L.Line
			page.text(L.Margin, y, L.BodySize, "Bill to: "+contact.FullName)
		}
	}
	y -= L.Section
	header := func() {
		page.text(L.Margin, y, L.BodySize, "Description")
		page.text(L.AmountX, y, L.BodySize, "Amount")
		y -= L.RuleGap
		page.rule(L.Margin, y, L.Right)
		y -= L.Gap
	}
	newPage := func() {
		page = doc.page()
		y = L.Top
	}
	header()
	for _, l := range inv.Lines {
		parts := wrap(l.Description, L.WrapLines)
		if y-L.Line*float64(len(parts)) < L.LinesBottom {
			newPage()
			header()
		}
		page.textRight(L.Right, y, L.BodySize, money(l.AmountCents))
		for _, part := range parts {
			page.text(L.Margin, y, L.BodySize, part)
			y -= L.Line
		}
	}
	y -= L.RuleGap
	page.rule(L.TotalsX, y, L.Right)
	y -= L.Gap
	type total struct{ label, amount string }
	totals := []total{{"Subtotal", money(inv.SubtotalCents)}}
	if !isZero(int64(inv.AdjustmentCents)) {
		totals = append(totals, total{"Adjustment", money(inv.AdjustmentCents)})
	}
	if !isZero(int64(inv.InterestCents)) {
		totals = append(totals, total{"Late interest", money(inv.InterestCents)})
	}
	totals = append(totals, total{"Total", money(inv.TotalCents)}, total{"Paid", money(inv.AmountPaidCents)})
	if !isZero(int64(inv.WriteOffCents)) {
		totals = append(totals, total{"Written off", money(inv.WriteOffCents)})
	}
	totals = append(totals, total{"Balance due", money(inv.BalanceDueCents)})
	for _, row := range totals {
		if y < L.TotalsBottom {
			newPage()
		}
		page.text(L.TotalsX, y, L.BodySize, row.label)
		page.textRight(L.Right, y, L.BodySize, row.amount)
		y -= L.Line
	}
	if inv.ClientMessage != "" {
		y -= L.Gap
		for _, part := range wrap(inv.ClientMessage, L.WrapMessage) {
			if y < L.TotalsBottom {
				newPage()
			}
			page.text(L.Margin, y, L.BodySize, part)
			y -= L.Line
		}
	}
	terms := s.settingInt(ctx, "billing.payment_terms_days")
	page.text(L.Margin, L.FooterY, L.SmallSize, fmt.Sprintf("Payment is due within %d days of the invoice date.", terms))
	return doc.bytes(logo), nil
}

func wrap(s string, width int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		line := ""
		for _, w := range words {
			if line != "" && len(line)+1+len(w) > width {
				out = append(out, line)
				line = ""
			}
			if line != "" {
				line += " "
			}
			line += w
		}
		out = append(out, line)
	}
	return out
}

type pdfDoc struct{ pages []*pdfPage }

type pdfPage struct {
	buf   bytes.Buffer
	image bool
}

func (d *pdfDoc) page() *pdfPage {
	p := &pdfPage{}
	d.pages = append(d.pages, p)
	return p
}

// latin1 maps text to WinAnsi bytes and escapes PDF string delimiters.
func latin1(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '(' || r == ')' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\t':
			b.WriteByte(' ')
		case unicode.IsControl(r):
		case r <= unicode.MaxLatin1:
			b.WriteByte(byte(r))
		default:
			b.WriteByte('?')
		}
	}
	return b.String()
}

func (p *pdfPage) text(x, y, size float64, s string) {
	fmt.Fprintf(&p.buf, "BT /F1 %.1f Tf %.2f %.2f Td (%s) Tj ET\n", size, x, y, latin1(s))
}

// textRight right-aligns s at x using Helvetica's 0.556 em digit width as the estimate.
func (p *pdfPage) textRight(x, y, size float64, s string) {
	p.text(x-float64(len(s))*size*0.556, y, size, s)
}

func (p *pdfPage) rule(x1, y, x2 float64) {
	fmt.Fprintf(&p.buf, "0.5 w %.2f %.2f m %.2f %.2f l S\n", x1, y, x2, y)
}

func (p *pdfPage) imageAt(w, h, x, y float64) {
	p.image = true
	fmt.Fprintf(&p.buf, "q %.2f 0 0 %.2f %.2f %.2f cm /Im1 Do Q\n", w, h, x, y)
}

func (d *pdfDoc) bytes(logo *jpegInfo) []byte {
	var out bytes.Buffer
	var offsets []int
	obj := func(body string) {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}
	out.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	// 1 catalog, 2 pages, 3 font, 4 image (or a placeholder), then content+page pairs.
	n := len(d.pages)
	kids := make([]string, n)
	for i := range d.pages {
		kids[i] = fmt.Sprintf("%d 0 R", 7+2*i)
	}
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), n))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	if logo != nil {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "4 0 obj\n<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /%s /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n",
			logo.width, logo.height, logo.space, len(logo.data))
		out.Write(logo.data)
		out.WriteString("\nendstream\nendobj\n")
	} else {
		obj("null")
	}
	obj("<< /ProcSet [/PDF /Text /ImageC] >>")
	for _, p := range d.pages {
		content := p.buf.Bytes()
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n<< /Length %d >>\nstream\n", len(offsets), len(content))
		out.Write(content)
		out.WriteString("endstream\nendobj\n")
		res := "/Font << /F1 3 0 R >>"
		if p.image && logo != nil {
			res += " /XObject << /Im1 4 0 R >>"
		}
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << %s >> /Contents %d 0 R >>", res, len(offsets)))
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, off := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return out.Bytes()
}

type jpegInfo struct {
	width, height int
	space         string
	data          []byte
}

// parseJPEG reads the frame size and colour model of a JPEG with the standard library decoder.
// The PDF embeds the original bytes with DCTDecode.
func parseJPEG(b []byte) (jpegInfo, error) {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return jpegInfo{}, err
	}
	space := "DeviceRGB"
	switch cfg.ColorModel {
	case color.GrayModel:
		space = "DeviceGray"
	case color.CMYKModel:
		space = "DeviceCMYK"
	}
	return jpegInfo{width: cfg.Width, height: cfg.Height, space: space, data: b}, nil
}
