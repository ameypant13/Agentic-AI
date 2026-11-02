package logrules

import (
	"os"
	"strings"
	"testing"
)

// BenchmarkScanRealLog scans a real log file: LOGRULES_SAMPLE=<file> go test -bench ScanRealLog
func BenchmarkScanRealLog(b *testing.B) {
	raw, err := os.ReadFile(os.Getenv("LOGRULES_SAMPLE"))
	if err != nil {
		b.Skip("no sample log")
	}
	lines := strings.Split(string(raw), "\n")
	f, _ := Defaults()
	set, _ := Compile(f.Rules, f.Formats)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		set.Scan("app-prophecy-5898ddc84f-rkdff", lines)
	}
}
