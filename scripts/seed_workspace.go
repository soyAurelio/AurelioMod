package main

import (
	"database/sql"
	"fmt"
	"os"
	_ "github.com/lib/pq"
)

func main() {
	apiKey := "ws_test_" + os.Args[1]
	dbURL := "NEON_DATABASE_URL_REDACTED"
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}
	defer db.Close()
	
	var id, name, plan string
	err = db.QueryRow(`INSERT INTO workspaces (name, api_key, plan) 
		VALUES ('Test Server', $1, 'bronze')
		RETURNING id, name, api_key, plan`, apiKey).Scan(&id, &name, &apiKey, &plan)
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}
	fmt.Printf("ID=%s\nNAME=%s\nAPI_KEY=%s\nPLAN=%s\n", id, name, apiKey, plan)
}
