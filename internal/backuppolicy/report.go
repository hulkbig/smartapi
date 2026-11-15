package backuppolicy

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Report contains only receiver-observed results, never destinations, credentials
// or raw upload errors. It is separate from the application's control database.
type Run struct {
	OperationID      string     `json:"operation_id"`
	Status           string     `json:"status"`
	Phase            string     `json:"phase"`
	StartedAt        time.Time  `json:"started_at"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	Bytes            int64      `json:"bytes"`
	VerifiedReadback bool       `json:"verified_readback"`
}
type Report struct {
	Version int   `json:"version"`
	Runs    []Run `json:"runs"`
}

const reportFile = "scheduled-backup-status.json"

func ReadReport(dataDir string) (Report, error) {
	var report Report
	if dataDir == "" {
		return report, os.ErrNotExist
	}
	path := filepath.Join(dataDir, reportFile)
	info, err := os.Lstat(path)
	if err != nil {
		return report, err
	}
	if !info.Mode().IsRegular() || info.Size() > 65536 {
		return report, fmt.Errorf("invalid backup report")
	}
	f, err := os.Open(path)
	if err != nil {
		return report, err
	}
	defer f.Close()
	if err = json.NewDecoder(io.LimitReader(f, 65537)).Decode(&report); err != nil {
		return Report{}, err
	}
	if report.Version != 1 || len(report.Runs) > 10 {
		return Report{}, fmt.Errorf("unsupported backup report")
	}
	return report, nil
}

func WriteRun(dataDir string, run Run) error {
	report, err := ReadReport(dataDir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	report.Version = 1
	runs := []Run{run}
	for _, old := range report.Runs {
		if old.OperationID != run.OperationID && len(runs) < 10 {
			runs = append(runs, old)
		}
	}
	report.Runs = runs
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	// No secret data; the non-root application must be able to read the receiver's report.
	f, err := os.CreateTemp(dataDir, ".backup-status-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0644); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dataDir, reportFile))
}
