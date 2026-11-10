package billing

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// renderPDF draws the invoice on US Letter pages with the firm's name, address, logo and terms.
// ponytail: ceiling: a built-in text PDF writer with the standard Helvetica font; characters
// outside Latin-1 print as "?", and only a baseline JPEG logo is drawn. Upgrade path: a PDF
// library with embedded fonts and PNG logos. Trigger: a firm name or client name outside
// Latin-1, or a PNG logo upload.
func (s *Service) renderPDF(ctx context.Context, inv Invoice) ([]byte, error) {
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
	y := 740.0
	if logo != nil {
		h := 54.0
		w := h * float64(logo.width) / float64(logo.height)
		if w > 160 {
			w, h = 160, 160*float64(logo.height)/float64(logo.width)
		}
		page.imageAt(w, h, 54, 760-h)
		y = 740 - h
	}
	page.text(54, y, 16, firm)
	y -= 16
	for _, line := range []string{s.setting(ctx, "firm.address"), s.setting(ctx, "firm.phone"), s.setting(ctx, "firm.email"), s.setting(ctx, "firm.website")} {
		if strings.TrimSpace(line) != "" {
			page.text(54, y, 10, line)
			y -= 13
		}
	}
	page.text(400, 740, 16, "INVOICE")
	page.text(400, 722, 10, "Number: "+inv.Number)
	page.text(400, 709, 10, "Date: "+inv.IssuedOn)
	page.text(400, 696, 10, "Due: "+inv.DueOn)
	page.text(400, 683, 10, "Status: "+inv.Status)
	y -= 14
	page.text(54, y, 11, "Matter "+inv.MatterNum+" "+inv.MatterName)
	for _, c := range s.clientParties(inv.MatterID) {
		if contact, err := s.d.Practice.Contact(ctx, c); err == nil {
			y -= 13
			page.text(54, y, 10, "Bill to: "+contact.FullName)
		}
	}
	y -= 28
	header := func() {
		page.text(54, y, 10, "Description")
		page.text(480, y, 10, "Amount")
		y -= 6
		page.rule(54, y, 558)
		y -= 14
	}
	header()
	for _, l := range inv.Lines {
		parts := wrap(l.Description, 80)
		if y-float64(13*len(parts)) < 120 {
			page = doc.page()
			y = 740
			header()
		}
		for i, part := range parts {
			page.text(54, y, 10, part)
			if i == 0 {
				page.textRight(558, y, 10, money(l.AmountCents))
			}
			y -= 13
		}
	}
	y -= 6
	page.rule(360, y, 558)
	y -= 14
	totals := [][2]string{{"Subtotal", money(inv.SubtotalCents)}}
	if inv.AdjustmentCents != 0 {
		totals = append(totals, [2]string{"Adjustment " + inv.AdjustmentReason, money(inv.AdjustmentCents)})
	}
	if inv.InterestCents != 0 {
		totals = append(totals, [2]string{"Late interest", money(inv.InterestCents)})
	}
	totals = append(totals, [2]string{"Total", money(inv.TotalCents)}, [2]string{"Paid", money(inv.AmountPaidCents)})
	if inv.WriteOffCents != 0 {
		totals = append(totals, [2]string{"Written off", money(inv.WriteOffCents)})
	}
	totals = append(totals, [2]string{"Balance due", money(inv.BalanceDueCents)})
	for _, row := range totals {
		if y < 60 {
			page = doc.page()
			y = 740
		}
		page.text(360, y, 10, row[0])
		page.textRight(558, y, 10, row[1])
		y -= 13
	}
	if inv.ClientMessage != "" {
		y -= 14
		for _, part := range wrap(inv.ClientMessage, 95) {
			if y < 60 {
				page = doc.page()
				y = 740
			}
			page.text(54, y, 10, part)
			y -= 13
		}
	}
	terms := s.settingInt(ctx, "billing.payment_terms_days")
	page.text(54, 48, 9, fmt.Sprintf("Payment is due within %d days of the invoice date.", terms))
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
		case r < 32:
		case r < 256:
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

// parseJPEG reads the frame size and colour space from a baseline or progressive JPEG.
func parseJPEG(b []byte) (jpegInfo, error) {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return jpegInfo{}, errors.New("not a jpeg")
	}
	i := 2
	for i+4 <= len(b) {
		if b[i] != 0xFF {
			return jpegInfo{}, errors.New("bad jpeg marker")
		}
		marker := b[i+1]
		size := int(binary.BigEndian.Uint16(b[i+2:]))
		if (marker == 0xC0 || marker == 0xC1 || marker == 0xC2) && i+9 < len(b) {
			h := int(binary.BigEndian.Uint16(b[i+5:]))
			w := int(binary.BigEndian.Uint16(b[i+7:]))
			space := "DeviceRGB"
			switch b[i+9] {
			case 1:
				space = "DeviceGray"
			case 4:
				space = "DeviceCMYK"
			}
			if w == 0 || h == 0 {
				break
			}
			return jpegInfo{width: w, height: h, space: space, data: b}, nil
		}
		i += 2 + size
	}
	return jpegInfo{}, errors.New("no jpeg frame")
}
