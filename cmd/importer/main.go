package main

import (
	"encoding/json"
	"fmt"
	"os"
	"alibaba-router/store"
)

type resultEntry struct {
	Email   string `json:"email"`
	APIKey  string `json:"api_key"`
}

func main() {
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Println("read err:", err)
		os.Exit(1)
	}
	var entries []resultEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		fmt.Println("parse err:", err)
		os.Exit(1)
	}
	s, err := store.Open("data")
	if err != nil {
		fmt.Println("open store:", err)
		os.Exit(1)
	}
	defer s.Close()
	var imported, skipped int
	for _, e := range entries {
		if e.APIKey == "" {
			skipped++
			continue
		}
		_, err := s.ImportAccount(e.Email, e.APIKey, "results.json")
		if err != nil {
			fmt.Printf("skip %s: %v\n", e.Email, err)
			skipped++
			continue
		}
		imported++
	}
	fmt.Printf("Imported: %d | Skipped: %d | Total in file: %d\n", imported, skipped, len(entries))
	accs, _ := s.ListAccounts()
	fmt.Printf("Accounts in DB: %d\n", len(accs))
}
