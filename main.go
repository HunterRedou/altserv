package main 

import (
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"database/sql"
	"os"

	"github.com/HunterRedou/altserv/internal/db"
	"github.com/HunterRedou/altserv/internal/evatr"
	"github.com/joho/godotenv"

	_ "github.com/lib/pq"
)

type apiConfig struct {
	fileserverHits atomic.Int32
	db *db.Queries
	platform string
	evatr *evatr.Client
	reqVATID string
}

func main(){

	const home = "."
	const port = "8080"
	
	godotenv.Load()
	dbUrl := os.Getenv("DB_URL")
	if dbUrl == ""{
		fmt.Printf("DB_URL must be set")
		return
	}

	dbConn, err := sql.Open("postgres", dbUrl)
	if err != nil{
		fmt.Printf("Cannot open database")
		return 
	}
	defer dbConn.Close()

	dbQueries := db.New(dbConn)

	platformType := os.Getenv("PLATFORM")
	if platformType == ""{
		fmt.Printf("Platformsetup is required")
		return
	}

	reqVATID := os.Getenv("EVATR_ID")
	if reqVATID == ""{
		fmt.Printf("EVATR_ID must be set")
		return
	}

	apiCfg := apiConfig{
		fileserverHits: atomic.Int32{},
		db: dbQueries,
		platform: platformType,
		evatr: evatr.NewClient(),
		reqVATID: reqVATID,
	}


	mux := http.NewServeMux()
	mux.Handle("/app/", apiCfg.middlewareMetricsInc(http.StripPrefix("/app", http.FileServer(http.Dir(home)))))
	mux.HandleFunc("POST /admin/reset", apiCfg.handlerReset)
	mux.HandleFunc("POST /api/users", apiCfg.handlerUser)
	mux.HandleFunc("GET /api/users", apiCfg.handlerGetUsers)
	mux.HandleFunc("POST /api/firms", apiCfg.handlerFirm)

	srv := &http.Server{
		Addr:	":" + port,
		Handler: mux,
	}

	log.Printf("Serving on port: %s\n", home, port)
	log.Fatal(srv.ListenAndServe())
}

