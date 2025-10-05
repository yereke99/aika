package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/xuri/excelize/v2"
)

func main() {
	dbPath := flag.String("db", "./aika.db", "path to SQLite DB")
	xlsxPath := flag.String("xlsx", "./document/just_users.xlsx", "path to Excel file")
	flag.Parse()

	db, err := sql.Open("sqlite3", *dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		log.Fatalf("ping db: %v", err)
	}

	// create if not exists (does NOT alter existing schema)
	if err := createJustTable(db); err != nil {
		log.Fatalf("createJustTable: %v", err)
	}

	if err := migrateExcelToJust(db, *xlsxPath); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	log.Println("Migration finished.")
}

// --- your exact schema (unchanged) ---
func createJustTable(db *sql.DB) error {
	const stmt = `
	CREATE TABLE IF NOT EXISTS just (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		id_user BIGINT NOT NULL UNIQUE,
		userName VARCHAR(255) NOT NULL,
		dataRegistred VARCHAR(50) NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	`
	_, err := db.Exec(stmt)
	return err
}

func migrateExcelToJust(db *sql.DB, xlsxPath string) error {
	const skipID int64 = 6391833468

	info, err := os.Stat(xlsxPath)
	if err != nil {
		return fmt.Errorf("stat xlsx: %w", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("xlsx is empty")
	}

	f, err := excelize.OpenFile(xlsxPath)
	if err != nil {
		return fmt.Errorf("open xlsx: %w", err)
	}
	defer f.Close()

	sheet := f.GetSheetName(0)
	if sheet == "" {
		return fmt.Errorf("no sheet found")
	}

	rows, err := f.GetRows(sheet)
	if err != nil {
		return fmt.Errorf("get rows: %w", err)
	}
	if len(rows) == 0 {
		return fmt.Errorf("sheet is empty")
	}

	// Build a normalized header index: lowercased, non-alnum removed
	norm := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(s))
		var b strings.Builder
		for _, r := range s {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			}
		}
		return b.String()
	}

	header := rows[0]
	colIndex := make(map[string]int)
	for i, h := range header {
		colIndex[norm(h)] = i
	}

	findIdx := func(cands ...string) (int, bool) {
		for _, c := range cands {
			if idx, ok := colIndex[norm(c)]; ok {
				return idx, true
			}
		}
		return -1, false
	}

	// Accept common variants (with/without spaces/underscores)
	idIdx, ok1 := findIdx("id_user", "user_id", "User ID", "userid", "iduser", "telegram_id", "tg_id")
	userIdx, ok2 := findIdx("userName", "username", "User Name", "user name", "nickname")
	dateIdx, ok3 := findIdx("dataRegistred", "dataRegistered", "Date Registered", "date_registered", "registration_date")

	if !(ok1 && ok2 && ok3) {
		var seen []string
		for k := range colIndex {
			seen = append(seen, k)
		}
		return fmt.Errorf("required headers not found. Need User ID, Username, Date Registered. Seen(normalized): %v", seen)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO just (id_user, userName, dataRegistred) VALUES (?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	var inserted, ignored, skipped int
	nowStr := time.Now().Format("2006-01-02 15:04:05")

	for r := 1; r < len(rows); r++ {
		row := rows[r]
		get := func(i int) string {
			if i < 0 || i >= len(row) {
				return ""
			}
			return strings.TrimSpace(row[i])
		}

		idStr := get(idIdx)
		if idStr == "" {
			skipped++
			continue
		}

		idVal, ok := parseID(idStr)
		if !ok || idVal == 0 {
			skipped++
			continue
		}
		if idVal == skipID {
			skipped++
			continue
		}

		userName := get(userIdx)
		if userName == "" {
			userName = "-"
		}
		dataReg := get(dateIdx)
		if dataReg == "" {
			dataReg = nowStr
		}

		res, err := stmt.Exec(idVal, userName, dataReg)
		if err != nil {
			return fmt.Errorf("insert row %d (id_user=%d): %w", r+1, idVal, err)
		}
		if aff, _ := res.RowsAffected(); aff == 1 {
			inserted++
		} else {
			ignored++
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	log.Printf("migrate summary -> inserted: %d, ignored(dedup): %d, skipped: %d", inserted, ignored, skipped)
	return nil
}

// robust id parsing for excel values (text/number/scientific)
func parseID(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, true
	}
	if f, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64); err == nil {
		return int64(math.Round(f)), true
	}
	var b strings.Builder
	for i, r := range s {
		if (r >= '0' && r <= '9') || (r == '-' && i == 0) {
			b.WriteRune(r)
		}
	}
	clean := b.String()
	if clean == "" {
		return 0, false
	}
	if n, err := strconv.ParseInt(clean, 10, 64); err == nil {
		return n, true
	}
	return 0, false
}
