package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestComplianceDocumentation guards the review package against drift. It
// checks that every required document exists, that no placeholder remains, that
// relative links and file references resolve, and that the review packet lists
// every policy. It does not replace the professional review.
func TestComplianceDocumentation(t *testing.T) {
	complianceDir := filepath.Join("docs", "compliance")

	required := []string{
		"README.md",
		"data-classification.md",
		"processing-activities.md",
		"privacy-impact-assessment.md",
		"retention-policy.md",
		"deletion-policy.md",
		"access-control-policy.md",
		"incident-response.md",
		"backup-policy.md",
		"billing-requirements.md",
		"review-packet.md",
		"review-record.md",
	}

	for _, name := range required {
		if !fileExists(filepath.Join(complianceDir, name)) {
			t.Errorf("required compliance document is missing: %s", name)
		}
	}

	files := make([]string, 0, len(required)+1)
	for _, name := range required {
		files = append(files, filepath.Join(complianceDir, name))
	}
	files = append(files, filepath.Join("docs", "overview.md"))

	for _, path := range files {
		if !fileExists(path) {
			continue
		}
		checkDocument(t, path)
	}

	packet, err := os.ReadFile(filepath.Join(complianceDir, "review-packet.md"))
	if err != nil {
		t.Fatalf("read review packet: %v", err)
	}
	for _, name := range required {
		if name == "README.md" || name == "review-packet.md" || name == "review-record.md" {
			continue
		}
		if !strings.Contains(string(packet), name) {
			t.Errorf("review-packet.md does not mention policy %s", name)
		}
	}

	readme, err := os.ReadFile(filepath.Join(complianceDir, "README.md"))
	if err != nil {
		t.Fatalf("read compliance README: %v", err)
	}
	for _, name := range []string{"review-packet.md", "review-record.md"} {
		if !strings.Contains(string(readme), name) {
			t.Errorf("README.md does not link %s", name)
		}
	}
}

func checkDocument(t *testing.T, path string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(data)
	docDir := filepath.Dir(path)

	placeholderRE := regexp.MustCompile(`\b(TODO|TBD|FIXME|lorem)\b`)
	if loc := placeholderRE.FindStringIndex(text); loc != nil {
		t.Errorf("%s contains placeholder %q", path, text[loc[0]:loc[1]])
	}

	linkRE := regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)
	for _, match := range linkRE.FindAllStringSubmatch(text, -1) {
		target := strings.TrimSpace(match[1])
		if target == "" || strings.HasPrefix(target, "#") ||
			strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
			continue
		}
		target = strings.SplitN(target, "#", 2)[0]
		if target == "" {
			continue
		}
		if !fileExists(filepath.Join(docDir, target)) {
			t.Errorf("%s has a broken link: %s", path, match[1])
		}
	}

	fileRE := regexp.MustCompile("`([^`\\n]+\\.(?:md|txt|sh|yml|json|sql|go))`")
	for _, match := range fileRE.FindAllStringSubmatch(text, -1) {
		target := match[1]
		if strings.HasPrefix(target, "/") || strings.Contains(target, "://") ||
			strings.Contains(target, "*") {
			continue
		}
		if !fileExists(filepath.Join(docDir, target)) && !fileExists(target) {
			t.Errorf("%s references a missing file: %s", path, target)
		}
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
