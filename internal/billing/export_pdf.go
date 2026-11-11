package billing

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"golang.org/x/text/encoding/charmap"
)

const (
	pdfPageWidth    = 595
	pdfPageHeight   = 842
	pdfMargin       = 50
	pdfLineHeight   = 14
	pdfFontSize     = 10
	pdfLinesPerPage = 48
	pdfMaxLineRunes = 95
)

var pdfEncoder = charmap.Windows1252.NewEncoder()

// WritePDF writes an internal billing overview as a small PDF 1.4 document. It
// uses the built-in Helvetica font and WinAnsi encoding, so no external font or
// PDF library is needed. The export contains ids and billing data only.
func WritePDF(w io.Writer, data ExportData) error {
	pages := paginate(pdfContentLines(data), pdfLinesPerPage)
	return writePDF(w, pages)
}

func pdfContentLines(data ExportData) []string {
	lines := []string{
		"Abrechnungsvorschlag",
		"",
		"Bericht: " + data.ReportID.String(),
		"",
		"Leistungsschluessel | Leistung | Menge | Datum | Abrechnungsstelle | Vertragsreferenz",
		strings.Repeat("-", 95),
	}
	for _, line := range data.Lines {
		lines = append(lines, fmt.Sprintf(
			"%s | %s | %d | %s | %s | %s",
			line.ServiceCode,
			line.ServiceName,
			line.Quantity,
			line.PerformedAt.UTC().Format("2006-01-02"),
			line.PayerName,
			line.ContractReference,
		))
	}
	lines = append(lines, "", "Status: freigegeben")
	lines = append(lines, "Interner Export. Keine Kasseneinreichung.")
	if len(data.Run.Findings) > 0 {
		lines = append(lines, "", "Hinweise:")
		for _, finding := range data.Run.Findings {
			lines = append(lines, fmt.Sprintf("%s %s: %s", finding.Severity, finding.Code, finding.Message))
		}
	}
	return lines
}

func paginate(lines []string, perPage int) [][]string {
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		wrapped = append(wrapped, wrapLine(line, pdfMaxLineRunes)...)
	}

	pages := make([][]string, 0, (len(wrapped)+perPage-1)/perPage)
	for start := 0; start < len(wrapped); start += perPage {
		end := start + perPage
		if end > len(wrapped) {
			end = len(wrapped)
		}
		pages = append(pages, wrapped[start:end])
	}
	if len(pages) == 0 {
		pages = append(pages, []string{})
	}
	return pages
}

func wrapLine(line string, limit int) []string {
	if limit < 1 {
		return []string{line}
	}
	runes := []rune(line)
	if len(runes) <= limit {
		return []string{line}
	}
	result := make([]string, 0, (len(runes)+limit-1)/limit)
	for len(runes) > limit {
		result = append(result, string(runes[:limit]))
		runes = runes[limit:]
	}
	if len(runes) > 0 {
		result = append(result, string(runes))
	}
	return result
}

func writePDF(w io.Writer, pages [][]string) error {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")

	offsets := make([]int, 0, 3+2*len(pages))
	startObject := func(number int) {
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n", number)
	}

	// Object 1: catalog.
	startObject(1)
	buf.WriteString("<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")

	// Object 2: page tree.
	kids := make([]string, 0, len(pages))
	for index := range pages {
		kids = append(kids, fmt.Sprintf("%d 0 R", 4+2*index))
	}
	startObject(2)
	fmt.Fprintf(&buf, "<< /Type /Pages /Kids [%s] /Count %d >>\nendobj\n", strings.Join(kids, " "), len(pages))

	// Object 3: shared font.
	startObject(3)
	buf.WriteString("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>\nendobj\n")

	for index, page := range pages {
		pageObject := 4 + 2*index
		contentObject := 5 + 2*index

		startObject(pageObject)
		fmt.Fprintf(&buf,
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>\nendobj\n",
			pdfPageWidth, pdfPageHeight, contentObject)

		content := pdfPageContent(page)
		startObject(contentObject)
		fmt.Fprintf(&buf, "<< /Length %d >>\nstream\n", len(content))
		buf.Write(content)
		buf.WriteString("\nendstream\nendobj\n")
	}

	xrefOffset := buf.Len()
	totalObjects := 3 + 2*len(pages)
	fmt.Fprintf(&buf, "xref\n0 %d\n", totalObjects+1)
	buf.WriteString("0000000000 65535 f \n")
	for _, offset := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", totalObjects+1, xrefOffset)

	_, err := w.Write(buf.Bytes())
	return err
}

func pdfPageContent(lines []string) []byte {
	var builder strings.Builder
	builder.WriteString("BT\n")
	fmt.Fprintf(&builder, "/F1 %d Tf\n", pdfFontSize)
	fmt.Fprintf(&builder, "%d %d Td\n", pdfMargin, pdfPageHeight-pdfMargin)
	fmt.Fprintf(&builder, "%d TL\n", pdfLineHeight)
	for _, line := range lines {
		fmt.Fprintf(&builder, "(%s) Tj T*\n", pdfEscape(line))
	}
	builder.WriteString("ET\n")
	return []byte(builder.String())
}

func pdfEscape(value string) string {
	encoded := encodeWinAnsi(value)
	var builder strings.Builder
	for _, char := range encoded {
		switch {
		case char == '\\' || char == '(' || char == ')':
			builder.WriteByte('\\')
			builder.WriteByte(char)
		case char < 0x20 || char == 0x7f:
			builder.WriteByte(' ')
		default:
			builder.WriteByte(char)
		}
	}
	return builder.String()
}

// encodeWinAnsi maps text to Windows-1252. A rune that the encoding cannot
// represent becomes '?', so the PDF stays valid even for unusual input.
func encodeWinAnsi(value string) []byte {
	result := make([]byte, 0, len(value))
	for _, r := range value {
		encoded, err := pdfEncoder.Bytes([]byte(string(r)))
		if err != nil || len(encoded) == 0 {
			result = append(result, '?')
			continue
		}
		result = append(result, encoded...)
	}
	return result
}
