package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "github.com/lib/pq"
	"github.com/cpradmin/prompts-mcp/tools"
)

func main() {
	var (
		memoryDir  = flag.String("memory-dir", filepath.Join(os.Getenv("HOME"), ".claude/projects/-home-kntrnjb/memory"), "Path to memory directory")
		dbHost     = flag.String("db-host", "10.174.210.22", "Postgres host (Savera on LAN)")
		dbPort     = flag.String("db-port", "5432", "Postgres port")
		dbName     = flag.String("db-name", "ember", "Database name")
		dbUser     = flag.String("db-user", "postgres", "Database user")
		dbPassword = flag.String("db-pass", os.Getenv("POSTGRES_PASSWORD"), "Database password")
		action     = flag.String("action", "extract", "Action: extract|load|report")
	)
	flag.Parse()

	// Extract patterns from memory files
	fmt.Println("🔍 Extracting patterns from memory files...")
	extractor := tools.NewMemoryExtractor(*memoryDir)
	patterns, err := extractor.ExtractAll()
	if err != nil {
		log.Fatalf("Error extracting patterns: %v", err)
	}

	fmt.Printf("✅ Extracted %d patterns from %s\n", len(patterns), *memoryDir)

	// Group by type
	byType := make(map[string]int)
	byDomain := make(map[string]int)
	for _, p := range patterns {
		byType[p.PatternType]++
		byDomain[p.Domain]++
	}

	fmt.Println("\nBy Type:")
	for t, count := range byType {
		fmt.Printf("  %s: %d\n", t, count)
	}
	fmt.Println("\nBy Domain:")
	for d, count := range byDomain {
		fmt.Printf("  %s: %d\n", d, count)
	}

	if *action == "extract" {
		// Just report, don't load
		return
	}

	// Connect to Postgres
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		*dbHost, *dbPort, *dbUser, *dbPassword, *dbName)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("Error connecting to Postgres: %v", err)
	}
	defer db.Close()

	// Verify connection
	if err := db.Ping(); err != nil {
		log.Fatalf("Error pinging Postgres: %v", err)
	}
	fmt.Println("\n✅ Connected to Postgres")

	if *action == "load" {
		// Load patterns into database
		fmt.Println("\n📝 Loading patterns into database...")
		loaded := 0
		skipped := 0

		for _, p := range patterns {
			query := `
				INSERT INTO prompts_training.patterns
				(domain, pattern_name, pattern_text, pattern_type, source_file, source_section, confidence)
				VALUES ($1, $2, $3, $4, $5, $6, 0.5)
				ON CONFLICT (pattern_name, domain) DO NOTHING
			`

			result, err := db.Exec(query, p.Domain, p.PatternName, p.PatternText,
				p.PatternType, p.SourceFile, p.SourceSection)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error inserting pattern %s: %v\n", p.PatternName, err)
				continue
			}

			rows, err := result.RowsAffected()
			if err == nil && rows > 0 {
				loaded++
			} else {
				skipped++
			}
		}

		fmt.Printf("\n✅ Loaded %d patterns, skipped %d duplicates\n", loaded, skipped)
	}

	if *action == "report" {
		// Generate a report for Selah
		fmt.Println("\n📊 Generating report for Selah...")
		query := `
			SELECT domain, pattern_type, COUNT(*) as count
			FROM prompts_training.patterns
			GROUP BY domain, pattern_type
			ORDER BY domain, pattern_type
		`

		rows, err := db.Query(query)
		if err != nil {
			log.Fatalf("Error querying report: %v", err)
		}
		defer rows.Close()

		fmt.Println("\nPatterns in Database:")
		for rows.Next() {
			var domain, ptype string
			var count int
			if err := rows.Scan(&domain, &ptype, &count); err != nil {
				log.Fatal(err)
			}
			fmt.Printf("  %s / %s: %d patterns\n", domain, ptype, count)
		}
	}
}
