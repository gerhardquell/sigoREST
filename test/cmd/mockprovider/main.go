//**********************************************************************
//      test/cmd/mockprovider/main.go
//**********************************************************************
//  Standalone Mock-Provider für manuelle sigoREST-Tests.
//  Start: go run ./test/cmd/mockprovider -port 18080 -rps 2
//  Dann in channels.json einen Kanal auf http://localhost:18080 zeigen
//  lassen und sigoREST normal betreiben.
//**********************************************************************

package main

import (
	"flag"
	"fmt"
	"net/http"
	"time"

	"sigorest/test/mockprovider"
)

func main() {
	port := flag.Int("port", 18080, "Port für Mock-Server")
	rps := flag.Int("rps", 2, "Erlaubte Requests pro Sekunde (echte Provider simulieren)")
	flag.Parse()

	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	fmt.Printf("Mock-Provider auf http://%s  (limit: %d req/s)\n", addr, *rps)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mockprovider.NewStandalone(*rps),
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Println("Fehler:", err)
	}
}
