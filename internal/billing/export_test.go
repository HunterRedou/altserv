package billing

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestWriteCSV(t *testing.T) {
	reportID := uuid.New()
	data := ExportData{
		ReportID: reportID,
		Lines: []BillingLine{
			{
				ServiceCode:       "=SUM(A1)",
				ServiceName:       "Leistung",
				Quantity:          2,
				PerformedAt:       time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC),
				PayerName:         "+Kasse",
				ContractReference: "-V1",
				LegalArea:         "sgb_xi",
			},
		},
	}

	var buffer bytes.Buffer
	if err := WriteCSV(&buffer, data); err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}

	reader := csv.NewReader(&buffer)
	reader.Comma = ';'
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	if records[0][0] != "Bericht-ID" || records[0][1] != "Leistungsschluessel" {
		t.Fatalf("header = %v", records[0])
	}
	row := records[1]
	if row[0] != reportID.String() {
		t.Errorf("report id = %q, want %q", row[0], reportID)
	}
	if row[1] != "'=SUM(A1)" {
		t.Errorf("service code = %q, want formula-safe value", row[1])
	}
	if row[5] != "'+Kasse" || row[6] != "'-V1" {
		t.Errorf("payer/reference not formula-safe: %q %q", row[5], row[6])
	}
	if row[3] != "2" {
		t.Errorf("quantity = %q, want 2", row[3])
	}
	if row[4] != "2026-11-11T10:00:00Z" {
		t.Errorf("performed at = %q", row[4])
	}
}

func TestWritePDF(t *testing.T) {
	data := ExportData{
		ReportID: uuid.New(),
		Run: BillingRun{
			Status: "approved",
			Findings: []BillingFinding{
				{Severity: SeverityWarning, Code: CodeServiceRepeated, Message: "Bitte pruefen"},
			},
		},
		Lines: []BillingLine{
			{
				ServiceCode:       "S-1",
				ServiceName:       "Leistung",
				Quantity:          1,
				PerformedAt:       time.Now().UTC(),
				PayerName:         "Pflegekasse",
				ContractReference: "V-1",
			},
		},
	}

	var buffer bytes.Buffer
	if err := WritePDF(&buffer, data); err != nil {
		t.Fatalf("WritePDF: %v", err)
	}
	content := buffer.String()
	if !strings.HasPrefix(content, "%PDF-1.4\n") {
		t.Fatalf("missing PDF header")
	}
	if !strings.HasSuffix(content, "%%EOF\n") {
		t.Fatalf("missing PDF trailer")
	}
	if !strings.Contains(content, "/Type /Catalog") || !strings.Contains(content, "/BaseFont /Helvetica") {
		t.Fatalf("missing catalog or font")
	}
	if !strings.Contains(content, "Interner Export. Keine Kasseneinreichung.") {
		t.Fatalf("missing internal-only notice")
	}
	if strings.Contains(content, "\x00") {
		t.Fatalf("PDF contains a NUL byte")
	}
}

func TestWritePDFEscapesParenthesesAndBackslashes(t *testing.T) {
	var buffer bytes.Buffer
	if err := WritePDF(&buffer, ExportData{Lines: []BillingLine{{ServiceName: `a(b)\c`}}}); err != nil {
		t.Fatalf("WritePDF: %v", err)
	}
	if !strings.Contains(buffer.String(), `a\(b\)\\c`) {
		t.Fatalf("PDF text is not escaped")
	}
}
