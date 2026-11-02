package store

import (
	"fmt"

	"gorm.io/gorm"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSQLitePragmas(t *testing.T) {
	st, err := Open("file:" + filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	var temp, busy int
	var journal string
	st.db.Raw("PRAGMA temp_store").Scan(&temp)
	st.db.Raw("PRAGMA busy_timeout").Scan(&busy)
	st.db.Raw("PRAGMA journal_mode").Scan(&journal)
	if temp != 2 || busy != 10000 || journal != "wal" {
		t.Errorf("temp_store=%d busy_timeout=%d journal_mode=%s", temp, busy, journal)
	}
	if got := sqliteDSN("file:x.db?_pragma=busy_timeout(5000)"); got != "file:x.db?_pragma=busy_timeout(5000)&_pragma=temp_store(2)&_pragma=journal_mode(WAL)&_txlock=immediate" {
		t.Errorf("dsn = %s", got)
	}
}

// Workers write concurrently: cost days in transactions, sessions, audit rows.
// None of them may fail with SQLITE_BUSY or wedge a connection.
func TestSQLiteConcurrentWriters(t *testing.T) {
	st, err := Open("file:" + filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 400)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				day := fmt.Sprintf("2026-10-%02d", i%28+1)
				var ct []CostContainerDay
				for c := 0; c < 100; c++ {
					ct = append(ct, CostContainerDay{Day: day, Cluster: fmt.Sprint("c", w), Name: fmt.Sprint("wl", c), Container: "x"})
				}
				if err := st.ReplaceCostDay(&CostClusterDay{Day: day, Cluster: fmt.Sprint("c", w), Hours: 24}, nil, ct); err != nil {
					errs <- err
				}
				if err := st.CreateSession(&Session{ID: fmt.Sprint(w, "-", i), Email: "a@b", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
					errs <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if err := st.CreateSession(&Session{ID: "after", Email: "a@b", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Errorf("write after the burst: %v", err)
	}
}

// A read must not wait for a writer that holds the lock (WAL, several connections).
func TestSQLiteReadDuringWrite(t *testing.T) {
	st, err := Open("file:" + filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	locked, release := make(chan struct{}), make(chan struct{})
	go st.db.Transaction(func(tx *gorm.DB) error {
		tx.Create(&Session{ID: "w", Email: "a@b", ExpiresAt: time.Now().Add(time.Hour)})
		close(locked)
		<-release
		return nil
	})
	<-locked
	done := make(chan error, 1)
	go func() { _, err := st.ListLearnings(""); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(2 * time.Second):
		t.Error("a read waited for the writer")
	}
	close(release)
}

func TestAckBackfilledFromAudit(t *testing.T) {
	st, err := Open("file:" + filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	st.SaveFinding(&Finding{ID: "F-2", Key: "k2", Title: "y", State: "acknowledged", FirstSeenAt: now, LastSeenAt: now})
	st.SaveFinding(&Finding{ID: "F-20", Key: "k20", Title: "z", State: "acknowledged", FirstSeenAt: now, LastSeenAt: now})
	st.Audit("pant.amey@prophecy.io", "acknowledge_finding", "cwan", "ok", "F-2: y")
	st.backfillFindingAcks()
	f, _ := st.GetFinding("F-2")
	if f.AcknowledgedBy != "pant.amey@prophecy.io" || f.AcknowledgedAt == nil {
		t.Errorf("backfilled = %+v", f)
	}
	// "F-2:" must not match F-20's audit-less acknowledgement.
	if g, _ := st.GetFinding("F-20"); g.AcknowledgedBy != "" {
		t.Errorf("F-20 = %+v", g)
	}
}
