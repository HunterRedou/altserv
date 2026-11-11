package billing

import (
	"encoding/csv"
	"io"
	"strconv"
	"strings"
	"time"
)

// csvHeader is stable so an internal consumer can rely on the columns. The
// export contains ids and billing data only, never patient names or free text.
var csvHeader = []string{
	"Bericht-ID",
	"Leistungsschluessel",
	"Leistung",
	"Menge",
	"Leistungsdatum",
	"Abrechnungsstelle",
	"Vertragsreferenz",
	"Rechtsgebiet",
}

// WriteCSV writes the billing lines of an approved run as semicolon-separated
// CSV. It does not validate the run; the service validates before calling it.
func WriteCSV(w io.Writer, data ExportData) error {
	writer := csv.NewWriter(w)
	writer.Comma = ';'

	if err := writer.Write(csvHeader); err != nil {
		return err
	}
	for _, line := range data.Lines {
		row := []string{
			data.ReportID.String(),
			csvSafe(line.ServiceCode),
			csvSafe(line.ServiceName),
			strconv.Itoa(line.Quantity),
			line.PerformedAt.UTC().Format(time.RFC3339),
			csvSafe(line.PayerName),
			csvSafe(line.ContractReference),
			csvSafe(line.LegalArea),
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

// csvSafe prevents a spreadsheet from evaluating a cell as a formula. The value
// itself is preserved after the leading apostrophe.
func csvSafe(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	switch value[0] {
	case '=', '+', '-', '@':
		return "'" + value
	default:
		return value
	}
}
