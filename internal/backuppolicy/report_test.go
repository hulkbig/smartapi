package backuppolicy

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBackupReportBoundedHistoryAndAtomicUpdates(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadReport(dir); !os.IsNotExist(err) {
		t.Fatalf("missing report = %v", err)
	}
	for i := 0; i < 12; i++ {
		if err := WriteRun(dir, Run{OperationID: fmt.Sprint(i), Status: "running", StartedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	completed := time.Now()
	if err := WriteRun(dir, Run{OperationID: "11", Status: "success", VerifiedReadback: true, Bytes: 42, CompletedAt: &completed}); err != nil {
		t.Fatal(err)
	}
	report, err := ReadReport(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Runs) != 10 || report.Runs[0].OperationID != "11" || report.Runs[1].OperationID != "10" || !report.Runs[0].VerifiedReadback || report.Runs[0].Bytes != 42 {
		t.Fatalf("bad report: %+v", report)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, ".backup-status-*"))
	if len(matches) != 0 {
		t.Fatal("temporary status file leaked")
	}
	if err := os.WriteFile(filepath.Join(dir, reportFile), make([]byte, 65537), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadReport(dir); err == nil {
		t.Fatal("oversize accepted")
	}
	os.Remove(filepath.Join(dir, reportFile))
	target := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(target, []byte(`{"version":1,"runs":[]}`), 0600)
	os.Symlink(target, filepath.Join(dir, reportFile))
	if _, err = ReadReport(dir); err == nil {
		t.Fatal("symlink accepted")
	}
}
